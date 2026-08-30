package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #407: schema v9 retires the legacy SQLite semantic-memory tables. Any
// unmigrated row is backed up into the graph first (MigrateLegacyFacts is
// idempotent and already covered by memory_migration_test.go); this test
// locks the wiring — the migration runs on the next boot after an upgrade,
// drops the table only once the backup succeeds, and lands schema_version
// at 9.
func TestSchemaV9RetiresLegacyFactsTable(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	// Simulate a pre-v9 install: a legacy fact never migrated, and the
	// schema pinned at v8 (the version before retirement).
	if _, err := db.Exec(`CREATE TABLE facts (
		id INTEGER PRIMARY KEY,
		subject TEXT NOT NULL,
		content TEXT NOT NULL,
		source TEXT DEFAULT 'user',
		created_at TEXT DEFAULT (datetime('now'))
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO facts (subject, content, source, created_at) VALUES
		('User likes tea', 'Prefers tea in the morning', 'chat', '2026-07-29 10:00:00')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE _meta SET value = '8' WHERE key = 'schema_version'`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Re-open: the next boot after upgrading past v9 runs the migration.
	db2 := Connect(home)
	defer db2.Close()

	var version string
	if err := db2.QueryRow("SELECT value FROM _meta WHERE key = 'schema_version'").Scan(&version); err != nil || version != "9" {
		t.Fatalf("schema version = %q, err=%v, want 9", version, err)
	}

	var exists int
	db2.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='facts'").Scan(&exists)
	if exists != 0 {
		t.Fatal("legacy facts table should have been dropped")
	}

	entries, err := os.ReadDir(filepath.Join(home, "memories"))
	if err != nil {
		t.Fatalf("expected memories dir with migrated fact: %v", err)
	}
	found := false
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(home, "memories", e.Name()))
		if strings.Contains(string(data), "Prefers tea in the morning") {
			found = true
		}
	}
	if !found {
		t.Fatalf("legacy fact was not backed up into the graph before the table was dropped; entries: %v", entries)
	}
}

// A fresh install (no legacy facts table ever existed) reaches v9 cleanly —
// MigrateLegacyFacts' own table-existence guard makes this a no-op.
func TestSchemaV9NoOpOnFreshInstall(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	var version string
	if err := db.QueryRow("SELECT value FROM _meta WHERE key = 'schema_version'").Scan(&version); err != nil || version != "9" {
		t.Fatalf("schema version = %q, err=%v, want 9", version, err)
	}
}
