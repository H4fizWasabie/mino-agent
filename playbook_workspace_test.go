package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
