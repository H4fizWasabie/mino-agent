package main

import (
	"time"
	"path/filepath"
	"strings"
	"testing"
)

func TestManageMemoryUsesGraphAndLeavesSQLiteFactsUntouched(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	// Simulate a pre-cutover install: legacy table exists with rows to migrate.
	db.Exec(`CREATE TABLE facts (
		id INTEGER PRIMARY KEY,
		subject TEXT NOT NULL,
		content TEXT NOT NULL,
		source TEXT DEFAULT 'user',
		created_at TEXT DEFAULT (datetime('now'))
	)`)
	if _, err := db.Exec("INSERT INTO facts (subject, content) VALUES (?, ?)", "User preference", "legacy body"); err != nil {
		t.Fatal(err)
	}
	memories := filepath.Join(home, "memories")
	mem := &Memory{db: db, cfg: &Settings{Home: home, MemoriesDir: memories}, graph: NewGraphMemory(memories, nil)}
	if err := mem.graph.RecordFact(Fact{ID: "user_preference", Type: "semantic", Subject: "User preference", Body: "graph body"}); err != nil {
		t.Fatal(err)
	}
	tool := makeManageMemoryTool(mem)
	if got := tool.Fn(map[string]any{"action": "correct", "subject": "User preference", "content": "corrected"}); !strings.Contains(got, "Corrected") {
		t.Fatalf("correct = %q", got)
	}
	fact, ok := mem.graph.FindFact("user_preference")
	if !ok || fact.Body != "corrected" {
		t.Fatalf("graph fact = %+v, found=%v", fact, ok)
	}
	var legacy string
	if err := db.QueryRow("SELECT content FROM facts WHERE subject = ?", "User preference").Scan(&legacy); err != nil || legacy != "legacy body" {
		t.Fatalf("sqlite fact = %q, err=%v", legacy, err)
	}
	if got := tool.Fn(map[string]any{"action": "forget", "subject": "User preference"}); !strings.Contains(got, "Forgot") {
		t.Fatalf("forget = %q", got)
	}
	if _, ok := mem.graph.FindFact("user_preference"); ok {
		t.Fatal("graph fact survived forget")
	}
	if err := db.QueryRow("SELECT content FROM facts WHERE subject = ?", "User preference").Scan(&legacy); err != nil || legacy != "legacy body" {
		t.Fatalf("sqlite fact after forget = %q, err=%v", legacy, err)
	}
}

// ISSUE-203: manage_memory can add facts with a stale_after, and the
// staleness sweep honors the declared expiry (DRF-002 volatile facts).
func TestManageMemoryAddWithStaleAfterExpires(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	memories := filepath.Join(home, "memories")
	mem := &Memory{db: db, cfg: &Settings{Home: home, MemoriesDir: memories}, graph: NewGraphMemory(memories, nil)}
	tool := makeManageMemoryTool(mem)

	got := tool.Fn(map[string]any{"action": "add", "subject": "Current test stack", "content": "main is test-model", "stale_after": "1h"})
	if !strings.Contains(got, "expires") {
		t.Fatalf("add with stale_after = %q, want expiry in reply", got)
	}
	fact, ok := mem.graph.FindFact("current_test_stack")
	if !ok {
		t.Fatalf("fact not found after add: %q", got)
	}
	if fact.StaleAfter.IsZero() || !fact.StaleAfter.After(time.Now()) {
		t.Fatalf("stale_after not set: %+v", fact.StaleAfter)
	}
	if fact.Source != "user" {
		t.Fatalf("source = %q, want user (amanuensis)", fact.Source)
	}
}

// ISSUE-203: a declared stale_after wins over the DRF-002 authorship
// exemption — even a user-sourced fact marked as a dated snapshot expires.
func TestDeclaredStaleAfterWinsOverAuthoritativeSource(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	memories := filepath.Join(home, "memories")
	mem := &Memory{db: db, cfg: &Settings{Home: home, MemoriesDir: memories}, graph: NewGraphMemory(memories, nil)}
	expired := time.Now().Add(-time.Hour)
	if err := mem.graph.RecordFact(Fact{ID: "snapshot_fact", Type: "semantic", Subject: "Current stack snapshot", Body: "main: x", Source: "user", StaleAfter: expired}); err != nil {
		t.Fatal(err)
	}
	rejected := mem.graph.ArchiveStaleSemantic(time.Now())
	if rejected != 1 {
		t.Fatalf("ArchiveStaleSemantic rejected %d, want 1 (declared expiry beats authorship)", rejected)
	}
	if _, ok := mem.graph.FindFact("snapshot_fact"); ok {
		t.Fatal("fact still live after declared expiry")
	}
}

func TestManageMemoryAddRejectsBadStaleAfter(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	memories := filepath.Join(home, "memories")
	mem := &Memory{db: db, cfg: &Settings{Home: home, MemoriesDir: memories}, graph: NewGraphMemory(memories, nil)}
	tool := makeManageMemoryTool(mem)
	got := tool.Fn(map[string]any{"action": "add", "subject": "S", "content": "c", "stale_after": "not-a-date"})
	if !strings.Contains(got, "invalid stale_after") {
		t.Fatalf("bad stale_after = %q, want error surfaced", got)
	}
	if _, ok := mem.graph.FindFact("s"); ok {
		t.Fatal("fact was added despite invalid stale_after")
	}
}
