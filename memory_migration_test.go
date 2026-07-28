package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateLegacyFactsArchivesRowsAndAvoidsCollisions(t *testing.T) {
	home := t.TempDir()
	memories := filepath.Join(home, "memories")
	db := Connect(home)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO facts (subject, content, source, created_at) VALUES
		('User likes tea', 'Prefers tea in the morning', 'chat', '2026-07-29 10:00:00'),
		('User likes tea', 'Prefers green tea', 'chat', '2026-07-29 10:01:00')`); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(memories, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memories, "user_likes_tea.md"), []byte("---\nid: user_likes_tea\ntype: semantic\nsubject: User likes tea\nat: 2026-07-29T09:00:00Z\n---\nExisting claim\n"), 0644); err != nil {
		t.Fatal(err)
	}

	report, err := MigrateLegacyFacts(db, home, memories)
	if err != nil {
		t.Fatal(err)
	}
	if report.Archived != 2 || report.Canonicalized != 2 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(home, "memory-migration", "legacy", "fact_1.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "memory-migration", "legacy", "fact_2.md")); err != nil {
		t.Fatal(err)
	}
	manifestData, err := os.ReadFile(filepath.Join(home, "memory-migration", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest legacyManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Facts) != 2 || manifest.Facts["1"].CanonicalID == manifest.Facts["2"].CanonicalID {
		t.Fatalf("manifest = %+v", manifest)
	}
	if _, err := os.Stat(filepath.Join(memories, manifest.Facts["1"].CanonicalID+".md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(memories, manifest.Facts["2"].CanonicalID+".md")); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM facts").Scan(&count); err != nil || count != 2 {
		t.Fatalf("sqlite facts count = %d, err=%v", count, err)
	}

	report, err = MigrateLegacyFacts(db, home, memories)
	if err != nil || report.Canonicalized != 0 || report.Archived != 0 {
		t.Fatalf("second migration report = %+v, err=%v", report, err)
	}
}

func TestMigrateLegacyFactsPreservesSourceAndBody(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	if _, err := db.Exec("INSERT INTO facts (subject, content, source) VALUES (?, ?, ?)", "A fact", "The why", "telegram"); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateLegacyFacts(db, home, filepath.Join(home, "memories")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "memory-migration", "legacy", "fact_1.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "source: telegram") || !strings.Contains(text, "sqlite:fact:1") || !strings.Contains(text, "The why") {
		t.Fatalf("archive = %s", text)
	}
}
