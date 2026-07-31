package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestManageMemoryUsesGraphAndLeavesSQLiteFactsUntouched(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
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
