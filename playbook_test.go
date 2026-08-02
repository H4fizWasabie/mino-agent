package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceRunResumesFirstIncompleteStage(t *testing.T) {
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect", "02-report"})
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
	pb, err := loadPlaybookWorkspace(home, "brief")
	if err != nil {
		t.Fatal(err)
	}
	run, err := loadOrCreatePlaybookRun(pb, registry, "make the briefing", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	stage1, _ := workspaceStage(pb, 1)
	stage2, _ := workspaceStage(pb, 2)
	path1 := playbookRunOutputPath(pb, run, stage1, stage1.Outputs[0])
	path2 := playbookRunOutputPath(pb, run, stage2, stage2.Outputs[0])
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	calls := 0
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		calls++
		if calls == 1 {
			if err := os.MkdirAll(filepath.Dir(path1), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path1, []byte("collected"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return &LoopResult{Status: "complete", Reply: "done", ToolCalls: []ToolCall{{Name: "write_file", Args: map[string]any{"path": path1}}}}
	}
	result, err := RunPlaybook(context.Background(), core, "brief", "make the briefing", "test", nil)
	if err != nil || result.Status != "failed" || result.StagesRun != 3 {
		t.Fatalf("first run = %+v, err=%v", result, err)
	}
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		if err := os.MkdirAll(filepath.Dir(path2), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path2, []byte("reported"), 0600); err != nil {
			t.Fatal(err)
		}
		return &LoopResult{Status: "complete", Reply: "done", ToolCalls: []ToolCall{{Name: "write_file", Args: map[string]any{"path": path2}}}}
	}
	result, err = RunPlaybook(context.Background(), core, "brief", "ignored on resume", "test", nil)
	if err != nil || result.Status != "complete" || result.StagesRun != 1 {
		t.Fatalf("resume = %+v, err=%v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(playbookRunsDir(pb), run.ID, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved PlaybookRun
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Stages[0].Attempts != 1 || saved.Stages[1].Attempts != 3 || saved.Status != "complete" {
		t.Fatalf("state = %+v", saved)
	}
}

func TestWorkspaceRejectsPreSeededOutput(t *testing.T) {
	// VPS incident reproduction: the main loop did the real work, hand-wrote the
	// playbook output files, then run_playbook rubber-stamped them. write-attributed
	// verification must fail the stage when the output was not written by the
	// stage's own tool calls.
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
	pb, err := loadPlaybookWorkspace(home, "brief")
	if err != nil {
		t.Fatal(err)
	}
	run, err := loadOrCreatePlaybookRun(pb, registry, "cheat attempt", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	stage1, _ := workspaceStage(pb, 1)
	path1 := playbookRunOutputPath(pb, run, stage1, stage1.Outputs[0])
	// Main loop "cheats": writes the output file before the playbook runs.
	if err := os.MkdirAll(filepath.Dir(path1), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path1, []byte("preseeded result"), 0600); err != nil {
		t.Fatal(err)
	}
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		// Stage does nothing — no write_file call. The pre-seeded file exists.
		return &LoopResult{Status: "complete", Reply: "done"}
	}
	result, err := RunPlaybook(context.Background(), core, "brief", "cheat attempt", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" {
		t.Fatalf("pre-seeded output passed verification: %+v", result)
	}
	if !strings.Contains(result.Reply, "not written by this stage") {
		t.Fatalf("reply = %q, want attribution failure", result.Reply)
	}
	if result.StagesRun != 2 {
		t.Fatalf("StagesRun = %d, want 2 (retry-safe stage attempted twice, both failed)", result.StagesRun)
	}
}

func TestWorkspaceStageEventsCarryTraceTag(t *testing.T) {
	// Trace events emitted inside a playbook stage must carry playbook/stage tags
	// so the dashboard can group stage work instead of showing a flat stream.
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	pb, err := loadPlaybookWorkspace(home, "brief")
	if err != nil {
		t.Fatal(err)
	}
	stage1, _ := workspaceStage(pb, 1)
	path := playbookRunOutputPath(pb, &PlaybookRun{ID: "tagtest", Playbook: "brief"}, stage1, stage1.Outputs[0])
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		// The seam does not carry ctx; emulate the tagged loop by writing a
		// tagged tool event directly (RunLoopContext does this via trace()).
		logTrace(home, "tool", map[string]any{"tool": "search_web", "status": "ok", "playbook": "brief", "stage": "01-collect"})
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("collected"), 0600); err != nil {
			t.Fatal(err)
		}
		return &LoopResult{Status: "complete", Reply: "done", ToolCalls: []ToolCall{{Name: "write_file", Args: map[string]any{"path": path}}}}
	}
	result, err := RunPlaybook(context.Background(), core, "brief", "tag test", "test", nil)
	if err != nil || result.Status != "failed" {
		t.Fatalf("run = %+v, err=%v", result, err)
	}
	tail := traceTail(home)
	found := false
	for _, ev := range tail {
		if ev["type"] == "tool" && ev["playbook"] == "brief" && ev["stage"] == "01-collect" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no stage-tagged tool event in trace tail: %v", tail)
	}
}

func TestPlaybookWriteGuard(t *testing.T) {
	// The playbook tree is read-only to the main loop and to stages outside
	// their own run directory: pre-seeding outputs before run_playbook (the VPS
	// cheat) must be refused, while a stage may write its own run outputs.
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	ctx := context.Background()

	// Main loop: any write into the playbook tree is refused.
	if guard := playbookWriteGuard(home, filepath.Join(home, "playbooks", "brief", "output", "pre-seed.md"), ctx); guard == "" {
		t.Fatal("main-loop write into playbook tree was allowed")
	}
	if guard := playbookWriteGuard(home, filepath.Join(home, "playbooks", "brief", "stages", "01-collect", "CONTEXT.md"), ctx); guard == "" {
		t.Fatal("main-loop write into stage contract was allowed")
	}
	// Outside the tree: always allowed.
	if guard := playbookWriteGuard(home, filepath.Join(home, "notes.md"), ctx); guard != "" {
		t.Fatalf("outside-tree write refused: %s", guard)
	}
	// Doubled-home hallucination (the VPS .mino/.mino/ class): rejected even
	// though the path is outside the real playbook tree.
	doubled := filepath.Join(home, filepath.Base(home), "playbooks", "brief", "output", "x.md")
	if guard := playbookWriteGuard(home, doubled, ctx); guard == "" || !strings.Contains(guard, "doubled-path") {
		t.Fatalf("doubled-home path not rejected: %s (guard=%q)", doubled, guard)
	}

	// Inside a stage: only its own run directory is writable.
	stageCtx := context.WithValue(ctx, traceTagKey{}, map[string]string{
		"playbook": "brief",
		"stage":    "01-collect",
		"run":      "run-123",
	})
	if guard := playbookWriteGuard(home, filepath.Join(home, "playbooks", "brief", "runs", "run-123", "stages", "01-collect", "output", "result.md"), stageCtx); guard != "" {
		t.Fatalf("stage write to own run output refused: %s", guard)
	}
	if guard := playbookWriteGuard(home, filepath.Join(home, "playbooks", "brief", "stages", "01-collect", "CONTEXT.md"), stageCtx); guard == "" {
		t.Fatal("stage write to contract was allowed")
	}
	if guard := playbookWriteGuard(home, filepath.Join(home, "playbooks", "brief", "runs", "other-run", "output", "result.md"), stageCtx); guard == "" {
		t.Fatal("stage write to another run was allowed")
	}
}

func TestWorkspaceRejectsHumanCheckpointStage(t *testing.T) {
	// Autonomy rule: a stage that defers to a human is a conversation, not a
	// playbook — validation must reject it at creation time.
	home := t.TempDir()
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry}
	tool := makeManagePlaybookTool(core)
	stage := "# Review\n\n## Inputs\n\n| Source | File/Location | Section/Scope | Why |\n| --- | --- | --- | --- |\n\n## Process\n\n1. Write the report.\n2. Stop here. Ask the owner.\n\n## Tools\n\n- write_file\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Report | `output/report.md` | Markdown |\n"
	got := tool.Fn(map[string]any{"action": "create", "name": "needs-owner", "context": "# Needs owner\n", "stages": []any{map[string]any{"name": "01-review", "context": stage}}})
	if !strings.Contains(got, "human checkpoint") {
		t.Fatalf("create = %q, want human-checkpoint rejection", got)
	}
}

func TestWorkspaceLabelsSelfCertifiedStage(t *testing.T) {
	// A stage whose Audit section declares `self` marks the run self-certified:
	// the model judged its own work and the audit trail must say so.
	home := t.TempDir()
	writeWorkspaceStageTool(t, home, "selfish", "search_web")
	// add an Audit section declaring self
	stageDir := filepath.Join(home, "playbooks", "selfish", "stages", "01-work", "CONTEXT.md")
	data, err := os.ReadFile(stageDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stageDir, append(data, []byte("\n## Audit\n\n- self\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	registry.Register(&Tool{Name: "search_web", Behavior: BehaviorObserve})
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
	pb, err := loadPlaybookWorkspace(home, "selfish")
	if err != nil {
		t.Fatal(err)
	}
	run, err := loadOrCreatePlaybookRun(pb, registry, "run", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	stage1, _ := workspaceStage(pb, 1)
	path := playbookRunOutputPath(pb, run, stage1, stage1.Outputs[0])
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("result"), 0600); err != nil {
			t.Fatal(err)
		}
		return &LoopResult{Status: "complete", Reply: "done", ToolCalls: []ToolCall{{Name: "write_file", Args: map[string]any{"path": path}}}}
	}
	result, err := RunPlaybook(context.Background(), core, "selfish", "run", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "complete" {
		t.Fatalf("run = %+v", result)
	}
	if !result.SelfCertified {
		t.Fatal("self-certified stage did not label the run")
	}
}

func TestCapturePlaybookDerivesEvidenceFromAudit(t *testing.T) {
	// teach → compile: after a successful task, capture_playbook must derive the
	// stage's Tools and Outputs from the audit log — real slugs, real filenames,
	// scoped to the current turn only. The model's prose supplies Process only;
	// it cannot invent tools or paths, and stale calls from earlier turns in the
	// same session must not leak into the contract.
	home := t.TempDir()
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	registry.Register(&Tool{Name: "search_web", Behavior: BehaviorObserve})
	db := Connect(home)
	defer db.Close()
	mem := NewMemory(db, nil, settings)
	core := &Core{Settings: settings, DB: db, Tools: registry, Sessions: NewSessionManager(settings, mem), Memory: mem}
	// Simulate the audit trail: stale Gmail work from an earlier turn (same
	// session), then the current task turn (searched, then wrote a report).
	stale := time.Now().UTC().Add(-2 * time.Hour)
	teachStart := time.Now().UTC().Add(-time.Minute)
	now := time.Now().UTC()
	audit := []map[string]any{
		{"event": "turn_start", "session_id": "tg:1", "timestamp": stale.Format(time.RFC3339)},
		{"tool_name": "MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL", "args": map[string]any{}, "status": "ok", "session_id": "tg:1", "timestamp": stale.Format(time.RFC3339)},
		{"tool_name": "bash", "args": map[string]any{"command": "rm -rf /"}, "status": "error", "session_id": "tg:1", "timestamp": stale.Format(time.RFC3339)},
		{"tool_name": "write_file", "args": map[string]any{"path": "/tmp/old-gmail-scan.md"}, "status": "ok", "session_id": "tg:1", "timestamp": stale.Format(time.RFC3339)},
		{"event": "turn_start", "session_id": "tg:1", "timestamp": teachStart.Format(time.RFC3339)},
		{"tool_name": "search_web", "args": map[string]any{"query": "AI news"}, "status": "ok", "session_id": "tg:1", "timestamp": teachStart.Format(time.RFC3339)},
		{"tool_name": "search_web", "args": map[string]any{"query": "model releases"}, "status": "ok", "session_id": "tg:1", "timestamp": teachStart.Format(time.RFC3339)},
		{"tool_name": "write_file", "args": map[string]any{"path": "/home/knowledge/ai-daily/2026-08-01.md"}, "status": "ok", "session_id": "tg:1", "timestamp": teachStart.Format(time.RFC3339)},
		{"tool_name": "write_file", "args": map[string]any{"path": "/tmp/other-session.md"}, "status": "ok", "session_id": "tg:2", "timestamp": now.Format(time.RFC3339)},
		{"event": "turn_start", "session_id": "tg:1", "timestamp": now.Format(time.RFC3339)},
		{"tool_name": "query_audit", "args": map[string]any{}, "status": "ok", "session_id": "tg:1", "timestamp": now.Format(time.RFC3339)},
	}
	var b strings.Builder
	for _, ev := range audit {
		raw, _ := json.Marshal(ev)
		b.Write(raw)
		b.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(home, "audit.jsonl"), []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), sessionIDKey{}, "tg:1")
	tool := makeCapturePlaybookTool(core)
	got := tool.ContextFn(ctx, map[string]any{
		"name":    "daily-news",
		"context": "# Daily news\n",
		"process": "1. Search for the latest AI news. 2. Write the report to /home/knowledge/ai-daily/2026-08-01.md.",
	})
	if !strings.Contains(got, "Created and validated playbook daily-news") {
		t.Fatalf("capture = %q", got)
	}
	pb, err := loadPlaybookWorkspace(home, "daily-news")
	if err != nil {
		t.Fatal(err)
	}
	stage := pb.Stages[0]
	// Tools derived from the current turn only: search_web + write_file. Not the
	// stale MCP/bash calls, not the other session's calls.
	if len(stage.Tools) != 2 || !containsString(stage.Tools, "search_web") || !containsString(stage.Tools, "write_file") {
		t.Fatalf("captured tools = %v, want [search_web write_file]", stage.Tools)
	}
	// Output derived from the current turn's write_file path's basename only.
	if len(stage.Outputs) != 1 || stage.Outputs[0].Path != "output/2026-08-01.md" {
		t.Fatalf("captured outputs = %+v, want [output/2026-08-01.md]", stage.Outputs)
	}
	// Process prose preserved, with the absolute path re-anchored to the run dir.
	if !strings.Contains(stage.Context, "Search for the latest AI news") {
		t.Fatalf("process prose missing: %s", stage.Context)
	}
	if strings.Contains(stage.Context, "/home/knowledge") {
		t.Fatalf("absolute path not re-anchored: %s", stage.Context)
	}
}

func TestCapturePlaybookRequiresEvidence(t *testing.T) {
	// No successful tool calls in the audit log → capture refuses: a playbook
	// must be compiled from a task that actually ran, never improvised.
	home := t.TempDir()
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry}
	if err := os.WriteFile(filepath.Join(home, "audit.jsonl"), []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), sessionIDKey{}, "tg:1")
	got := makeCapturePlaybookTool(core).ContextFn(ctx, map[string]any{
		"name": "ghost", "context": "# Ghost\n", "process": "1. Do things.",
	})
	if !strings.Contains(got, "no completed task turn") {
		t.Fatalf("capture = %q, want turn requirement", got)
	}
}

func TestWorkspaceDoesNotResumeDestructiveStage(t *testing.T) {
	// A failed run whose next incomplete stage is destructive must be terminal:
	// resuming it would re-execute the external side effect (the VPS
	// duplicate-Threads-post incident — the stage posted, then failed
	// verification, and each resume posted again).
	home := t.TempDir()
	writeWorkspaceStageTool(t, home, "destructive", "threads_post")
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	registry.Register(&Tool{Name: "threads_post", Behavior: BehaviorMutate})
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
	pb, err := loadPlaybookWorkspace(home, "destructive")
	if err != nil {
		t.Fatal(err)
	}
	// Create a failed run: stage attempts once, writes nothing, fails verification.
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		return &LoopResult{Status: "complete", Reply: "done"} // no output: fails audit
	}
	result, err := RunPlaybook(context.Background(), core, "destructive", "run", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" {
		t.Fatalf("first run should fail, got %+v", result)
	}
	// The failed run is NOT resumable: the next stage is destructive, so a new
	// invocation must create a fresh run instead of replaying the side effect.
	run, err := loadOrCreatePlaybookRun(pb, registry, "run again", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "running" || len(run.Stages) != 1 || run.Stages[0].Attempts != 0 {
		t.Fatalf("expected fresh run, got %+v", run)
	}
}

func TestWorkspaceRejectsMissingContract(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "playbooks", "bad"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPlaybookWorkspace(home, "bad"); err == nil {
		t.Fatal("missing root contract was accepted")
	}
}

func TestManagePlaybookLifecycle(t *testing.T) {
	home := t.TempDir()
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry}
	tool := makeManagePlaybookTool(core)
	stage := "# Gather\n\n## Inputs\n\n| Source | File/Location | Section/Scope | Why |\n| --- | --- | --- | --- |\n\n## Process\n\n1. Write the report.\n\n## Tools\n\n- write_file\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Report | `output/report.md` | Markdown |\n"
	create := tool.Fn(map[string]any{"action": "create", "name": "daily-report", "context": "# Daily report\n", "stages": []any{map[string]any{"name": "01-gather", "context": stage}}})
	if create != "Created and validated playbook daily-report." {
		t.Fatalf("create = %q", create)
	}
	if got := tool.Fn(map[string]any{"action": "validate", "name": "daily-report"}); got != "Playbook daily-report is valid." {
		t.Fatalf("validate = %q", got)
	}
	if got := tool.Fn(map[string]any{"action": "inspect", "name": "daily-report"}); !strings.Contains(got, "01-gather") {
		t.Fatalf("inspect = %q", got)
	}
	if err := saveSchedules(home, []PlaybookSchedule{{Name: "daily-report", Time: "09:00", Timezone: "Asia/Kuala_Lumpur"}}); err != nil {
		t.Fatal(err)
	}
	if got := tool.Fn(map[string]any{"action": "delete", "name": "daily-report"}); !strings.Contains(got, "cancel") {
		t.Fatalf("scheduled delete = %q", got)
	}
	if err := saveSchedules(home, nil); err != nil {
		t.Fatal(err)
	}
	if got := tool.Fn(map[string]any{"action": "delete", "name": "daily-report"}); !strings.Contains(got, "Deleted") {
		t.Fatalf("delete = %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, "playbooks", "daily-report")); !os.IsNotExist(err) {
		t.Fatalf("playbook still exists: %v", err)
	}
}

func TestManagePlaybookRefusesUpdateWithResumableRun(t *testing.T) {
	home := t.TempDir()
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry}
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	pb, err := loadPlaybookWorkspace(home, "brief")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreatePlaybookRun(pb, registry, "brief", "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	got := makeManagePlaybookTool(core).Fn(map[string]any{"action": "update", "name": "brief", "config": "status: paused\n"})
	if !strings.Contains(got, "resumable run") {
		t.Fatalf("update = %q", got)
	}
	data, err := os.ReadFile(filepath.Join(home, "playbooks", "brief", "config.md"))
	if err == nil && strings.Contains(string(data), "paused") {
		t.Fatalf("resumable playbook was changed: %s", data)
	}
}

func TestManagePlaybookCanonicalizesGeneratedStageContracts(t *testing.T) {
	home := t.TempDir()
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	registry.Register(&Tool{Name: "search_web"})
	core := &Core{Settings: settings, Tools: registry}
	legacy := "## Read\n- Current date.\n\n## Do\nSearch for the news.\n\n## Tools\n- `search_web`\n- `write_file`\n\n## Write\n- `output/news.md`\n"
	got := makeManagePlaybookTool(core).Fn(map[string]any{
		"action": "create", "name": "generated-news", "context": "# Generated news\n",
		"stages": []any{map[string]any{"name": "research", "context": legacy}, map[string]any{"name": "report", "context": legacy}},
	})
	if !strings.Contains(got, "Created and validated") {
		t.Fatalf("create = %q", got)
	}
	pb, err := loadPlaybookWorkspace(home, "generated-news")
	if err != nil {
		t.Fatal(err)
	}
	if pb.Stages[0].Name != "research" || pb.Stages[1].Name != "report" || len(pb.Stages[0].Outputs) != 1 || pb.Stages[0].Tools[0] != "search_web" {
		t.Fatalf("canonical workspace = %+v", pb.Stages)
	}
}

// writeWorkspaceStageTool writes a one-stage playbook whose whitelist is the
// given tools (plus write_file, which every stage must declare).
func writeWorkspaceStageTool(t *testing.T, home, name string, tools ...string) {
	t.Helper()
	root := filepath.Join(home, "playbooks", name)
	if err := os.MkdirAll(filepath.Join(root, "stages", "01-work"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CONTEXT.md"), []byte("# Test playbook\n"), 0600); err != nil {
		t.Fatal(err)
	}
	all := append(append([]string{}, tools...), "write_file")
	var b strings.Builder
	b.WriteString("# Work\n\n## Inputs\n\n| Source | File/Location | Section/Scope | Why |\n| --- | --- | --- | --- |\n\n## Process\n\n1. Produce the result.\n\n## Tools\n\n")
	for _, tool := range all {
		fmt.Fprintf(&b, "- %s\n", tool)
	}
	b.WriteString("\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Result | `output/result.md` | Markdown |\n")
	if err := os.WriteFile(filepath.Join(root, "stages", "01-work", "CONTEXT.md"), []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceDoesNotRetryDestructiveStage(t *testing.T) {
	// A stage whose whitelist contains a destructive tool (bash) must fail loud
	// on the first attempt: retrying a partially-executed destructive stage is
	// how double-deletions happen.
	home := t.TempDir()
	writeWorkspaceStageTool(t, home, "destructive", "bash")
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	registry.Register(&Tool{Name: "bash", Behavior: BehaviorMutate})
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		return &LoopResult{Status: "complete", Reply: "done"} // no output written
	}
	result, err := RunPlaybook(context.Background(), core, "destructive", "run", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" {
		t.Fatalf("destructive stage should fail, got %+v", result)
	}
	if result.StagesRun != 1 {
		t.Fatalf("StagesRun = %d, want 1 (destructive stage must not retry)", result.StagesRun)
	}
	pb, err := loadPlaybookWorkspace(home, "destructive")
	if err != nil {
		t.Fatal(err)
	}
	run2, err := latestPlaybookRun(pb)
	if err != nil || run2 == nil {
		t.Fatalf("latest run: %v %v", run2, err)
	}
	if run2.Stages[0].Attempts != 1 {
		t.Fatalf("Attempts = %d, want 1 (no retry)", run2.Stages[0].Attempts)
	}
}

func TestWorkspaceRetriesReadOnlyStage(t *testing.T) {
	// A read-only whitelist (search_web + write_file) is retry-safe: the first
	// attempt fails verification, the retry succeeds.
	home := t.TempDir()
	writeWorkspaceStageTool(t, home, "readonly", "search_web")
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	registry.Register(&Tool{Name: "search_web", Behavior: BehaviorObserve})
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
	pb, err := loadPlaybookWorkspace(home, "readonly")
	if err != nil {
		t.Fatal(err)
	}
	run, err := loadOrCreatePlaybookRun(pb, registry, "run", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	stage1, _ := workspaceStage(pb, 1)
	path := playbookRunOutputPath(pb, run, stage1, stage1.Outputs[0])
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	attempts := 0
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		attempts++
		if attempts == 1 {
			return &LoopResult{Status: "complete", Reply: "done"} // no output: fails audit
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("result"), 0600); err != nil {
			t.Fatal(err)
		}
		return &LoopResult{Status: "complete", Reply: "done", ToolCalls: []ToolCall{{Name: "write_file", Args: map[string]any{"path": path}}}}
	}
	result, err := RunPlaybook(context.Background(), core, "readonly", "run", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "complete" {
		t.Fatalf("read-only stage should complete after retry, got %+v", result)
	}
	if attempts != 2 || result.StagesRun != 2 {
		t.Fatalf("attempts = %d, StagesRun = %d, want 2 (retried once)", attempts, result.StagesRun)
	}
}

func writeWorkspacePlaybook(t *testing.T, home, name string, stages []string) {
	t.Helper()
	root := filepath.Join(home, "playbooks", name)
	if err := os.MkdirAll(filepath.Join(root, "stages"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CONTEXT.md"), []byte("# Test playbook\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, stage := range stages {
		dir := filepath.Join(root, "stages", stage)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		input := ""
		if stage != stages[0] {
			input = "| Previous stage | `../" + stages[0] + "/output/result.md` | Full file | Handoff |\n"
		}
		content := "# " + stage + "\n\n## Inputs\n\n| Source | File/Location | Section/Scope | Why |\n| --- | --- | --- | --- |\n" + input + "\n## Process\n\n1. Produce the result.\n\n## Tools\n\n- write_file\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Result | `output/result.md` | Markdown |\n"
		if err := os.WriteFile(filepath.Join(dir, "CONTEXT.md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlaybookSystemPromptHasNoClock(t *testing.T) {
	// Cache stability: the playbook system prompt must be byte-stable across
	// stage iterations. The clock is injected into the stage prompt (user role),
	// never into system — a timestamp in system would force a full provider
	// cache rewrite on every iteration (~63% of input billed at full rate).
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
	pb, err := loadPlaybookWorkspace(home, "brief")
	if err != nil {
		t.Fatal(err)
	}
	conversation := core.Sessions.Get("test")
	system := conversation.Session.BuildPlaybookSystem("run it", "")
	if strings.Contains(system, "System time") || strings.Contains(system, "AUTHORITATIVE LOCAL CLOCK") {
		t.Fatalf("system prompt contains a clock: %s", system)
	}
	run, err := loadOrCreatePlaybookRun(pb, registry, "run it", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	stage, _ := workspaceStage(pb, 1)
	// The clock lands in the user-role stage message, not in system.
	stageMsg := buildWorkspaceStagePrompt(pb, run, stage) + "\n\n" + appendSystemTime("", time.Now(), core.Settings.Location())
	if !strings.Contains(stageMsg, "System time") {
		t.Fatalf("stage message missing clock: %s", stageMsg)
	}
}
