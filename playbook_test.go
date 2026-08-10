package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The main loop tail-injects routing + clock into the user message for cache
// stability (RespondForContext). run_playbook must forward only the clean user
// message to the stage — the injected "your first action MUST be
// run_playbook" guidance makes the stage model try to re-run the playbook
// from inside the stage and bail when the tool is whitelisted out (issue #97).
func TestCleanPlaybookRequest(t *testing.T) {
	tests := []struct {
		name    string
		request string
		want    string
	}{
		{
			"explicit routing + clock tail stripped",
			"Run the threads-replies playbook now.\n\n\nThe user explicitly asked to run the \"threads-replies\" playbook. Your first action MUST be run_playbook with name=\"threads-replies\". Do NOT do the work yourself first — the playbook's stages perform the task. Run it, then report the result.\n\n[AUTHORITATIVE LOCAL CLOCK: Monday, 2026-08-10 10:04:35 +08 (UTC+08:00). Today is 2026-08-10.]\nUse this clock; do not infer the current time from conversation history.",
			"Run the threads-replies playbook now.",
		},
		{
			"soft routing + clock tail stripped",
			"anything about the news?\n\nPOSSIBLY RELEVANT PLAYBOOK: \"malaysian-news-daily\" — daily news\nUse run_playbook with name=\"malaysian-news-daily\" only if this repeatable procedure is the best fit for the current request. Otherwise handle the request normally.\n\n[AUTHORITATIVE LOCAL CLOCK: Monday, 2026-08-10 10:04:35 +08 (UTC+08:00). Today is 2026-08-10.]",
			"anything about the news?",
		},
		{
			"clock-only tail stripped",
			"Scheduled run\n\n[AUTHORITATIVE LOCAL CLOCK: Monday, 2026-08-10 10:04:35 +08 (UTC+08:00). Today is 2026-08-10.]\nUse this clock; do not infer the current time from conversation history.",
			"Scheduled run",
		},
		{
			"plain message unchanged",
			"Run the playbook",
			"Run the playbook",
		},
		{
			"user text after a clock-like marker is trimmed (documented tradeoff)",
			"First line\nSecond line\n\n[AUTHORITATIVE LOCAL CLOCK: something the user typed]\nthird",
			"First line\nSecond line",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanPlaybookRequest(tt.request); got != tt.want {
				t.Fatalf("cleanPlaybookRequest(%q) = %q, want %q", tt.request, got, tt.want)
			}
		})
	}
}

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

	// Issue #42: a stray relative .mino prefix must be rejected with the
	// corrected absolute path (it resolves via CWD but breaks attribution).
	rel := ".mino/playbooks/brief/runs/run-123/stages/01-collect/output/result.md"
	if guard := playbookWriteGuard(home, rel, ctx); guard == "" || !strings.Contains(guard, "absolute path") {
		t.Fatalf("relative .mino prefix not rejected: %q (guard=%q)", rel, guard)
	}
	if guard := playbookWriteGuard(home, rel, ctx); !strings.Contains(guard, filepath.Join(home, "playbooks")) {
		t.Fatalf("relative .mino guard missing the corrected path: %q", guard)
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
	if !strings.Contains(create, "Created and validated playbook daily-report") || !strings.Contains(create, "playbooks/daily-report/") {
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
	stageMsg := buildWorkspaceStagePrompt(pb, run, stage, time.Now(), time.Local) + "\n\n" + appendSystemTime("", time.Now(), core.Settings.Location())
	if !strings.Contains(stageMsg, "System time") {
		t.Fatalf("stage message missing clock: %s", stageMsg)
	}
}

// Declared stage inputs must actually resolve (issue #86): Runtime sources
// render the run clock, glob paths expand (newest first, path-attributed,
// bounded), an empty glob is a valid empty list rather than an error, and
// absolute declared paths resolve as-is instead of being double-joined under
// the playbook dir. Only a genuinely broken literal path stays "Unavailable".
func TestWorkspaceStageInputsResolve(t *testing.T) {
	home := t.TempDir()
	stageDir := filepath.Join(home, "playbooks", "brief", "stages", "01-collect")
	if err := os.MkdirAll(stageDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "playbooks", "brief", "CONTEXT.md"), []byte("# Test playbook\n"), 0600); err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(home, "logs")
	empty := filepath.Join(home, "empty")
	abs := filepath.Join(home, "abs")
	for _, dir := range []string{logs, empty, abs} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	oldLog := filepath.Join(logs, "a.md")
	newLog := filepath.Join(logs, "b.md")
	if err := os.WriteFile(oldLog, []byte("OLD LOG"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newLog, []byte("NEW LOG"), 0600); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(oldLog, base, base.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newLog, base, base.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	absLog := filepath.Join(abs, "x.md")
	if err := os.WriteFile(absLog, []byte("ABS LOG"), 0600); err != nil {
		t.Fatal(err)
	}
	content := "# Collect\n\n## Inputs\n\n| Source | File/Location | Section/Scope | Why |\n| --- | --- | --- | --- |\n" +
		"| Runtime | Authoritative local date | Full | Date the post |\n" +
		"| Logs | `" + filepath.Join(logs, "*.md") + "` | Most recent | Exclusion list |\n" +
		"| Empty glob | `" + filepath.Join(empty, "*.md") + "` | All | No logs yet |\n" +
		"| Abs logs | `" + filepath.Join(abs, "*.md") + "` | All | Absolute path |\n" +
		"| Missing | `" + filepath.Join(home, "nope.md") + "` | Full | Gone |\n\n" +
		"## Process\n\n1. Do it.\n\n## Tools\n\n- write_file\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Result | `output/result.md` | Markdown |\n"
	if err := os.WriteFile(filepath.Join(stageDir, "CONTEXT.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	pb, err := loadPlaybookWorkspace(home, "brief")
	if err != nil {
		t.Fatal(err)
	}
	run, err := loadOrCreatePlaybookRun(pb, registry, "run it", "test", base)
	if err != nil {
		t.Fatal(err)
	}
	stage, _ := workspaceStage(pb, 1)
	loc := time.FixedZone("MYT", 8*3600)
	now := time.Date(2026, 8, 10, 8, 30, 0, 0, loc)
	msg := buildWorkspaceStagePrompt(pb, run, stage, now, loc)

	tests := []struct {
		name    string
		want    string
		notWant string
	}{
		{"runtime source renders the run clock", "### Authoritative local date\n2026-08-10 (Monday)\n", "### Authoritative local date\nUnavailable"},
		{"glob match carries its path header", "--- " + newLog + " ---\nNEW LOG", ""},
		{"empty glob is an empty list, not an error", "### " + filepath.Join(empty, "*.md") + "\nNo files matched.\n", "### " + filepath.Join(empty, "*.md") + "\nUnavailable"},
		{"absolute glob path resolves", "--- " + absLog + " ---\nABS LOG", ""},
		{"literal missing file stays unavailable", "### " + filepath.Join(home, "nope.md") + "\nUnavailable:", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(msg, tt.want) {
				t.Fatalf("expected %q in stage prompt:\n%s", tt.want, msg)
			}
			if tt.notWant != "" && strings.Contains(msg, tt.notWant) {
				t.Fatalf("did not expect %q in stage prompt:\n%s", tt.notWant, msg)
			}
		})
	}
	// Newest match first (mtime), so the exclusion list is most-recent-led.
	if i, j := strings.Index(msg, "NEW LOG"), strings.Index(msg, "OLD LOG"); i < 0 || j < 0 || i > j {
		t.Fatalf("glob matches not newest-first:\n%s", msg)
	}
}

// A stage whose declared outputs are verified must complete even when the
// final model call flaked (status "error") — the outputs are the contract.
// Observed 2026-08-08: the 09:30 run failed with "all vision providers
// failed" while the post was already published and the log written.
func TestWorkspaceCompletesWhenOutputsVerifiedDespiteRuntimeError(t *testing.T) {
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
	run, err := loadOrCreatePlaybookRun(pb, registry, "make the briefing", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	stage1, _ := workspaceStage(pb, 1)
	path1 := playbookRunOutputPath(pb, run, stage1, stage1.Outputs[0])
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		// The stage writes its output, then the final model call flakes.
		if err := os.MkdirAll(filepath.Dir(path1), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path1, []byte("collected"), 0600); err != nil {
			t.Fatal(err)
		}
		return &LoopResult{Status: "error", Reply: "(error: all vision providers failed: empty model response)", ToolCalls: []ToolCall{{Name: "write_file", Args: map[string]any{"path": path1}}}}
	}
	result, err := RunPlaybook(context.Background(), core, "brief", "make the briefing", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete (outputs verified despite runtime error)", result.Status)
	}
	if !strings.Contains(result.Reply, "work verified complete") {
		t.Fatalf("reply = %q, want the verified-complete note", result.Reply)
	}
}

// Control: a runtime error WITHOUT verified outputs still fails the stage.
func TestWorkspaceFailsWhenRuntimeErrorAndOutputsMissing(t *testing.T) {
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		return &LoopResult{Status: "error", Reply: "(error: empty model response)"}
	}
	result, err := RunPlaybook(context.Background(), core, "brief", "make the briefing", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed (no outputs written)", result.Status)
	}
}

// Issue #22: absolute output paths are quarantined outputs — enforced like
// any declared output, but resolved as-is (outside the run workspace).
func TestPlaybookRunOutputPathResolvesAbsoluteAsQuarantined(t *testing.T) {
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	pb, err := loadPlaybookWorkspace(home, "brief")
	if err != nil {
		t.Fatal(err)
	}
	stage, _ := workspaceStage(pb, 1)
	run, _ := loadOrCreatePlaybookRun(pb, NewRegistry(), "x", "test", time.Now())
	rel := playbookRunOutputPath(pb, run, stage, stage.Outputs[0])
	if filepath.IsAbs(rel) == false || !strings.Contains(rel, "runs") {
		t.Fatalf("relative output path not joined under run dir: %q", rel)
	}
	abs := playbookRunOutputPath(pb, run, stage, StageOutput{Name: "quarantined", Path: "/home/mino/.mino/data/threads-replies/digest.md"})
	if abs != "/home/mino/.mino/data/threads-replies/digest.md" {
		t.Fatalf("absolute output path not preserved: %q", abs)
	}
}

// appendQuarantinedOutput declares an absolute-path output on disk — the
// way the contract author would write it.
func appendQuarantinedOutput(t *testing.T, home, stage, absPath string) {
	t.Helper()
	p := filepath.Join(home, "playbooks", "brief", "stages", stage, "CONTEXT.md")
	s, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s = append(s, []byte("| Digest | `"+absPath+"` | Markdown |\n")...)
	if err := os.WriteFile(p, s, 0600); err != nil {
		t.Fatal(err)
	}
}

// A stage declaring a quarantined (absolute) output cannot complete until it
// is written; once written, the stage completes with it.
func TestWorkspaceQuarantinedOutputEnforced(t *testing.T) {
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	qpath := filepath.Join(home, "data", "digest.md")
	appendQuarantinedOutput(t, home, "01-collect", qpath)
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
	pb, err := loadPlaybookWorkspace(home, "brief")
	if err != nil {
		t.Fatal(err)
	}
	stage1, _ := workspaceStage(pb, 1)
	run, err := loadOrCreatePlaybookRun(pb, registry, "x", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	path1 := playbookRunOutputPath(pb, run, stage1, stage1.Outputs[0])
	if got := playbookRunOutputPath(pb, run, stage1, stage1.Outputs[1]); got != qpath {
		t.Fatalf("quarantined path = %q, want %q", got, qpath)
	}
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	// First attempt: only the normal output written — the quarantined one is
	// missing, so the stage must not complete; the injected loop gets a second
	// call, writes both, and the run completes.
	attempts := 0
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		attempts++
		if attempts == 1 {
			os.MkdirAll(filepath.Dir(path1), 0700)
			os.WriteFile(path1, []byte("x"), 0600)
			return &LoopResult{Status: "complete", Reply: "done", ToolCalls: []ToolCall{{Name: "write_file", Args: map[string]any{"path": path1}}}}
		}
		os.MkdirAll(filepath.Dir(qpath), 0700)
		os.WriteFile(qpath, []byte("digest"), 0600)
		return &LoopResult{Status: "complete", Reply: "done", ToolCalls: []ToolCall{{Name: "write_file", Args: map[string]any{"path": path1}}, {Name: "write_file", Args: map[string]any{"path": qpath}}}}
	}
	result, err := RunPlaybook(context.Background(), core, "brief", "x", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete after quarantined output written (reply: %q)", result.Status, result.Reply)
	}
	if attempts < 2 {
		t.Fatalf("stage loop ran %d times, want 2 (first attempt must be blocked by the missing quarantined output)", attempts)
	}
}

// Quarantined outputs are never recorded as artifacts (no distill), while
// normal outputs are.
func TestQuarantinedOutputsSkippedFromArtifacts(t *testing.T) {
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	qpath := filepath.Join(home, "data", "digest.md")
	appendQuarantinedOutput(t, home, "01-collect", qpath)
	db := Connect(t.TempDir())
	defer db.Close()
	mem := NewMemory(db, nil, &Settings{Home: home, TopK: 4, ConsolidateEvery: 0})
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil), Memory: mem}
	pb, err := loadPlaybookWorkspace(home, "brief")
	if err != nil {
		t.Fatal(err)
	}
	stage1, _ := workspaceStage(pb, 1)
	run, err := loadOrCreatePlaybookRun(pb, registry, "x", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	path1 := playbookRunOutputPath(pb, run, stage1, stage1.Outputs[0])
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		os.MkdirAll(filepath.Dir(path1), 0700)
		os.WriteFile(path1, []byte("x"), 0600)
		os.MkdirAll(filepath.Dir(qpath), 0700)
		os.WriteFile(qpath, []byte("digest"), 0600)
		return &LoopResult{Status: "complete", Reply: "done", ToolCalls: []ToolCall{{Name: "write_file", Args: map[string]any{"path": path1}}, {Name: "write_file", Args: map[string]any{"path": qpath}}}}
	}
	if _, err := RunPlaybook(context.Background(), core, "brief", "x", "test", nil); err != nil {
		t.Fatal(err)
	}
	var artifacts []string
	rows, err := db.Query("SELECT path FROM session_artifacts")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		rows.Scan(&p)
		artifacts = append(artifacts, p)
	}
	if len(artifacts) != 1 || artifacts[0] != path1 {
		t.Fatalf("artifacts = %v, want only the normal output %q (quarantined must not distill)", artifacts, path1)
	}
}

// Issue #44: run_playbook from inside a stage of the SAME playbook must be
// rejected with a corrective error (no re-run / no recursion).
func TestRunPlaybookRejectsSamePlaybookRecursion(t *testing.T) {
	core := &Core{}
	tool := makeRunPlaybookTool(core)
	// Inside a stage of "brief".
	stageCtx := context.WithValue(context.Background(), traceTagKey{}, map[string]string{
		"playbook": "brief",
		"stage":    "01-collect",
		"run":      "run-123",
	})
	out := tool.ContextFn(stageCtx, map[string]any{"name": "brief"})
	if !strings.Contains(out, "already inside playbook brief") {
		t.Fatalf("same-playbook recursion not rejected: %q", out)
	}
	// The guard fires before any core use; allowed paths (cross-playbook and
	// chat) proceed unchanged — verify the guard passes them through.
	for _, tc := range []struct {
		ctx  context.Context
		name string
	}{
		{stageCtx, "other-playbook"},          // cross-playbook delegation allowed
		{context.Background(), "brief"},      // chat context, no stage tags
	} {
		func() {
			defer func() { recover() }() // nil test Core panics past the guard — fine
			out = tool.ContextFn(tc.ctx, map[string]any{"name": tc.name})
			if strings.Contains(out, "already inside") {
				t.Fatalf("allowed path wrongly rejected: %q", out)
			}
		}()
	}
}

// --- Schedule reliability (issue #74): serial dispatcher, 1-minute window,
// no catch-up and no missed-run record made due-but-never-fired schedules
// invisible. Tests cover the classify seam, parallel dispatch, boot catch-up
// and missed-run notification. ---

type callRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *callRecorder) record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name)
}

func (r *callRecorder) has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if c == name {
			return true
		}
	}
	return false
}

func (r *callRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *callRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func runnerRecording(rec *callRecorder) scheduledPlaybookRunner {
	return func(ctx context.Context, core *Core, name, purpose, sessionID string, obs Observer) (*PlaybookResult, error) {
		rec.record(name)
		return &PlaybookResult{Status: "completed"}, nil
	}
}

func waitForCall(t *testing.T, rec *callRecorder, name string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rec.has(name) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("runner was not called for %q (calls: %v)", name, rec.all())
}

// waitForRows waits until the responsibility table has at least want rows, so
// no spawned run is still writing when the temp dir is torn down.
func waitForRows(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM responsibility_events").Scan(&n); err == nil && n >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected >= %d responsibility rows, saw fewer", want)
}

func newScheduleTestCore(t *testing.T) (*Core, string) {
	t.Helper()
	home := t.TempDir()
	db := Connect(home)
	t.Cleanup(func() { db.Close() })
	return &Core{
		Settings:         &Settings{Home: home},
		DB:               db,
		Responsibilities: NewResponsibilityStore(db),
	}, home
}

func mustKL(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Kuala_Lumpur")
	if err != nil {
		t.Fatalf("load KL timezone: %v", err)
	}
	return loc
}

func TestClassifySchedule(t *testing.T) {
	loc := mustKL(t)
	at := func(d, h, m int) time.Time { return time.Date(2026, 8, d, h, m, 0, 0, loc) }
	const tz = "Asia/Kuala_Lumpur"
	today1300 := "2026-08-10T05:00:00Z"  // today 13:00 KL
	today1305 := "2026-08-10T05:00:05Z"  // today 13:00:05 KL
	yesterday09 := "2026-08-09T01:00:00Z" // yesterday 09:00 KL
	future := "2026-08-11T05:00:00Z"

	tests := []struct {
		name      string
		s         PlaybookSchedule
		now       time.Time
		allowLate bool
		want      scheduleAction
	}{
		{"in window fires", PlaybookSchedule{Time: "13:00", Timezone: tz}, at(10, 13, 0), false, scheduleFire},
		{"in window fires in catch-up mode too", PlaybookSchedule{Time: "13:00", Timezone: tz}, at(10, 13, 0), true, scheduleFire},
		{"before window skips", PlaybookSchedule{Time: "13:00", Timezone: tz}, at(10, 12, 59), false, scheduleSkip},
		{"before window: catch-up sees yesterday's occurrence missed", PlaybookSchedule{Time: "13:00", Timezone: tz, LastRun: yesterday09}, at(10, 12, 59), true, scheduleMissed},
		{"after window skips in tick mode", PlaybookSchedule{Time: "13:00", Timezone: tz}, at(10, 13, 2), false, scheduleSkip},
		{"after window same day fires late in catch-up mode", PlaybookSchedule{Time: "13:00", Timezone: tz}, at(10, 13, 2), true, scheduleFire},
		{"covered by today's run skips", PlaybookSchedule{Time: "13:00", Timezone: tz, LastRun: today1305}, at(10, 13, 0), false, scheduleSkip},
		{"covered by today's run skips catch-up too", PlaybookSchedule{Time: "13:00", Timezone: tz, LastRun: today1305}, at(10, 14, 0), true, scheduleSkip},
		{"run exactly at occurrence covers", PlaybookSchedule{Time: "13:00", Timezone: tz, LastRun: today1300}, at(10, 13, 0), false, scheduleSkip},
		{"yesterday's run does not cover today's occurrence", PlaybookSchedule{Time: "13:00", Timezone: tz, LastRun: yesterday09}, at(10, 13, 0), false, scheduleFire},
		{"next-day old miss is missed", PlaybookSchedule{Time: "13:00", Timezone: tz, LastRun: yesterday09}, at(11, 8, 0), true, scheduleMissed},
		{"never-run old occurrence is missed at classify level", PlaybookSchedule{Time: "13:00", Timezone: tz}, at(11, 8, 0), true, scheduleMissed},
		{"future LastRun covers", PlaybookSchedule{Time: "13:00", Timezone: tz, LastRun: future}, at(10, 13, 0), false, scheduleSkip},
		{"invalid timezone skips", PlaybookSchedule{Time: "13:00", Timezone: "Mars/Olympus"}, at(10, 13, 0), false, scheduleSkip},
		{"invalid time skips", PlaybookSchedule{Time: "25:99", Timezone: tz}, at(10, 13, 0), false, scheduleSkip},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifySchedule(tt.s, tt.now, tt.allowLate); got != tt.want {
				t.Fatalf("classifySchedule(%+v, %v, allowLate=%v) = %v, want %v", tt.s, tt.now, tt.allowLate, got, tt.want)
			}
		})
	}
}

func TestDispatchSlowRunDoesNotStarveSibling(t *testing.T) {
	core, home := newScheduleTestCore(t)
	loc := mustKL(t)
	rec := &callRecorder{}
	release := make(chan struct{})
	run := func(ctx context.Context, core *Core, name, purpose, sessionID string, obs Observer) (*PlaybookResult, error) {
		rec.record(name)
		if name == "slow" {
			<-release // block the slow run until the test releases it
		}
		return &PlaybookResult{Status: "completed"}, nil
	}
	base := time.Date(2026, 8, 10, 13, 0, 0, 0, loc)
	if err := saveSchedules(home, []PlaybookSchedule{
		{Name: "slow", Time: "13:00", Timezone: "Asia/Kuala_Lumpur"},
		{Name: "fast", Time: "13:01", Timezone: "Asia/Kuala_Lumpur"},
	}); err != nil {
		t.Fatal(err)
	}

	// Pass 1: slow is due and blocks inside the runner.
	dispatchDueSchedulesAt(core, base.Add(30*time.Second), run)
	waitForCall(t, rec, "slow")

	// Pass 2 one minute later: fast is due. Under the old serial dispatcher
	// this pass could not even start while slow blocked.
	dispatchDueSchedulesAt(core, base.Add(90*time.Second), run)
	waitForCall(t, rec, "fast")

	close(release)
	waitForRows(t, core.DB, 4) // both runs fully recorded before teardown
}

func TestDispatchAlreadyRanTodaySkips(t *testing.T) {
	core, home := newScheduleTestCore(t)
	loc := mustKL(t)
	rec := &callRecorder{}
	if err := saveSchedules(home, []PlaybookSchedule{{
		Name: "daily", Time: "13:00", Timezone: "Asia/Kuala_Lumpur",
		LastRun: "2026-08-10T05:00:05Z", // today's 13:00:05 KL
	}}); err != nil {
		t.Fatal(err)
	}
	dispatchDueSchedulesAt(core, time.Date(2026, 8, 10, 13, 0, 30, 0, loc), runnerRecording(rec))
	if rec.count() != 0 {
		t.Fatalf("already-ran schedule fired: %v", rec.all())
	}
}

func TestDispatchFiresInWindowAndClaimsSlot(t *testing.T) {
	core, home := newScheduleTestCore(t)
	loc := mustKL(t)
	rec := &callRecorder{}
	if err := saveSchedules(home, []PlaybookSchedule{{
		Name: "daily", Time: "13:00", Timezone: "Asia/Kuala_Lumpur",
	}}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 10, 13, 0, 30, 0, loc)
	dispatchDueSchedulesAt(core, now, runnerRecording(rec))
	waitForCall(t, rec, "daily")
	got, err := loadSchedules(home)
	if err != nil || len(got) != 1 || got[0].LastRun == "" {
		t.Fatalf("LastRun not claimed synchronously: %+v (err %v)", got, err)
	}
	// A second pass inside the same window must not double-fire.
	dispatchDueSchedulesAt(core, now.Add(20*time.Second), runnerRecording(rec))
	if rec.count() != 1 {
		t.Fatalf("schedule fired %d times, want 1", rec.count())
	}
	waitForRows(t, core.DB, 2)
}

func TestDuplicateScheduleNameFiresOnce(t *testing.T) {
	core, home := newScheduleTestCore(t)
	loc := mustKL(t)
	rec := &callRecorder{}
	if err := saveSchedules(home, []PlaybookSchedule{
		{Name: "daily", Time: "13:00", Timezone: "Asia/Kuala_Lumpur"},
		{Name: "daily", Time: "13:00", Timezone: "Asia/Kuala_Lumpur"},
	}); err != nil {
		t.Fatal(err)
	}
	dispatchDueSchedulesAt(core, time.Date(2026, 8, 10, 13, 0, 30, 0, loc), runnerRecording(rec))
	waitForCall(t, rec, "daily")
	if rec.count() != 1 {
		t.Fatalf("duplicate schedule entries fired %d times, want 1", rec.count())
	}
	waitForRows(t, core.DB, 2)
}

func TestCatchUpFiresLateSameDay(t *testing.T) {
	core, home := newScheduleTestCore(t)
	loc := mustKL(t)
	rec := &callRecorder{}
	if err := saveSchedules(home, []PlaybookSchedule{{
		Name: "daily", Time: "13:00", Timezone: "Asia/Kuala_Lumpur",
	}}); err != nil {
		t.Fatal(err)
	}
	catchUpSchedulesAt(core, time.Date(2026, 8, 10, 14, 0, 0, 0, loc), runnerRecording(rec))
	waitForCall(t, rec, "daily")
	got, err := loadSchedules(home)
	if err != nil || len(got) != 1 || got[0].LastRun == "" {
		t.Fatalf("catch-up run not claimed: %+v (err %v)", got, err)
	}
	if got[0].MissedAt != "" {
		t.Fatalf("caught-up run must not be marked missed: %+v", got[0])
	}
	waitForRows(t, core.DB, 2)
}

func TestCatchUpRecordsMissedRunAndNotifiesOnce(t *testing.T) {
	core, home := newScheduleTestCore(t)
	loc := mustKL(t)
	rec := &callRecorder{}
	if err := saveSchedules(home, []PlaybookSchedule{{
		Name: "daily", Time: "13:00", Timezone: "Asia/Kuala_Lumpur",
		LastRun: "2026-08-09T01:00:00Z", // ran yesterday 09:00 KL; yesterday 13:00 missed
	}}); err != nil {
		t.Fatal(err)
	}
	nextDay8AM := time.Date(2026, 8, 11, 8, 0, 0, 0, loc)
	catchUpSchedulesAt(core, nextDay8AM, runnerRecording(rec))
	if rec.count() != 0 {
		t.Fatalf("old miss must not fire: %v", rec.all())
	}
	got, err := loadSchedules(home)
	if err != nil || len(got) != 1 || got[0].MissedAt == "" {
		t.Fatalf("missed run not recorded: %+v (err %v)", got, err)
	}
	outbox := filepath.Join(home, "outbox", "msg_owner.txt")
	data, err := os.ReadFile(outbox)
	if err != nil {
		t.Fatalf("no missed-run notice in outbox: %v", err)
	}
	if !strings.Contains(string(data), "daily") {
		t.Fatalf("missed-run notice missing schedule name: %s", data)
	}

	// A second boot must not notify again.
	catchUpSchedulesAt(core, nextDay8AM.Add(time.Minute), runnerRecording(rec))
	entries, err := os.ReadDir(filepath.Join(home, "outbox"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("miss notified more than once: %v (err %v)", entries, err)
	}
}

func TestCatchUpNeverRunScheduleIsNotAMiss(t *testing.T) {
	core, home := newScheduleTestCore(t)
	loc := mustKL(t)
	rec := &callRecorder{}
	if err := saveSchedules(home, []PlaybookSchedule{{
		Name: "daily", Time: "13:00", Timezone: "Asia/Kuala_Lumpur",
	}}); err != nil {
		t.Fatal(err)
	}
	catchUpSchedulesAt(core, time.Date(2026, 8, 11, 8, 0, 0, 0, loc), runnerRecording(rec))
	if rec.count() != 0 {
		t.Fatalf("never-run schedule fired: %v", rec.all())
	}
	got, err := loadSchedules(home)
	if err != nil || len(got) != 1 || got[0].MissedAt != "" {
		t.Fatalf("never-run schedule marked missed: %+v (err %v)", got, err)
	}
	if _, err := os.Stat(filepath.Join(home, "outbox", "msg_owner.txt")); !os.IsNotExist(err) {
		t.Fatalf("never-run schedule notified: %v", err)
	}
}
