package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// --- Audit schema ---

const auditSchema = `
CREATE TABLE IF NOT EXISTS audit_events (
	id INTEGER PRIMARY KEY,
	session_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	message TEXT NOT NULL,
	iteration INTEGER DEFAULT 0,
	created_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_audit_events_session ON audit_events(session_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_type ON audit_events(event_type);
`

// initAudit ensures the audit_events table exists (called from Connect).
func initAudit(db *sql.DB) {
	if _, err := db.Exec(auditSchema); err != nil {
		panic(fmt.Sprintf("audit schema: %v", err))
	}
}

// --- Write ---

// auditLog records an event to the audit_events table.
func (w *Core) auditLog(sessionID, eventType, message string, iteration int) {
	if w.DB == nil {
		return
	}
	w.DB.Exec(
		"INSERT INTO audit_events (session_id, event_type, message, iteration) VALUES (?, ?, ?, ?)",
		sessionID, eventType, message, iteration,
	)
}

// --- Query tool ---

func makeQueryAuditTool(db *sql.DB) *Tool {
	return &Tool{
		Name:        "query_audit",
		Description: "Query Mino's audit log. Returns recent events (interrupts, loop detections, errors, tool calls) across sessions. Use to answer 'what happened?' questions about Mino's own behavior.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter by session ID (e.g., 'tg:123'). Leave empty for all sessions.",
				},
				"event_type": map[string]any{
					"type":        "string",
					"description": "Filter by event type: interrupt, loop_detected, error, tool_call. Leave empty for all.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum events to return (default 20, max 50).",
				},
				"since": map[string]any{
					"type":        "string",
					"description": "Return events after this time, e.g., '2026-07-28' or '2026-07-28T09:00:00'. Leave empty for all.",
				},
			},
		},
		Behavior: BehaviorObserve,
		Fn: func(args map[string]any) string {
			limit := 20
			if v, ok := args["limit"].(float64); ok && v > 0 {
				limit = int(v)
				if limit > 50 {
					limit = 50
				}
			}

			var conditions []string
			var params []any

			if v, ok := args["session_id"].(string); ok && v != "" {
				conditions = append(conditions, "session_id = ?")
				params = append(params, v)
			}
			if v, ok := args["event_type"].(string); ok && v != "" {
				conditions = append(conditions, "event_type = ?")
				params = append(params, v)
			}
			if v, ok := args["since"].(string); ok && v != "" {
				conditions = append(conditions, "created_at >= ?")
				params = append(params, v)
			}

			where := ""
			if len(conditions) > 0 {
				where = "WHERE " + strings.Join(conditions, " AND ")
			}

			// Query audit_events
			query := fmt.Sprintf(
				"SELECT session_id, event_type, message, iteration, created_at FROM audit_events %s ORDER BY id DESC LIMIT ?",
				where,
			)
			params = append(params, limit)
			rows, err := db.Query(query, params...)
			if err != nil {
				return fmt.Sprintf("Error querying audit log: %v", err)
			}
			defer rows.Close()

			var lines []string
			lines = append(lines, fmt.Sprintf("Audit log (last %d events):", limit))
			count := 0
			for rows.Next() {
				var sessionID, eventType, message, createdAt string
				var iteration int
				if err := rows.Scan(&sessionID, &eventType, &message, &iteration, &createdAt); err != nil {
					continue
				}
				lines = append(lines, fmt.Sprintf("[%s] [%s] %s (session=%s, iter=%d)", createdAt, eventType, message, sessionID, iteration))
				count++
			}
			if count == 0 {
				lines = append(lines, "(no events found)")
			}

			// Also include recent tool errors from tool_calls
			toolRows, err := db.Query(
				"SELECT session_id, tool_name, output_summary, created_at FROM tool_calls WHERE status = 'error' ORDER BY id DESC LIMIT 10",
			)
			if err == nil {
				defer toolRows.Close()
				hasErrors := false
				for toolRows.Next() {
					if !hasErrors {
						lines = append(lines, "\nRecent tool errors:")
						hasErrors = true
					}
					var sessionID, toolName, output, createdAt string
					toolRows.Scan(&sessionID, &toolName, &output, &createdAt)
					lines = append(lines, fmt.Sprintf("[%s] %s: %s (session=%s)", createdAt, toolName, truncate(output, 120), sessionID))
				}
			}

			return strings.Join(lines, "\n")
		},
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- Auto-cleanup ---

// auditRetentionDays defines how long audit events are kept.
const auditRetentionDays = 30

// auditKey is the context key for the audit logging callback.
type auditKey struct{}

// pruneOldAuditEvents deletes events older than retention period.
func (w *Core) pruneOldAuditEvents() {
	if w.DB == nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -auditRetentionDays).Format("2006-01-02")
	w.DB.Exec("DELETE FROM audit_events WHERE created_at < ?", cutoff)
}
