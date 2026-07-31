package main

import (
	"database/sql"
	"time"
)

type ResponsibilityEntry struct {
	Responsibility
	Latest ResponsibilityEvent `json:"latest"`
}

type ResponsibilityViews struct {
	Today []ResponsibilityEntry `json:"today"`
	Work  []ResponsibilityEntry `json:"work"`
	Error string                `json:"error,omitempty"`
}

type ResponsibilityDetail struct {
	Responsibility
	History []ResponsibilityEvent `json:"history"`
}

func (s *ResponsibilityStore) Views(now time.Time, location *time.Location) (ResponsibilityViews, error) {
	if location == nil {
		location = time.UTC
	}
	items, err := s.List(ResponsibilityFilter{})
	if err != nil {
		return ResponsibilityViews{}, err
	}
	views := ResponsibilityViews{
		Today: []ResponsibilityEntry{},
		Work:  []ResponsibilityEntry{},
	}
	today := now.In(location)
	for _, item := range items {
		history, err := s.History(item.ID)
		if err != nil {
			return views, err
		}
		if len(history) == 0 {
			continue
		}
		entry := ResponsibilityEntry{Responsibility: item, Latest: history[len(history)-1]}
		if item.Kind != "system" {
			views.Work = append(views.Work, entry)
		}
		at := entry.Latest.At.In(location)
		if entry.Latest.Type != "imported" &&
			at.Year() == today.Year() && at.YearDay() == today.YearDay() {
			views.Today = append(views.Today, entry)
		}
	}
	return views, nil
}

func (s *ResponsibilityStore) Detail(id string) (ResponsibilityDetail, error) {
	item, err := s.get(id)
	if err != nil {
		return ResponsibilityDetail{}, err
	}
	history, err := s.History(id)
	return ResponsibilityDetail{Responsibility: item, History: history}, err
}

func (s *ResponsibilityStore) List(filter ResponsibilityFilter) ([]Responsibility, error) {
	query := `SELECT id, kind, title, outcome, owner, status, next_action, next_owner, due_at,
		last_run_at, schedule, source_kind, source_ref, verification, created_at, updated_at FROM responsibilities WHERE 1=1`
	var args []any
	if filter.Kind != "" {
		query += " AND kind = ?"
		args = append(args, filter.Kind)
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}
	query += " ORDER BY updated_at DESC, id"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Responsibility
	for rows.Next() {
		item, err := scanResponsibility(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *ResponsibilityStore) History(id string) ([]ResponsibilityEvent, error) {
	rows, err := s.db.Query(`SELECT event_type, outcome, owner, status, next_action,
		next_owner, due_at, last_run_at, schedule, verification, summary, evidence, created_at
		FROM responsibility_events WHERE responsibility_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResponsibilityEvent
	for rows.Next() {
		var event ResponsibilityEvent
		var due, lastRun sql.NullString
		var at string
		if err := rows.Scan(&event.Type, &event.Outcome, &event.Owner, &event.Status,
			&event.NextAction, &event.NextOwner, &due, &lastRun, &event.Schedule,
			&event.Verification, &event.Summary, &event.Evidence, &at); err != nil {
			return nil, err
		}
		event.ResponsibilityID = id
		if event.DueAt, err = parseResponsibilityTime(due); err != nil {
			return nil, err
		}
		if event.LastRunAt, err = parseResponsibilityTime(lastRun); err != nil {
			return nil, err
		}
		if event.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *ResponsibilityStore) get(id string) (Responsibility, error) {
	return scanResponsibility(s.db.QueryRow(`SELECT id, kind, title, outcome, owner, status,
		next_action, next_owner, due_at, last_run_at, schedule, source_kind, source_ref,
		verification, created_at, updated_at FROM responsibilities WHERE id = ?`, id))
}

type rowScanner interface{ Scan(...any) error }

func scanResponsibility(row rowScanner) (Responsibility, error) {
	var item Responsibility
	var due, lastRun sql.NullString
	var created, updated string
	err := row.Scan(&item.ID, &item.Kind, &item.Title, &item.Outcome, &item.Owner, &item.Status,
		&item.NextAction, &item.NextOwner, &due, &lastRun, &item.Schedule, &item.SourceKind,
		&item.SourceRef, &item.Verification, &created, &updated)
	if err != nil {
		return item, err
	}
	if item.DueAt, err = parseResponsibilityTime(due); err != nil {
		return item, err
	}
	if item.LastRunAt, err = parseResponsibilityTime(lastRun); err != nil {
		return item, err
	}
	if item.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return item, err
	}
	if item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return item, err
	}
	return item, nil
}

func parseResponsibilityTime(raw sql.NullString) (*time.Time, error) {
	if !raw.Valid {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339Nano, raw.String)
	return &value, err
}
