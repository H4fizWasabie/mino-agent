package main

import (
	"database/sql"
	"fmt"
	"time"
)

type Responsibility struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	Title        string     `json:"title"`
	Outcome      string     `json:"outcome"`
	Owner        string     `json:"owner"`
	Status       string     `json:"status"`
	NextAction   string     `json:"next_action"`
	NextOwner    string     `json:"next_owner"`
	DueAt        *time.Time `json:"due_at,omitempty"`
	LastRunAt    *time.Time `json:"last_run_at,omitempty"`
	Schedule     string     `json:"schedule"`
	SourceKind   string     `json:"source_kind"`
	SourceRef    string     `json:"source_ref"`
	Verification string     `json:"verification"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type ResponsibilityEvent struct {
	ResponsibilityID string     `json:"responsibility_id"`
	Type             string     `json:"type"`
	Kind             string     `json:"kind,omitempty"`
	Title            string     `json:"title,omitempty"`
	Outcome          string     `json:"outcome"`
	Owner            string     `json:"owner"`
	Status           string     `json:"status"`
	NextAction       string     `json:"next_action"`
	NextOwner        string     `json:"next_owner"`
	Summary          string     `json:"summary"`
	Evidence         string     `json:"evidence"`
	DueAt            *time.Time `json:"due_at,omitempty"`
	LastRunAt        *time.Time `json:"last_run_at,omitempty"`
	Schedule         string     `json:"schedule"`
	SourceKind       string     `json:"source_kind,omitempty"`
	SourceRef        string     `json:"source_ref,omitempty"`
	Verification     string     `json:"verification"`
	At               time.Time  `json:"at"`
}

type ResponsibilityFilter struct{ Kind, Status string }

type ResponsibilityStore struct{ db *sql.DB }

func NewResponsibilityStore(db *sql.DB) *ResponsibilityStore {
	return &ResponsibilityStore{db: db}
}

func (s *ResponsibilityStore) Record(event ResponsibilityEvent) (Responsibility, error) {
	if event.ResponsibilityID == "" || event.Status == "" || event.Type == "" || event.Summary == "" {
		return Responsibility{}, fmt.Errorf("responsibility id, status, event type, and summary are required")
	}
	if !validResponsibilityStatus(event.Status) {
		return Responsibility{}, fmt.Errorf("invalid responsibility status %q", event.Status)
	}
	if event.Status == "verified" && event.Evidence == "" {
		return Responsibility{}, fmt.Errorf("verified responsibility requires evidence")
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Responsibility{}, err
	}
	defer tx.Rollback()
	at := event.At.UTC().Format(time.RFC3339Nano)
	var due any
	if event.DueAt != nil {
		due = event.DueAt.UTC().Format(time.RFC3339Nano)
	}
	var lastRun any
	if event.LastRunAt != nil {
		lastRun = event.LastRunAt.UTC().Format(time.RFC3339Nano)
	}
	current, getErr := scanResponsibility(tx.QueryRow(`SELECT id, kind, title, outcome, owner,
		status, next_action, next_owner, due_at, last_run_at, schedule, source_kind, source_ref,
		verification, created_at, updated_at FROM responsibilities WHERE id = ?`,
		event.ResponsibilityID))
	switch {
	case getErr == sql.ErrNoRows:
		if event.Kind == "" || event.Title == "" || event.Owner == "" {
			return Responsibility{}, fmt.Errorf("kind, title, and owner are required for a new responsibility")
		}
		if event.Outcome == "" {
			event.Outcome = event.Title
		}
		if _, err = tx.Exec(`INSERT INTO responsibilities
			(id, kind, title, outcome, owner, status, next_action, next_owner, due_at, last_run_at, schedule, source_kind, source_ref, verification, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			event.ResponsibilityID, event.Kind, event.Title, event.Outcome, event.Owner, event.Status,
			event.NextAction, event.NextOwner, due, lastRun, event.Schedule, event.SourceKind,
			event.SourceRef, event.Verification, at, at); err != nil {
			return Responsibility{}, err
		}
	case getErr != nil:
		return Responsibility{}, getErr
	default:
		if !validResponsibilityTransition(current.Kind, current.Status, event.Status) {
			return Responsibility{}, fmt.Errorf("cannot move responsibility from %q to %q", current.Status, event.Status)
		}
		if event.Outcome == "" {
			event.Outcome = current.Outcome
		}
		if event.Owner == "" {
			event.Owner = current.Owner
		}
		if event.NextAction == "" {
			event.NextAction = current.NextAction
		}
		if event.NextOwner == "" {
			event.NextOwner = current.NextOwner
		}
		if event.Verification == "" {
			event.Verification = current.Verification
		}
		if event.Schedule == "" {
			event.Schedule = current.Schedule
		}
		if due == nil && current.DueAt != nil {
			due = current.DueAt.UTC().Format(time.RFC3339Nano)
		}
		if lastRun == nil && current.LastRunAt != nil {
			lastRun = current.LastRunAt.UTC().Format(time.RFC3339Nano)
		}
		if _, err = tx.Exec(`UPDATE responsibilities SET outcome = ?, owner = ?, status = ?,
			next_action = ?, next_owner = ?, due_at = ?, last_run_at = ?, schedule = ?,
			verification = ?, updated_at = ? WHERE id = ?`,
			event.Outcome, event.Owner, event.Status, event.NextAction, event.NextOwner, due,
			lastRun, event.Schedule, event.Verification, at, event.ResponsibilityID); err != nil {
			return Responsibility{}, err
		}
	}
	if _, err = tx.Exec(`INSERT INTO responsibility_events
		(responsibility_id, event_type, outcome, owner, status, next_action, next_owner,
		due_at, last_run_at, schedule, verification, summary, evidence, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, event.ResponsibilityID, event.Type,
		event.Outcome, event.Owner, event.Status, event.NextAction, event.NextOwner, due,
		lastRun, event.Schedule, event.Verification, event.Summary, event.Evidence, at); err != nil {
		return Responsibility{}, err
	}
	if err = tx.Commit(); err != nil {
		return Responsibility{}, err
	}
	return s.get(event.ResponsibilityID)
}

func validResponsibilityStatus(status string) bool {
	switch status {
	case "needs_you", "working", "waiting", "blocked", "verified", "stopped":
		return true
	}
	return false
}

func validResponsibilityTransition(kind, from, to string) bool {
	// Routines are recurring by nature: a routine closed with "stopped" (e.g.
	// manual close during cleanup) must be restarted by its next scheduled
	// fire, otherwise the schedule dies silently forever (2026-08-07: all
	// three night playbooks stopped firing after a manual close).
	if kind == "routine" && to == "working" && (from == "verified" || from == "stopped") {
		return true
	}
	if from == "verified" || from == "stopped" {
		return from == to
	}
	return validResponsibilityStatus(to)
}
