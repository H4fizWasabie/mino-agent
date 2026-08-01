package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// CurrentSchemaVersion is incremented when the schema changes in a way
// that needs explicit migration. Add a migration function in runMigrations().
const CurrentSchemaVersion = 5

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
	`CREATE TABLE IF NOT EXISTS memory_embeddings (
		source TEXT NOT NULL,
		content TEXT NOT NULL,
		embedding TEXT NOT NULL,
		PRIMARY KEY (source, content)
	)`,
	`CREATE TABLE IF NOT EXISTS session_artifacts (
		path TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		label TEXT NOT NULL,
		size INTEGER NOT NULL,
		created_at TEXT DEFAULT (datetime('now'))
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
	`CREATE VIRTUAL TABLE IF NOT EXISTS tool_catalog_fts USING fts5(name UNINDEXED, description, keywords)`,
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
	runMigrations(db)
	if err := initAudit(db); err != nil {
		panic(fmt.Sprintf("initialize audit schema: %v", err))
	}
	_ = migrateChatLog(db)
	return db
}

// runMigrations gates versioned schema migrations by the _meta.schema_version key.
// Legacy databases (no _meta table yet) start at version 0.
// Each migration runs only if current < its target version, then bumps the version.
func runMigrations(db *sql.DB) {
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

