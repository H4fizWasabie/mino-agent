package main

import (
	"context"
	"encoding/json"
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
	run, err := loadOrCreatePlaybookRun(pb, "make the briefing", "test", time.Now())
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
	if err != nil || result.Status != "failed" || result.StagesRun != 2 {
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
	if saved.Stages[0].Attempts != 1 || saved.Stages[1].Attempts != 2 || saved.Status != "complete" {
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
	run, err := loadOrCreatePlaybookRun(pb, "cheat attempt", "test", time.Now())
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
	if result.StagesRun != 1 {
		t.Fatalf("StagesRun = %d, want 1 (stage attempted then failed)", result.StagesRun)
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
	if _, err := loadOrCreatePlaybookRun(pb, "brief", "test", time.Now()); err != nil {
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
