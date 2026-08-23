package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// CurrentSchemaVersion is incremented when the schema changes in a way
// that needs explicit migration. Add a migration function in runMigrations().
const CurrentSchemaVersion = 8

// Simplified schema — single statements, no triggers with embedded semicolons.
var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS calendar_events (
		id INTEGER PRIMARY KEY,
		title TEXT NOT NULL,
		start TEXT NOT NULL,
		"end" TEXT,
		attendees TEXT DEFAULT '',
		notes TEXT DEFAULT '',
		created_at TEXT DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS reminders (
		id INTEGER PRIMARY KEY,
		message TEXT NOT NULL,
		remind_at TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		created_at TEXT DEFAULT (datetime('now')),
		delivered_at TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_reminders_due ON reminders(status, remind_at)`,
	`CREATE TABLE IF NOT EXISTS chat_log (
		id INTEGER PRIMARY KEY,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		consolidated INTEGER DEFAULT 0,
		session_id TEXT DEFAULT 'default',
		source TEXT DEFAULT 'cli',
		created_at TEXT DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS session_artifacts (
		path TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		label TEXT NOT NULL,
		size INTEGER NOT NULL,
		distilled INTEGER NOT NULL DEFAULT 0,
		created_at TEXT DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS session_notes (
		session_id TEXT PRIMARY KEY,
		note TEXT NOT NULL,
		updated_at TEXT DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS tool_calls (
		id INTEGER PRIMARY KEY,
		session_id TEXT NOT NULL DEFAULT '',
		tool_name TEXT NOT NULL,
		args TEXT NOT NULL DEFAULT '',
		output_summary TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'ok',
		iteration INTEGER NOT NULL DEFAULT 0,
		created_at TEXT DEFAULT (datetime('now'))
	)`,
	`CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id)`,
	`CREATE INDEX IF NOT EXISTS idx_tool_calls_name ON tool_calls(tool_name)`,
	`CREATE TABLE IF NOT EXISTS responsibilities (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		title TEXT NOT NULL,
		outcome TEXT NOT NULL DEFAULT '',
		owner TEXT NOT NULL,
		status TEXT NOT NULL CHECK (status IN ('needs_you','working','waiting','blocked','verified','stopped')),
		next_action TEXT NOT NULL DEFAULT '',
		next_owner TEXT NOT NULL DEFAULT '',
		due_at TEXT,
		last_run_at TEXT,
		schedule TEXT NOT NULL DEFAULT '',
		source_kind TEXT NOT NULL DEFAULT '',
		source_ref TEXT NOT NULL DEFAULT '',
		verification TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS responsibility_events (
		id INTEGER PRIMARY KEY,
		responsibility_id TEXT NOT NULL REFERENCES responsibilities(id),
		event_type TEXT NOT NULL,
		outcome TEXT NOT NULL DEFAULT '',
		owner TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL CHECK (status IN ('needs_you','working','waiting','blocked','verified','stopped')),
		next_action TEXT NOT NULL DEFAULT '',
		next_owner TEXT NOT NULL DEFAULT '',
		due_at TEXT,
		last_run_at TEXT,
		schedule TEXT NOT NULL DEFAULT '',
		verification TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL,
		evidence TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_responsibilities_status ON responsibilities(status)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_responsibilities_source ON responsibilities(source_kind, source_ref)
		WHERE source_kind <> '' AND source_ref <> ''`,
	`CREATE INDEX IF NOT EXISTS idx_responsibility_events_responsibility ON responsibility_events(responsibility_id, id)`,
	`CREATE TABLE IF NOT EXISTS ops_journal (
		id INTEGER PRIMARY KEY,
		ts TEXT NOT NULL DEFAULT (datetime('now')),
		op_type TEXT NOT NULL,
		entity TEXT NOT NULL,
		before_state TEXT NOT NULL DEFAULT '',
		after_state TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'ok' CHECK (status IN ('ok','failed','rolled_back')),
		session_id TEXT NOT NULL DEFAULT '',
		undo_of INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS idx_ops_journal_entity ON ops_journal(entity, id)`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS tool_catalog_fts USING fts5(name UNINDEXED, description, keywords)`,
	// DATA-001 (#344): usage records move from the unbounded usage.jsonl into
	// SQLite; columns mirror the legacy JSONL record fields.
	`CREATE TABLE IF NOT EXISTS usage_log (
		id INTEGER PRIMARY KEY,
		ts TEXT NOT NULL,
		provider TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		session_id TEXT NOT NULL DEFAULT '',
		in_tokens INTEGER NOT NULL DEFAULT 0,
		out_tokens INTEGER NOT NULL DEFAULT 0,
		cache_read INTEGER NOT NULL DEFAULT 0,
		cache_write INTEGER NOT NULL DEFAULT 0,
		latency_ms INTEGER NOT NULL DEFAULT 0,
		cost_usd REAL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_usage_log_ts ON usage_log(ts)`,
	`CREATE INDEX IF NOT EXISTS idx_usage_log_session ON usage_log(session_id)`,
	`CREATE TABLE IF NOT EXISTS _meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
}

func Connect(home string) *sql.DB {
	path := filepath.Join(home, "state.db")
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(1)
	db.Exec("PRAGMA busy_timeout=3000")
	var integrity string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		panic(fmt.Sprintf("SQLite integrity check: %v %s", err, integrity))
	}
	if err := db.Ping(); err != nil {
		panic(err)
	}
	for _, stmt := range schemaStatements {
		if _, err := db.Exec(stmt); err != nil {
			panic(fmt.Sprintf("initialize SQLite schema: %v", err))
		}
	}
	runMigrations(db, home)
	if err := initAudit(db); err != nil {
		panic(fmt.Sprintf("initialize audit schema: %v", err))
	}
	_ = migrateChatLog(db)
	return db
}

// runMigrations gates versioned schema migrations by the _meta.schema_version key.
// Legacy databases (no _meta table yet) start at version 0.
// Each migration runs only if current < its target version, then bumps the version.
func runMigrations(db *sql.DB, home string) {
	var current int
	err := db.QueryRow("SELECT value FROM _meta WHERE key = 'schema_version'").Scan(&current)
	if err != nil {
		current = 0 // fresh DB or pre-versioning DB
		db.Exec("INSERT OR IGNORE INTO _meta (key, value) VALUES ('schema_version', '0')")
	}
	from := current

	// v3: projects table removed (replaced by playbook folders)
	if current < 3 {
		current = 3
	}
	// v4: persistent one-shot reminders
	if current < 4 {
		current = 4
	}
	// v5: authoritative Responsibility projection and append-only history
	if current < 5 {
		current = 5
	}
	// v6: playbook output distillation queue
	if current < 6 {
		db.Exec("ALTER TABLE session_artifacts ADD COLUMN distilled INTEGER NOT NULL DEFAULT 0")
		current = 6
	}
	// v7: embeddings removed (issue #179) — the store and all its consumers
	// are dead; FTS5 + essentials + triggers are the retrieval floor.
	if current < 7 {
		db.Exec("DROP TABLE IF EXISTS memory_embeddings")
		current = 7
	}
	// v8: usage.jsonl → usage_log (DATA-001, #344). One-time backfill inside
	// the migration: every install booting this version imports its existing
	// JSONL history, then the file is renamed aside.
	if current < 8 {
		migrateUsageJSONL(db, home)
		current = 8
	}

	if current != from {
		db.Exec("UPDATE _meta SET value = ? WHERE key = 'schema_version'", fmt.Sprint(CurrentSchemaVersion))
		slog.Info("schema migrated", "from", from, "to", CurrentSchemaVersion)
	}
}

func migrateChatLog(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(chat_log)")
	if err != nil {
		return err
	}
	defer rows.Close()
	cols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk)
		cols[name] = true
	}
	if !cols["session_id"] {
		db.Exec("ALTER TABLE chat_log ADD COLUMN session_id TEXT DEFAULT 'default'")
	}
	if !cols["source"] {
		db.Exec("ALTER TABLE chat_log ADD COLUMN source TEXT DEFAULT 'cli'")
	}
	return nil
}

// migrateUsageJSONL backfills the legacy usage.jsonl into usage_log (v8).
// Crash-safe: all inserts plus the _meta marker commit in one transaction, so
// a boot that dies mid-import never duplicates rows on the next one. The file
// is renamed to usage.jsonl.imported only after a successful commit; a failed
// rename is harmless (the marker prevents re-import).
func migrateUsageJSONL(db *sql.DB, home string) {
	var done string
	if err := db.QueryRow(`SELECT value FROM _meta WHERE key = 'usage_backfilled'`).Scan(&done); err == nil && done == "1" {
		return
	}
	path := filepath.Join(home, "usage.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		// No legacy file (fresh install or already migrated): mark done.
		db.Exec(`INSERT OR REPLACE INTO _meta (key, value) VALUES ('usage_backfilled', '1')`)
		return
	}
	tx, err := db.Begin()
	if err != nil {
		slog.Warn("usage backfill: begin failed", "error", err)
		return
	}
	stmt, err := tx.Prepare(`INSERT INTO usage_log
		(ts, provider, model, session_id, in_tokens, out_tokens, cache_read, cache_write, latency_ms, cost_usd)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		slog.Warn("usage backfill: prepare failed", "error", err)
		return
	}
	defer stmt.Close()
	imported := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r struct {
			TS         string  `json:"ts"`
			Provider   string  `json:"provider"`
			Model      string  `json:"model"`
			SessionID  string  `json:"session_id"`
			In         float64 `json:"in"`
			Out        float64 `json:"out"`
			CacheRead  float64 `json:"cache_read"`
			CacheWrite float64 `json:"cache_write"`
			LatencyMS  float64 `json:"latency_ms"`
			CostUSD    float64 `json:"cost_usd"`
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue // malformed line: same skip rule the old reader used
		}
		var cost any
		if r.CostUSD > 0 {
			cost = r.CostUSD
		}
		if _, err := stmt.Exec(r.TS, r.Provider, r.Model, r.SessionID,
			int64(r.In), int64(r.Out), int64(r.CacheRead), int64(r.CacheWrite),
			int64(r.LatencyMS), cost); err != nil {
			tx.Rollback()
			slog.Warn("usage backfill: insert failed", "error", err)
			return
		}
		imported++
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO _meta (key, value) VALUES ('usage_backfilled', '1')`); err != nil {
		tx.Rollback()
		slog.Warn("usage backfill: marker failed", "error", err)
		return
	}
	if err := tx.Commit(); err != nil {
		slog.Warn("usage backfill: commit failed", "error", err)
		return
	}
	if err := os.Rename(path, path+".imported"); err != nil {
		slog.Warn("usage backfill: rename failed (marker prevents re-import)", "path", path, "error", err)
	}
	slog.Info("usage history backfilled into state.db", "records", imported)
}
