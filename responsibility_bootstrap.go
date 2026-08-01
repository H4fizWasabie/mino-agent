package main

import (
	"database/sql"
	"fmt"
	"time"
)

type ResponsibilityBootstrap struct {
	Routines, Reminders int
	AlreadyDone         bool
}

func (s *ResponsibilityStore) Bootstrap(home string, fallback *time.Location, now time.Time) (ResponsibilityBootstrap, error) {
	var marker string
	err := s.db.QueryRow("SELECT value FROM _meta WHERE key = 'responsibility_baseline'").Scan(&marker)
	if err == nil {
		return ResponsibilityBootstrap{AlreadyDone: true}, nil
	}
	if err != sql.ErrNoRows {
		return ResponsibilityBootstrap{}, err
	}
	schedules, err := loadSchedules(home)
	if err != nil {
		return ResponsibilityBootstrap{}, err
	}
	result := ResponsibilityBootstrap{Routines: len(schedules)}
	for _, schedule := range schedules {
		if err := s.importRoutine(home, schedule, fallback, now); err != nil {
			return result, err
		}
	}
	rows, err := s.db.Query(`SELECT id, message, remind_at FROM reminders
		WHERE status = 'pending' ORDER BY id`)
	if err != nil {
		return result, err
	}
	type pendingReminder struct {
		id      int64
		message string
		due     time.Time
	}
	var reminders []pendingReminder
	for rows.Next() {
		var id int64
		var message, rawDue string
		if err := rows.Scan(&id, &message, &rawDue); err != nil {
			rows.Close()
			return result, err
		}
		due, err := time.Parse(time.RFC3339, rawDue)
		if err != nil {
			rows.Close()
			return result, err
		}
		reminders = append(reminders, pendingReminder{id: id, message: message, due: due})
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	for _, reminder := range reminders {
		if err := s.importIfMissing(ResponsibilityEvent{
			ResponsibilityID: fmt.Sprintf("reminder:%d", reminder.id), Type: "imported",
			Kind: "reminder", Title: reminder.message, Outcome: reminder.message, Owner: "mino", Status: "waiting",
			Summary: "Imported from a pending reminder.", SourceKind: "reminder",
			SourceRef: fmt.Sprint(reminder.id), DueAt: &reminder.due, NextAction: "Deliver reminder",
			NextOwner: "mino", At: now,
		}); err != nil {
			return result, err
		}
		result.Reminders++
	}
	summary := fmt.Sprintf("%d %s and %d pending %s imported.",
		result.Routines, plural(result.Routines, "routine", "routines"),
		result.Reminders, plural(result.Reminders, "reminder", "reminders"))
	if err := s.importIfMissing(ResponsibilityEvent{
		ResponsibilityID: "system:journal", Type: "baseline", Kind: "system",
		Title: "Mino responsibility journal started", Owner: "mino", Status: "verified",
		Summary: summary, Evidence: "schedules.json and pending reminders read",
		SourceKind: "migration", SourceRef: "responsibility-v1", At: now,
	}); err != nil {
		return result, err
	}
	if _, err := s.db.Exec(`INSERT INTO _meta (key, value) VALUES
		('responsibility_baseline', ?)`, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return result, err
	}
	return result, nil
}

func (s *ResponsibilityStore) importRoutine(home string, schedule PlaybookSchedule, fallback *time.Location, now time.Time) error {
	location := fallback
	if schedule.Timezone != "" {
		var err error
		location, err = time.LoadLocation(schedule.Timezone)
		if err != nil {
			return err
		}
	}
	if location == nil {
		location = time.UTC
	}
	due, err := nextScheduledRun(schedule.Time, location, now)
	if err != nil {
		return err
	}
	title := schedule.Name
	if playbook, err := loadPlaybookWorkspace(home, schedule.Name); err == nil && playbook.Description != "" {
		title = playbook.Description
	}
	var lastRun *time.Time
	if schedule.LastRun != "" {
		parsed, err := time.Parse(time.RFC3339, schedule.LastRun)
		if err != nil {
			return err
		}
		lastRun = &parsed
	}
	return s.importIfMissing(ResponsibilityEvent{
		ResponsibilityID: "routine:" + schedule.Name, Type: "imported",
		Kind: "routine", Title: title, Outcome: title, Owner: "mino", Status: "waiting",
		Summary: "Imported from the existing schedule.", SourceKind: "schedule",
		SourceRef: schedule.Name, Schedule: schedule.Time + " " + location.String(),
		DueAt: &due, LastRunAt: lastRun, NextAction: "Run scheduled playbook",
		NextOwner: "mino", At: now,
	})
}

func (s *ResponsibilityStore) importIfMissing(event ResponsibilityEvent) error {
	if _, err := s.get(event.ResponsibilityID); err == nil {
		return nil
	} else if err != sql.ErrNoRows {
		return err
	}
	_, err := s.Record(event)
	return err
}

func nextScheduledRun(clock string, location *time.Location, now time.Time) (time.Time, error) {
	value, err := time.ParseInLocation("15:04", clock, location)
	if err != nil {
		return time.Time{}, err
	}
	local := now.In(location)
	next := time.Date(local.Year(), local.Month(), local.Day(), value.Hour(), value.Minute(), 0, 0, location)
	if next.Before(local) {
		next = next.AddDate(0, 0, 1)
	}
	return next.UTC(), nil
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}
