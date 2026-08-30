package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditMemoryDirClassifiesFactProblemsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().UTC().Add(-48 * time.Hour)
	write := func(name string, fact Fact) {
		t.Helper()
		if err := writeMarkdownFact(filepath.Join(dir, name), fact); err != nil {
			t.Fatal(err)
		}
	}
	write("missing-source.md", Fact{ID: "missing_source", Type: "semantic", Subject: "Current model", At: old, Body: "Provider model is old"})
	write("copy-a.md", Fact{ID: "copy_a", Type: "semantic", Subject: "Same fact", Body: "same body"})
	write("copy-b.md", Fact{ID: "copy_b", Type: "semantic", Subject: "Same fact", Body: "same body"})
	write("conflict-a.md", Fact{ID: "conflict_a", Type: "semantic", Subject: "Current endpoint", Body: "https://old.example"})
	write("conflict-b.md", Fact{ID: "conflict_b", Type: "semantic", Subject: "Current endpoint", Body: "https://new.example"})
	write("id-a.md", Fact{ID: "same_id", Type: "semantic", Subject: "First"})
	write("id-b.md", Fact{ID: "same_id", Type: "semantic", Subject: "Second"})
	if err := os.WriteFile(filepath.Join(dir, "broken.md"), []byte("not markdown"), 0644); err != nil {
		t.Fatal(err)
	}

	report, err := AuditMemoryDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if report.Files != 8 || report.Parsed != 7 || len(report.ParseFailures) != 1 {
		t.Fatalf("counts = files %d parsed %d failures %d", report.Files, report.Parsed, len(report.ParseFailures))
	}
	if len(report.SourceLess) != 7 {
		t.Fatalf("source-less facts = %d, want 7", len(report.SourceLess))
	}
	if len(report.DuplicateIDs) != 1 || len(report.DuplicateIDs[0]) != 2 {
		t.Fatalf("duplicate IDs = %#v", report.DuplicateIDs)
	}
	if len(report.ExactDuplicates) != 1 || len(report.ExactDuplicates[0]) != 2 {
		t.Fatalf("exact duplicates = %#v", report.ExactDuplicates)
	}
	if len(report.ConflictingSubjects) != 1 || len(report.ConflictingSubjects[0]) != 2 {
		t.Fatalf("conflicting subjects = %#v", report.ConflictingSubjects)
	}
	if len(report.StaleSnapshots) != 1 || report.StaleSnapshots[0].ID != "missing_source" {
		t.Fatalf("stale snapshots = %#v", report.StaleSnapshots)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.json")); !os.IsNotExist(err) {
		t.Fatalf("audit wrote index.json: %v", err)
	}
	if got := report.Format(); !strings.Contains(got, "Repair path:") || !strings.Contains(got, "Same-subject/different-body families: 1") {
		t.Fatalf("report missing required details:\n%s", got)
	}
}

func TestAuditOriginClassifiesKnownSourceLessIDs(t *testing.T) {
	for _, test := range []struct {
		fact Fact
		want string
	}{
		{Fact{ID: "run_123", Tier: "run"}, "likely playbook run"},
		{Fact{ID: "ep_session", Type: "episodic"}, "likely episodic/consolidation"},
		{Fact{ID: "legacy_migration_1"}, "likely legacy migration"},
		{Fact{ID: "unknown"}, "unknown"},
	} {
		if got := auditOrigin(test.fact); got != test.want {
			t.Errorf("auditOrigin(%+v) = %q, want %q", test.fact, got, test.want)
		}
	}
}
