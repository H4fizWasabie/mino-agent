package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// SCR-003 hybrid runner: script + LLM stages in one playbook.

func writeHybridPlaybook(t *testing.T, home, name string) {
	t.Helper()
	dir := filepath.Join(home, "playbooks", name)
	must := func(p, c string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(c), 0700); err != nil {
			t.Fatal(err)
		}
	}
	must("CONTEXT.md", "# "+name+"\n")
	must("config.md", "status: active\n")
	// stage 01: LLM stage (CONTEXT.md)
	must("stages/01-think/CONTEXT.md", `# Think

## Process
1. Write the answer to output/thought.md.

## Tools
- write_file

## Outputs
| Artifact | Location | Format |
| --- | --- | --- |
| Thought | output/thought.md | Markdown |
`)
	// stage 02: script stage (script.sh, no CONTEXT.md)
	must("stages/02-post/script.sh", "#!/bin/bash\nprintf 'posted from script\\n' > output/posted.txt\n")
}

func TestHybridPlaybookRunsScriptThenLLMStage(t *testing.T) {
	home := t.TempDir()
	writeHybridPlaybook(t, home, "hybrid")
	pb, err := loadPlaybookWorkspace(home, "hybrid")
	if err != nil {
		t.Fatal(err)
	}
	if len(pb.Stages) != 2 {
		t.Fatalf("stages = %d, want 2", len(pb.Stages))
	}
	if pb.Stages[0].Script != "" || pb.Stages[1].Script != "script.sh" {
		t.Fatalf("stage kinds wrong: %q / %q", pb.Stages[0].Script, pb.Stages[1].Script)
	}
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}

	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	llmCalls := 0
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		llmCalls++
		// the LLM stage must write its declared output (thought.md)
		out := filepath.Join(home, "playbooks", "hybrid", "runs")
		_ = out
		// find the run dir the harness created
		entries, _ := os.ReadDir(out)
		var runDir string
		for _, e := range entries {
			if e.IsDir() {
				runDir = filepath.Join(out, e.Name())
			}
		}
		thought := filepath.Join(runDir, "stages", "01-think", "output", "thought.md")
		if err := os.MkdirAll(filepath.Dir(thought), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(thought, []byte("the thought"), 0600); err != nil {
			t.Fatal(err)
		}
		return &LoopResult{Status: "complete", Reply: "thought written", ToolCalls: []ToolCall{{Name: "write_file", Args: map[string]any{"path": thought}}}}
	}

	result, err := RunPlaybook(context.Background(), core, "hybrid", "run it", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete (reply=%q)", result.Status, result.Reply)
	}
	if llmCalls != 1 {
		t.Fatalf("LLM stage loop called %d times, want 1", llmCalls)
	}
	// the script stage produced its output
	entries, _ := os.ReadDir(filepath.Join(home, "playbooks", "hybrid", "runs"))
	var posted bool
	for _, e := range entries {
		p := filepath.Join(home, "playbooks", "hybrid", "runs", e.Name(), "stages", "02-post", "output", "posted.txt")
		if data, err := os.ReadFile(p); err == nil && strings.Contains(string(data), "posted from script") {
			posted = true
		}
	}
	if !posted {
		t.Fatal("script stage output posted.txt not found")
	}
}

func TestHybridPlaybookScriptStageFailureFailsRun(t *testing.T) {
	home := t.TempDir()
	writeHybridPlaybook(t, home, "hybrid")
	// break the script stage
	if err := os.WriteFile(filepath.Join(home, "playbooks", "hybrid", "stages", "02-post", "script.sh"), []byte("#!/bin/bash\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		// write the LLM stage's declared output so the run reaches stage 02
		out := filepath.Join(home, "playbooks", "hybrid", "runs")
		entries, _ := os.ReadDir(out)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			thought := filepath.Join(out, e.Name(), "stages", "01-think", "output", "thought.md")
			_ = os.MkdirAll(filepath.Dir(thought), 0700)
			_ = os.WriteFile(thought, []byte("the thought"), 0600)
		}
		thought := ""
		entries, _ = os.ReadDir(out)
		for _, e := range entries {
			if e.IsDir() {
				thought = filepath.Join(out, e.Name(), "stages", "01-think", "output", "thought.md")
			}
		}
		return &LoopResult{Status: "complete", Reply: "ok", ToolCalls: []ToolCall{{Name: "write_file", Args: map[string]any{"path": thought}}}}
	}
	result, err := RunPlaybook(context.Background(), core, "hybrid", "run it", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed (reply=%q)", result.Status, result.Reply)
	}
	if !strings.Contains(result.Reply, "02-post") {
		t.Fatalf("reply should name the failing stage: %q", result.Reply)
	}
}

func TestRunScriptStageExecutesAndRecords(t *testing.T) {
	home := t.TempDir()
	writeHybridPlaybook(t, home, "hybrid")
	pb, err := loadPlaybookWorkspace(home, "hybrid")
	if err != nil {
		t.Fatal(err)
	}
	core := &Core{Settings: &Settings{Home: home}, Tools: NewRegistry()}
	run := &PlaybookRun{ID: "R1"}
	out, code, err := runScriptStage(context.Background(), core, pb, run, &pb.Stages[1], "scheduled-hybrid")
	if err != nil || code != 0 {
		t.Fatalf("runScriptStage: code=%d err=%v out=%q", code, err, out)
	}
	// the script writes its declared output inside the run-scoped stage dir
	posted := filepath.Join(home, "playbooks", "hybrid", "runs", "R1", "stages", "02-post", "output", "posted.txt")
	data, err := os.ReadFile(posted)
	if err != nil || !strings.Contains(string(data), "posted from script") {
		t.Fatalf("posted.txt = %q err=%v, want the script's write", data, err)
	}
	so := filepath.Join(home, "playbooks", "hybrid", "runs", "R1", "stages", "02-post", "script-output.txt")
	if _, err := os.Stat(so); err != nil {
		t.Fatalf("script-output.txt missing: %v", err)
	}
	// fail-fast: non-zero exit
	pb2, _ := loadPlaybookWorkspace(home, "hybrid")
	if err := os.WriteFile(filepath.Join(pb2.Stages[1].Dir, "script.sh"), []byte("#!/bin/bash\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, code, err = runScriptStage(context.Background(), core, pb2, run, &pb2.Stages[1], "scheduled-hybrid")
	if err != nil || code != 7 {
		t.Fatalf("expected exit 7, got code=%d err=%v", code, err)
	}
}

func TestValidateStageScriptsRejectsBadBash(t *testing.T) {
	home := t.TempDir()
	writeHybridPlaybook(t, home, "hybrid")
	pb, err := loadPlaybookWorkspace(home, "hybrid")
	if err != nil {
		t.Fatal(err)
	}
	core := &Core{Settings: &Settings{Home: home}, Tools: NewRegistry()}
	if err := validateStageScripts(core, pb); err != nil {
		t.Fatalf("valid stage scripts rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pb.Stages[1].Dir, "script.sh"), []byte("if then\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateStageScripts(core, pb); err == nil {
		t.Fatal("invalid stage script accepted")
	}
}

var _ = time.Now // keep time import if helpers change

// TestValidateScriptFile covers the named seam (REL-04): bash -n + tool scan
// on a single script — the playbook-level and stage-level validators share it.
func TestValidateScriptFile(t *testing.T) {
	home := t.TempDir()
	r := NewRegistry()
	r.Register(&Tool{Name: "list_playbooks", Fn: func(map[string]any) string { return "" }})
	core := &Core{Settings: &Settings{Home: home}, Tools: r}
	p := filepath.Join(home, "ok.sh")
	if err := os.WriteFile(p, []byte("#!/bin/bash\nmino exec list_playbooks '{}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateScriptFile(core, p, "ok"); err != nil {
		t.Fatalf("valid script rejected: %v", err)
	}
	if err := os.WriteFile(p, []byte("if then\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateScriptFile(core, p, "bad"); err == nil {
		t.Fatal("invalid bash accepted")
	}
}
