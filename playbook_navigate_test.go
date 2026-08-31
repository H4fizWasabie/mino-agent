package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests for the #450/#451 navigation model: run_playbook advances one
// mechanical step per call instead of driving a dedicated stage loop.

// writeSideEffectingStagePlaybook writes a two-stage playbook whose second
// stage declares a BehaviorMutate tool (send_message) alongside write_file —
// the shape of the live morning-briefing/threads-tribal-battle playbooks
// that hit the #476 incident.
func writeSideEffectingStagePlaybook(t *testing.T, home, name string) {
	t.Helper()
	root := filepath.Join(home, "playbooks", name)
	if err := os.MkdirAll(filepath.Join(root, "stages", "01-gather"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "stages", "02-deliver"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CONTEXT.md"), []byte("# Test playbook\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stage1 := "# Gather\n\n## Process\n\n1. Write the result.\n\n## Tools\n\n- write_file\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Result | `output/result.md` | Markdown |\n"
	if err := os.WriteFile(filepath.Join(root, "stages", "01-gather", "CONTEXT.md"), []byte(stage1), 0600); err != nil {
		t.Fatal(err)
	}
	stage2 := "# Deliver\n\n## Process\n\n1. Send the message, then write the marker.\n\n## Tools\n\n- send_message\n- write_file\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Marker | `output/delivered.md` | Markdown |\n"
	if err := os.WriteFile(filepath.Join(root, "stages", "02-deliver", "CONTEXT.md"), []byte(stage2), 0600); err != nil {
		t.Fatal(err)
	}
}

// TestNavigatePlaybookRunContinuesSameRunAcrossSideEffectingStage is the
// regression test for the #476 live incident (2026-08-31): a session
// advancing its own in-progress run past a side-effecting (non-retry-safe)
// stage must keep advancing that exact run, never spawn a fresh one. Before
// the fix, every advance call re-derived resumability from scratch via
// loadOrCreatePlaybookRun, which cannot tell "the same session verifying its
// own just-finished side-effecting stage" apart from "resuming a different
// process's run after a crash" — it always chose the second interpretation,
// abandoning the run and starting a new one that redid the side effect from
// stage 1 (the owner received the same Telegram brief five times).
func TestNavigatePlaybookRunContinuesSameRunAcrossSideEffectingStage(t *testing.T) {
	home := t.TempDir()
	writeSideEffectingStagePlaybook(t, home, "brief")
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	registry.Register(&Tool{Name: "send_message", Behavior: BehaviorMutate})
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
	sessionID := "scheduled-brief"

	first, err := navigatePlaybookRun(context.Background(), core, "brief", "go", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "running" || !strings.Contains(first.Reply, "01-gather") {
		t.Fatalf("expected stage 1 navigation, got %+v", first)
	}

	pb, err := loadPlaybookWorkspace(home, "brief")
	if err != nil {
		t.Fatal(err)
	}
	run, err := latestPlaybookRun(pb)
	if err != nil || run == nil {
		t.Fatalf("expected a run on disk: %v", err)
	}
	firstRunID := run.ID
	stage1, _ := workspaceStage(pb, 1)
	path1 := playbookRunOutputPath(pb, run, stage1, stage1.Outputs[0])
	if err := os.MkdirAll(filepath.Dir(path1), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path1, []byte("gathered"), 0600); err != nil {
		t.Fatal(err)
	}

	// Advance past stage 1 (retry-safe, write_file only) into stage 2, the
	// side-effecting one. This call must still be operating on firstRunID.
	second, err := navigatePlaybookRun(context.Background(), core, "brief", "go", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "running" || !strings.Contains(second.Reply, "02-deliver") {
		t.Fatalf("expected stage 2 navigation, got %+v", second)
	}
	run, err = latestPlaybookRun(pb)
	if err != nil || run.ID != firstRunID {
		t.Fatalf("expected the same run %s to still be the latest after advancing to stage 2, got %+v (err=%v)", firstRunID, run, err)
	}

	// Simulate stage 2's side effect having happened (send_message already
	// executed for real) and its declared marker written. A small sleep
	// keeps the marker's mtime unambiguously after state.StartedAt — the
	// same margin real tool-call latency gives in production.
	time.Sleep(5 * time.Millisecond)
	stage2, _ := workspaceStage(pb, 2)
	path2 := playbookRunOutputPath(pb, run, stage2, stage2.Outputs[0])
	if err := os.MkdirAll(filepath.Dir(path2), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path2, []byte("delivered"), 0600); err != nil {
		t.Fatal(err)
	}

	// The critical assertion: advancing past the side-effecting stage must
	// complete firstRunID, never abandon it for a fresh run that would redo
	// the send_message call.
	third, err := navigatePlaybookRun(context.Background(), core, "brief", "go", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if third.Status != "complete" {
		t.Fatalf("expected the run to complete, got %+v", third)
	}
	if !strings.Contains(third.Reply, firstRunID) {
		t.Fatalf("expected completion to reference the original run %s, got %+v", firstRunID, third)
	}
	pbRunsDir := playbookRunsDir(pb)
	entries, err := os.ReadDir(pbRunsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("expected exactly one run directory (no fresh run spawned), got %d: %v", len(entries), names)
	}
}

func TestNavigatePlaybookRunScriptStageAutoDrives(t *testing.T) {
	// A script-backed stage must stay zero-inference (#304): one navigate
	// call should run it straight through with no model turn, landing
	// directly on the run's completion since it is the only stage.
	home := t.TempDir()
	writeScriptStage(t, home, "auto-script", "01-collect")
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}

	result, err := navigatePlaybookRun(context.Background(), core, "auto-script", "go", "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "complete" {
		t.Fatalf("expected complete, got %+v", result)
	}
}

func TestNavigatePlaybookRunRefusesUnsafeResume(t *testing.T) {
	// A crash mid-stage with a destructive (non-retry-safe) tool must never
	// be auto-resumed (the VPS duplicate-Threads-post incident) — navigation
	// must start a fresh run and say so, leaving the unsafe run untouched.
	home := t.TempDir()
	writeWorkspaceStageTool(t, home, "destructive", "threads_post")
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	registry.Register(&Tool{Name: "threads_post", Behavior: BehaviorMutate})
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}

	pb, err := loadPlaybookWorkspace(home, "destructive")
	if err != nil {
		t.Fatal(err)
	}
	run, err := loadOrCreatePlaybookRun(pb, registry, "go", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: the stage was handed to Mino (StartedAt set) and never
	// verified before the process died, leaving it "running" on disk.
	run.Stages[0].Status = "running"
	run.Stages[0].Attempts = 1
	run.Stages[0].StartedAt = time.Now().UTC()
	if err := savePlaybookRun(pb, run); err != nil {
		t.Fatal(err)
	}
	abandonedID := run.ID

	result, err := navigatePlaybookRun(context.Background(), core, "destructive", "go again", "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "running" || !strings.Contains(result.Reply, abandonedID) {
		t.Fatalf("expected a fresh run mentioning the abandoned run %s, got %+v", abandonedID, result)
	}
	// The abandoned run must be left exactly as it was — never touched.
	stale, err := latestResumablePlaybookRun(pb, registry)
	if err != nil {
		t.Fatal(err)
	}
	if stale != nil {
		t.Fatalf("the unsafe run must never be reported resumable, got %+v", stale)
	}
	data, err := os.ReadFile(filepath.Join(playbookRunsDir(pb), abandonedID, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"status": "running"`) {
		t.Fatalf("abandoned run must stay untouched on disk: %s", data)
	}
}

func TestNavigatePlaybookRunRetrySafeResumeVerifiesAndAdvances(t *testing.T) {
	// A retry-safe stage (write_file only) left "running" across a crash is
	// safe to pick back up: navigation re-verifies it, retries in place while
	// attempts remain, and completes once the declared output lands.
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}

	first, err := navigatePlaybookRun(context.Background(), core, "brief", "go", "test")
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "running" || !strings.Contains(first.Reply, "01-collect") {
		t.Fatalf("first call should hand back stage 1, got %+v", first)
	}

	// Verification fails: nothing written yet. Retry-safe, so the same stage
	// comes back with a failure note instead of failing the run.
	second, err := navigatePlaybookRun(context.Background(), core, "brief", "go", "test")
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "running" || !strings.Contains(second.Reply, "attempt") {
		t.Fatalf("second call should retry stage 1 with a failure note, got %+v", second)
	}

	pb, err := loadPlaybookWorkspace(home, "brief")
	if err != nil {
		t.Fatal(err)
	}
	run, err := latestPlaybookRun(pb)
	if err != nil || run == nil {
		t.Fatalf("expected a run on disk: %v", err)
	}
	stage1, _ := workspaceStage(pb, 1)
	path1 := playbookRunOutputPath(pb, run, stage1, stage1.Outputs[0])
	if err := os.MkdirAll(filepath.Dir(path1), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path1, []byte("collected"), 0600); err != nil {
		t.Fatal(err)
	}

	third, err := navigatePlaybookRun(context.Background(), core, "brief", "go", "test")
	if err != nil {
		t.Fatal(err)
	}
	if third.Status != "complete" {
		t.Fatalf("third call should verify the written output and complete, got %+v", third)
	}
}

func TestNavigatePlaybookRunReportsDeviationOnFailedVerification(t *testing.T) {
	// #447's deviation reporting must keep working when triggered by the
	// navigation advance signal instead of a dedicated loop-attempt exit.
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}

	if _, err := navigatePlaybookRun(context.Background(), core, "brief", "go", "test"); err != nil {
		t.Fatal(err)
	}
	// Exhaust attempts without ever writing the declared output.
	var last *PlaybookResult
	for i := 0; i < maxStageAttempts; i++ {
		r, err := navigatePlaybookRun(context.Background(), core, "brief", "go", "test")
		if err != nil {
			t.Fatal(err)
		}
		last = r
	}
	if last.Status != "failed" {
		t.Fatalf("expected the run to fail after exhausting attempts, got %+v", last)
	}
	entries, err := os.ReadDir(filepath.Join(home, "outbox"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected a deviation notice queued to the owner outbox: %v", err)
	}
}

func TestInterruptRunOnDiskStopsTheCancelledRunPermanently(t *testing.T) {
	// cancel_run on a navigation-mode run has no live call to cancel between
	// run_playbook calls (#310's registry only tracks a call in flight) —
	// interruptRunOnDisk is the fallback. Same as today's live cancellation
	// on the dedicated loop, an interrupted run is terminal: it is never
	// picked back up, and the next navigate call starts a fresh run.
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}

	if _, err := navigatePlaybookRun(context.Background(), core, "brief", "go", "test"); err != nil {
		t.Fatal(err)
	}
	pb, err := loadPlaybookWorkspace(home, "brief")
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := latestPlaybookRun(pb)
	if err != nil || cancelled == nil {
		t.Fatalf("expected a run on disk: %v", err)
	}

	if !interruptRunOnDisk(home, cancelled.ID, "owner said stop") {
		t.Fatal("expected interruptRunOnDisk to find and mark the running run")
	}
	data, err := os.ReadFile(filepath.Join(playbookRunsDir(pb), cancelled.ID, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"status": "interrupted"`) || !strings.Contains(string(data), "owner said stop") {
		t.Fatalf("expected the run to be recorded interrupted with its reason: %s", data)
	}

	result, err := navigatePlaybookRun(context.Background(), core, "brief", "go", "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "running" {
		t.Fatalf("expected a fresh run to start, got %+v", result)
	}
	fresh, err := latestPlaybookRun(pb)
	if err != nil || fresh == nil || fresh.ID == cancelled.ID {
		t.Fatalf("expected a new run distinct from the cancelled one, got %+v (cancelled was %s)", fresh, cancelled.ID)
	}
}

func TestPlaybookWriteGuardSessionNavFallback(t *testing.T) {
	// Chat-navigated writes have no per-call stageCtx to set traceTagKey; the
	// session nav pointer (#450/#451) is the fallback authorization source.
	home := t.TempDir()
	runDir := filepath.Join(home, "playbooks", "brief", "runs", "run-1", "stages", "01-collect", "output")
	path := filepath.Join(runDir, "result.md")

	ctx := context.WithValue(context.Background(), sessionIDKey{}, "sess-1")
	if msg := playbookWriteGuard(home, path, ctx); msg == "" {
		t.Fatal("expected a denial with no navigation pointer set")
	}

	setSessionNav("sess-1", "brief", "run-1")
	defer clearSessionNav("sess-1")
	if msg := playbookWriteGuard(home, path, ctx); msg != "" {
		t.Fatalf("expected the write to be allowed once the session nav pointer matches, got %q", msg)
	}

	otherPath := filepath.Join(home, "playbooks", "brief", "runs", "run-2", "stages", "01-collect", "output", "result.md")
	if msg := playbookWriteGuard(home, otherPath, ctx); msg == "" {
		t.Fatal("expected a denial for a different run than the session's nav pointer")
	}
}

// writeScriptStage writes a single-stage, script-backed playbook (#304):
// zero-inference, verified by exit code and declared output existence.
func writeScriptStage(t *testing.T, home, name, stage string) {
	t.Helper()
	root := filepath.Join(home, "playbooks", name)
	dir := filepath.Join(root, "stages", stage)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CONTEXT.md"), []byte("# Test playbook\n"), 0600); err != nil {
		t.Fatal(err)
	}
	content := "# " + stage + "\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Result | `output/result.md` | Markdown |\n"
	if err := os.WriteFile(filepath.Join(dir, "CONTEXT.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/bash\nmkdir -p output\necho done > output/result.md\n"
	scriptPath := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
}
