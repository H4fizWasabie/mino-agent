package main

// Scheduler — DECISIONS.md §5: cron engine for proactive prompts.
// ~/.mino/schedule.json:
//   [{"id":"morning-brief","schedule":"07:00","prompt":"Brief me on today","notify":true}]
//
// ponytail: simple HH:MM schedule only, no robfig/cron dependency.
// Add cron expressions via robfig/cron if needed.

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ScheduledJob matches ~/.mino/schedule.json
type ScheduledJob struct {
	ID       string `json:"id"`
	Schedule string `json:"schedule"` // "HH:MM" or "every Nm" e.g. "every 30m"
	Prompt   string `json:"prompt"`
	Notify   bool   `json:"notify"`
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
}

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
	s.mu.Lock()
	s.jobs = jobs
	s.mu.Unlock()
	slog.Info("schedule loaded", "jobs", len(jobs))
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
		}
	}
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
