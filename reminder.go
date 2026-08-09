package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func makeReminderTools(db *sql.DB, location *time.Location) []*Tool {
	return []*Tool{
		makeCreateReminderTool(db, location),
		makeListRemindersTool(db, location),
		makeCancelReminderTool(db),
	}
}

func makeCreateReminderTool(db *sql.DB, location *time.Location) *Tool {
	return &Tool{
		Name:        "create_reminder",
		Description: "Create a one-time reminder that Mino will send to the owner's Telegram chat. Resolve relative dates using the configured timezone and provide an ISO 8601 time. Use for meetings, appointments, and deadlines the owner wants to be reminded about.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message":   map[string]any{"type": "string", "description": "What to remind the user about"},
				"remind_at": map[string]any{"type": "string", "description": "When to send it: ISO 8601 with timezone, for example 2026-07-28T09:00:00+08:00"},
			},
			"required": []string{"message", "remind_at"},
		},
		Fn: func(args map[string]any) string {
			message, _ := args["message"].(string)
			remindAt, _ := args["remind_at"].(string)
			message = strings.TrimSpace(message)
			when, err := parseReminderTime(remindAt, location)
			if message == "" {
				return "Error: reminder message is required"
			}
			if err != nil {
				return fmt.Sprintf("Error: remind_at must be ISO 8601 or YYYY-MM-DD HH:MM in the configured timezone: %v", err)
			}
			if !when.After(time.Now()) {
				return "Error: reminder time must be in the future"
			}
			result, err := db.Exec("INSERT INTO reminders (message, remind_at) VALUES (?, ?)", message, when.UTC().Format(time.RFC3339))
			if err != nil {
				return fmt.Sprintf("Error creating reminder: %v", err)
			}
			id, _ := result.LastInsertId()
			return fmt.Sprintf("Created reminder #%d for %s: %s — stored in system reminders (SQLite), NOT your calendar", id, when.In(location).Format("2006-01-02 15:04 MST"), message)
		},
	}
}

func makeListRemindersTool(db *sql.DB, location *time.Location) *Tool {
	return &Tool{
		Name:        "list_reminders",
		Description: "List pending one-time reminders in the configured timezone. Use when asked about a meeting, appointment, or deadline that Mino was asked to remind about — reminders are system reminders, NOT calendar events.",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Fn: func(map[string]any) string {
			rows, err := db.Query("SELECT id, message, remind_at FROM reminders WHERE status = 'pending' ORDER BY remind_at LIMIT 50")
			if err != nil {
				return "No reminders found."
			}
			defer rows.Close()
			var out strings.Builder
			for rows.Next() {
				var id int64
				var message, remindAt string
				if rows.Scan(&id, &message, &remindAt) != nil {
					continue
				}
				when, _ := time.Parse(time.RFC3339, remindAt)
				fmt.Fprintf(&out, "- #%d at %s: %s\n", id, when.In(location).Format("2006-01-02 15:04 MST"), message)
			}
			if out.Len() == 0 {
				return "No pending reminders."
			}
			return "Pending reminders:\n" + out.String()
		},
	}
}

func makeCancelReminderTool(db *sql.DB) *Tool {
	return &Tool{
		Name:        "cancel_reminder",
		Description: "Cancel a pending reminder by its numeric ID.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "integer", "description": "Reminder ID from create_reminder or list_reminders"},
			},
			"required": []string{"id"},
		},
		Fn: func(args map[string]any) string {
			id, ok := reminderID(args["id"])
			if !ok || id <= 0 {
				return "Error: reminder id must be a positive integer"
			}
			result, err := db.Exec("UPDATE reminders SET status = 'cancelled' WHERE id = ? AND status = 'pending'", id)
			if err != nil {
				return fmt.Sprintf("Error cancelling reminder: %v", err)
			}
			changed, _ := result.RowsAffected()
			if changed == 0 {
				return fmt.Sprintf("No pending reminder found with id %d", id)
			}
			return fmt.Sprintf("Cancelled reminder #%d (status: cancelled, system reminders, SQLite)", id)
		},
	}
}

func parseReminderTime(raw string, location *time.Location) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04", "2006-01-02T15:04"} {
		if layout == time.RFC3339 {
			if parsed, err := time.Parse(layout, raw); err == nil {
				return parsed, nil
			}
			continue
		}
		if parsed, err := time.ParseInLocation(layout, raw, location); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time %q", raw)
}

func reminderID(raw any) (int64, bool) {
	switch value := raw.(type) {
	case float64:
		return int64(value), value == float64(int64(value))
	case int:
		return int64(value), true
	case int64:
		return value, true
	default:
		return 0, false
	}
}

func runReminderDispatcher(core *Core) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		dispatchDueReminders(core)
	}
}

// dispatchDueReminders delivers due reminders via the raw-HTTP Telegram path
// (same as the outbox) so it works in any gateway mode and survives bot-init
// failures. No delivery channel configured → reminders stay pending.
func dispatchDueReminders(core *Core) {
	if core.Settings.Telegram == "" || core.Settings.TelegramChatID <= 0 {
		return
	}
	rows, err := core.DB.Query("SELECT id, message FROM reminders WHERE status = 'pending' AND remind_at <= ? ORDER BY remind_at LIMIT 20", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return
	}
	// Collect first, then close: SetMaxOpenConns(1) means an open rows set
	// blocks any Exec (delivery/UPDATE) on the same DB.
	var dueReminders []struct {
		id      int64
		message string
	}
	for rows.Next() {
		var d struct {
			id      int64
			message string
		}
		if rows.Scan(&d.id, &d.message) != nil {
			continue
		}
		dueReminders = append(dueReminders, d)
	}
	rows.Close()
	for _, d := range dueReminders {
		reply := "⏰ Reminder: " + d.message
		if !sendTelegramText(core.Settings.Telegram, core.Settings.TelegramChatID, reply, true) {
			continue
		}
		core.recordTelegramNotification(core.Settings.TelegramChatID, reply)
		core.DB.Exec("UPDATE reminders SET status = 'delivered', delivered_at = datetime('now') WHERE id = ? AND status = 'pending'", d.id)
	}
}
