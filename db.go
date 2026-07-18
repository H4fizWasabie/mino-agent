package main

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

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
}

func Connect(home string) *sql.DB {
	path := filepath.Join(home, "state.db")
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on")
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


