package main

// Scheduler — DECISIONS.md §5: cron engine for proactive prompts.
// ~/.mino/schedule.json:
//   [{"id":"morning-brief","schedule":"07:00","prompt":"Brief me on today","notify":true}]
//
// ponytail: simple HH:MM schedule only, no robfig/cron dependency.
// Schedule format: "HH:MM" (daily at that UTC time) or "every Nh" / "every Nm".
// Add cron expressions via robfig/cron if needed.

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ScheduledJob matches ~/.mino/schedule.json
type ScheduledJob struct {
	ID       string `json:"id"`
	Schedule string `json:"schedule"` // "HH:MM" or "every Nm" e.g. "every 30m"
	Prompt   string `json:"prompt"`
	Notify   bool   `json:"notify"`
	Once     bool   `json:"once,omitempty"`
}

// Scheduler runs scheduled prompts and forwards results.
type Scheduler struct {
	home     string
	location *time.Location
	callback func(prompt string, notify bool)
	jobs     []ScheduledJob
	lastRun  map[string]time.Time
	mu       sync.Mutex
	stopCh   chan struct{}
	snapshot string
}

var scheduleFileMu sync.Mutex

// NewScheduler creates a scheduler. callback fires when a job is due.
func NewScheduler(home string, location *time.Location, callback func(prompt string, notify bool)) *Scheduler {
	return &Scheduler{
		home:     home,
		location: location,
		callback: callback,
		lastRun:  make(map[string]time.Time),
		stopCh:   make(chan struct{}),
	}
}

// loadJobs reads schedule.json
func (s *Scheduler) loadJobs() {
	path := filepath.Join(s.home, "schedule.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var jobs []ScheduledJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		slog.Warn("bad schedule.json", "error", err)
		return
	}
	valid := jobs[:0]
	seen := make(map[string]bool)
	for _, job := range jobs {
		switch {
		case !safeActionID(job.ID):
			slog.Warn("invalid scheduled job id", "id", job.ID)
		case seen[job.ID]:
			slog.Warn("duplicate scheduled job id", "id", job.ID)
		case validateSchedule(job.Schedule) != nil:
			slog.Warn("invalid scheduled job", "id", job.ID, "schedule", job.Schedule)
		default:
			seen[job.ID] = true
			valid = append(valid, job)
		}
	}
	s.mu.Lock()
	if string(data) == s.snapshot {
		s.mu.Unlock()
		return
	}
	s.jobs = append([]ScheduledJob(nil), valid...)
	s.snapshot = string(data)
	s.mu.Unlock()
	slog.Info("schedule loaded", "jobs", len(valid))
}

// Start begins the ticker loop (checks every 30s)
func (s *Scheduler) Start() {
	s.loadJobs()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.tick()
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop the scheduler
func (s *Scheduler) Stop() {
	close(s.stopCh)
}

func (s *Scheduler) tick() {
	CleanupArtifacts(24 * time.Hour)
	s.loadJobs() // reload in case schedule.json was modified

	// extension health check
	for _, alert := range CheckExtensions(s.home) {
		slog.Warn("extension alert", "alert", alert)
	}

	s.mu.Lock()
	jobs := make([]ScheduledJob, len(s.jobs))
	copy(jobs, s.jobs)
	s.mu.Unlock()

	location := s.location
	if location == nil {
		location = time.Local
	}
	now := time.Now().In(location)
	for _, j := range jobs {
		if s.shouldRun(j, now) {
			s.lastRun[j.ID] = now
			slog.Info("scheduled job triggered", "id", j.ID)
			if s.callback != nil {
				s.callback(j.Prompt, j.Notify)
			}
			if j.Once {
				s.remove(j.ID)
			}
		}
	}
}

func validateSchedule(schedule string) error {
	if strings.HasPrefix(schedule, "every ") {
		duration, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(schedule, "every ")))
		if err != nil || duration <= 0 {
			return fmt.Errorf("invalid interval")
		}
		return nil
	}
	if _, err := time.Parse("15:04", schedule); err != nil {
		return fmt.Errorf("expected HH:MM or every duration")
	}
	return nil
}

func (s *Scheduler) remove(id string) {
	scheduleFileMu.Lock()
	defer scheduleFileMu.Unlock()
	s.mu.Lock()
	jobs := make([]ScheduledJob, 0, len(s.jobs))
	for _, job := range s.jobs {
		if job.ID != id {
			jobs = append(jobs, job)
		}
	}
	if writeScheduleFile(s.home, jobs) == nil {
		s.jobs = jobs
		s.snapshot = ""
		delete(s.lastRun, id)
	}
	s.mu.Unlock()
}

func writeScheduleFile(home string, jobs []ScheduledJob) error {
	path := filepath.Join(home, "schedule.json")
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(home, "schedule-*.json")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0644); err == nil {
		_, err = file.Write(data)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (s *Scheduler) shouldRun(j ScheduledJob, now time.Time) bool {
	last, ok := s.lastRun[j.ID]

	// "every Nm" format
	if len(j.Schedule) > 6 && j.Schedule[:5] == "every" {
		d, err := time.ParseDuration(string(j.Schedule[6:]))
		if err != nil {
			return false
		}
		if ok && now.Sub(last) < d {
			return false
		}
		// first run: wait one duration from now
		if !ok {
			s.lastRun[j.ID] = now
			return false
		}
		return true
	}

	// "HH:MM" format (daily)
	t, err := time.Parse("15:04", j.Schedule)
	if err != nil {
		return false
	}
	scheduled := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if ok {
		lastDay := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, last.Location())
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, last.Location())
		if !lastDay.Before(today) {
			return false // already ran today
		}
	}
	// fire within 1 minute of scheduled time
	diff := now.Sub(scheduled)
	return diff >= 0 && diff < time.Minute
}
