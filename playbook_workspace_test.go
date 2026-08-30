package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// OBS-001: boot reconciliation marks runs stuck in "running" across a crash
// as "interrupted" — the 2026-08-14 orphan class dies without manual
// quarantine.
func TestReconcileInterruptedRuns(t *testing.T) {
	home := t.TempDir()
	writeRun := func(pb, id, status string) {
		dir := filepath.Join(home, "playbooks", pb, "runs", id)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		run := PlaybookRun{ID: id, Playbook: pb, Status: status}
		data, _ := json.Marshal(run)
		if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeRun("daily-ai-concept", "20260814T034431", "running") // the wedge orphan
	writeRun("daily-ai-concept", "20260814T024645", "complete")
	writeRun("morning-briefing", "20260813T233014", "running")

	n := ReconcileInterruptedRuns(home)
	if n != 2 {
		t.Fatalf("reconciled %d, want 2", n)
	}

	check := func(pb, id, want string) {
		data, err := os.ReadFile(filepath.Join(home, "playbooks", pb, "runs", id, "state.json"))
		if err != nil {
			t.Fatal(err)
		}
		var run PlaybookRun
		if err := json.Unmarshal(data, &run); err != nil {
			t.Fatal(err)
		}
		if run.Status != want {
			t.Fatalf("%s/%s status = %q, want %q", pb, id, run.Status, want)
		}
	}
	check("daily-ai-concept", "20260814T034431", "interrupted")
	check("morning-briefing", "20260813T233014", "interrupted")
	check("daily-ai-concept", "20260814T024645", "complete")
}

// DATA-006 (#404): completed/failed/interrupted run directories are pruned
// once past retention, but the newest run and any "running" run are always
// kept, and a durable summary survives pruning in runs-archive.jsonl.
func TestPrunePlaybookRuns(t *testing.T) {
	home := t.TempDir()
	writeRun := func(pb, id, status string, createdAt time.Time) {
		dir := filepath.Join(home, "playbooks", pb, "runs", id)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		run := PlaybookRun{ID: id, Playbook: pb, Status: status, CreatedAt: createdAt}
		data, _ := json.Marshal(run)
		if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -40) // past playbookRunRetention (30d)

	// Old, complete, not-newest: eligible for pruning.
	writeRun("ai-news-daily", "20260101T000000", "complete", old)
	// Old, but still running: protected regardless of age.
	writeRun("ai-news-daily", "20260102T000000", "running", old)
	// Newest run for the playbook: protected regardless of status or age
	// (it's always the resume target).
	writeRun("ai-news-daily", "20260103T000000", "failed", old)
	// Recent, complete: not old enough to prune.
	writeRun("morning-briefing", "20260201T000000", "complete", now)

	prunePlaybookRuns(home)

	exists := func(pb, id string) bool {
		_, err := os.Stat(filepath.Join(home, "playbooks", pb, "runs", id, "state.json"))
		return err == nil
	}
	if exists("ai-news-daily", "20260101T000000") {
		t.Error("old complete non-newest run should have been pruned")
	}
	if !exists("ai-news-daily", "20260102T000000") {
		t.Error("running run should never be pruned")
	}
	if !exists("ai-news-daily", "20260103T000000") {
		t.Error("newest run should never be pruned regardless of status")
	}
	if !exists("morning-briefing", "20260201T000000") {
		t.Error("recent run should not be pruned")
	}

	archive, err := os.ReadFile(filepath.Join(home, "playbooks", "ai-news-daily", "runs-archive.jsonl"))
	if err != nil {
		t.Fatalf("expected runs-archive.jsonl to exist: %v", err)
	}
	if !strings.Contains(string(archive), "20260101T000000") {
		t.Errorf("archive missing pruned run summary: %s", archive)
	}
}
