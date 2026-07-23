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
const CurrentSchemaVersion = 1

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
	`CREATE TABLE IF NOT EXISTS facts (
		id INTEGER PRIMARY KEY,
		subject TEXT NOT NULL,
		content TEXT NOT NULL,
		source TEXT DEFAULT 'user',
		created_at TEXT DEFAULT (datetime('now'))
	)`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS facts_fts USING fts5(subject, content, content=facts, content_rowid=id)`,
	`CREATE TRIGGER IF NOT EXISTS facts_ai AFTER INSERT ON facts BEGIN INSERT INTO facts_fts(rowid, subject, content) VALUES (new.id, new.subject, new.content); END`,
	`CREATE TRIGGER IF NOT EXISTS facts_ad AFTER DELETE ON facts BEGIN INSERT INTO facts_fts(facts_fts, rowid, subject, content) VALUES ('delete', old.id, old.subject, old.content); END`,
	`CREATE TRIGGER IF NOT EXISTS facts_au AFTER UPDATE ON facts BEGIN INSERT INTO facts_fts(facts_fts, rowid, subject, content) VALUES ('delete', old.id, old.subject, old.content); INSERT INTO facts_fts(rowid, subject, content) VALUES (new.id, new.subject, new.content); END`,
	`CREATE TABLE IF NOT EXISTS episodes (
		id INTEGER PRIMARY KEY,
		happened_at TEXT NOT NULL,
		summary TEXT NOT NULL,
		session_id TEXT DEFAULT 'default',
		source TEXT DEFAULT 'cli',
		created_at TEXT DEFAULT (datetime('now'))
	)`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS episodes_fts USING fts5(summary, content=episodes, content_rowid=id)`,
	`CREATE TRIGGER IF NOT EXISTS episodes_ai AFTER INSERT ON episodes BEGIN INSERT INTO episodes_fts(rowid, summary) VALUES (new.id, new.summary); END`,
	`CREATE TRIGGER IF NOT EXISTS episodes_ad AFTER DELETE ON episodes BEGIN INSERT INTO episodes_fts(episodes_fts, rowid, summary) VALUES ('delete', old.id, old.summary); END`,
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
	`CREATE TABLE IF NOT EXISTS projects (
		name TEXT PRIMARY KEY,
		objective TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'active',
		blocker TEXT NOT NULL DEFAULT '',
		next_action TEXT NOT NULL DEFAULT '',
		updated_at TEXT DEFAULT (datetime('now'))
	)`,
	// Schema version tracking — _meta stores key/value pairs.
	// Used by runMigrations() to gate versioned migrations.
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
	// facts/episodes may predate their FTS tables. Rebuild keeps the external
	// content indexes complete after upgrades and makes a missing FTS5 build fail
	// at startup instead of silently degrading recall.
	for _, table := range []string{"facts_fts", "episodes_fts"} {
		if _, err := db.Exec(fmt.Sprintf("INSERT INTO %s(%s) VALUES ('rebuild')", table, table)); err != nil {
			panic(fmt.Sprintf("rebuild %s: %v (build with -tags sqlite_fts5)", table, err))
		}
	}
	_ = migrateChatLog(db)
	_ = migrateFacts(db)
	_ = migrateEpisodes(db)
	runMigrations(db)
	return db
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

func migrateFacts(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(facts)")
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
	for name, definition := range map[string]string{
		"importance":    "INTEGER NOT NULL DEFAULT 2",
		"feedback":      "INTEGER NOT NULL DEFAULT 0",
		"last_accessed": "TEXT",
	} {
		if !cols[name] {
			if _, err := db.Exec("ALTER TABLE facts ADD COLUMN " + name + " " + definition); err != nil {
				return err
			}
		}
	}
	return nil
}

func migrateEpisodes(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(episodes)")
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
	for name, definition := range map[string]string{
		"session_id": "TEXT DEFAULT 'default'",
		"source":     "TEXT DEFAULT 'cli'",
	} {
		if !cols[name] {
			if _, err := db.Exec("ALTER TABLE episodes ADD COLUMN " + name + " " + definition); err != nil {
				return err
			}
		}
	}
	return nil
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

	// Example for future migrations:
	// if current < 2 {
	//     db.Exec("ALTER TABLE ... ADD COLUMN ...")
	//     current = 2
	// }

	if current != CurrentSchemaVersion {
		db.Exec("UPDATE _meta SET value = ? WHERE key = 'schema_version'", fmt.Sprint(CurrentSchemaVersion))
		slog.Info("schema migrated", "from", current, "to", CurrentSchemaVersion)
	}
}
