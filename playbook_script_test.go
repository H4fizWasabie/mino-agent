package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAINewsFetchAcceptsMarkdownSourceLabel(t *testing.T) {
	home := t.TempDir()
	stage := filepath.Join(home, "stages", "02-fetch")
	if err := os.MkdirAll(filepath.Join(home, "stages", "01-judgment", "output"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stage, 0700); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("playbook_defaults/ai-news-daily/stages/02-fetch/script.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "script.sh"), script, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "stages", "01-judgment", "output", "topics.md"), []byte("## Story\n**Source:** https://example.com/story\nKey claim: verified\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(home, "story.html")
	if err := os.WriteFile(fixture, []byte("<title>Story</title><p>A verified article paragraph with enough text.</p>"), 0600); err != nil {
		t.Fatal(err)
	}
	curl := "#!/bin/bash\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"-o\" ]; then shift; cp \"$FIXTURE\" \"$1\"; fi\n  shift\ndone\n"
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(curl), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "./script.sh")
	cmd.Dir = stage
	cmd.Env = append(os.Environ(), "FIXTURE="+fixture, "PATH="+bin+":"+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fetch script failed: %v\n%s", err, output)
	}
	facts, err := os.ReadFile(filepath.Join(stage, "output", "facts.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(facts), "Status: fetched") {
		t.Fatalf("facts = %q", facts)
	}
}

// playbook_script_test.go — script-backed playbook stages (issue #304,
// PA-007): the harness executes script.sh directly, zero inference; a
// non-zero exit or a missing declared output fails the run loudly; writes
// stay in the playbookWriteGuard's run-scoped zone; every script-stage
// action lands in the audit log (OBS-002); the shared validation seam
// (bash -n + tool scan) runs at edit time.

// writeScriptPlaybook writes a playbook whose stages carry script.sh (and
// optionally CONTEXT.md) per spec.
func writeScriptPlaybook(t *testing.T, home, name string, stages []struct {
	folder, script, context string
}) {
	t.Helper()
	root := filepath.Join(home, "playbooks", name)
	if err := os.MkdirAll(filepath.Join(root, "stages"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CONTEXT.md"), []byte("# "+name+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, st := range stages {
		dir := filepath.Join(root, "stages", st.folder)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if st.script != "" {
			if err := os.WriteFile(filepath.Join(dir, "script.sh"), []byte(st.script), 0700); err != nil {
				t.Fatal(err)
			}
		}
		if st.context != "" {
			if err := os.WriteFile(filepath.Join(dir, "CONTEXT.md"), []byte(st.context), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// scriptTestCore builds a Core with an in-memory audit DB and a write_file
// registry so runs exercise the guard and audit paths.
func scriptTestCore(t *testing.T, home string) (*Core, *callRecorder) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := initAudit(db); err != nil {
		t.Fatal(err)
	}
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil), DB: db}
	rec := &callRecorder{}
	oldLoop := runPlaybookStageLoop
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		rec.record("llm")
		return &LoopResult{Status: "complete", Reply: "done"}
	}
	t.Cleanup(func() { runPlaybookStageLoop = oldLoop })
	return core, rec
}

func loadScriptRunState(t *testing.T, pb *PlaybookWorkspace) PlaybookRun {
	t.Helper()
	entries, err := os.ReadDir(playbookRunsDir(pb))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 run dir, got %d", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(playbookRunsDir(pb), entries[0].Name(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var run PlaybookRun
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	return run
}

func auditEventTypes(t *testing.T, core *Core) map[string]int {
	t.Helper()
	rows, err := core.DB.Query("SELECT event_type, COUNT(*) FROM audit_events GROUP BY event_type")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types := map[string]int{}
	for rows.Next() {
		var typ string
		var n int
		if err := rows.Scan(&typ, &n); err != nil {
			t.Fatal(err)
		}
		types[typ] = n
	}
	return types
}

func TestScriptStageRunsWithZeroLLMCalls(t *testing.T) {
	home := t.TempDir()
	writeScriptPlaybook(t, home, "mech", []struct{ folder, script, context string }{
		{"01-fetch", "#!/bin/bash\nset -euo pipefail\nmkdir -p output\nprintf 'digest-v1' > output/result.md\n",
			"# fetch\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Result | `output/result.md` | Markdown |\n"},
	})
	core, rec := scriptTestCore(t, home)

	result, err := RunPlaybook(context.Background(), core, "mech", "run it", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "complete" || result.StagesRun != 1 {
		t.Fatalf("result = %+v", result)
	}
	if rec.count() != 0 {
		t.Fatalf("script stage made %d LLM calls, want 0 (zero inference)", rec.count())
	}
	pb, err := loadPlaybookWorkspace(home, "mech")
	if err != nil {
		t.Fatal(err)
	}
	run := loadScriptRunState(t, pb)
	st := run.Stages[0]
	if st.Status != "complete" || st.Script != "script.sh" || st.ExitCode != 0 || st.ScriptOutput == "" {
		t.Fatalf("stage record = %+v", st)
	}
	if len(st.Outputs) != 1 {
		t.Fatalf("outputs = %v", st.Outputs)
	}
	// The write landed in the run-scoped stage dir — the guard's writable
	// zone — and is the recorded output.
	out, err := os.ReadFile(st.Outputs[0])
	if err != nil || string(out) != "digest-v1" {
		t.Fatalf("output read: %q, err=%v", out, err)
	}
	if !strings.Contains(filepath.ToSlash(st.Outputs[0]), "/runs/"+run.ID+"/stages/01-fetch/output/") {
		t.Fatalf("output outside run zone: %s", st.Outputs[0])
	}
	// playbookWriteGuard: the same tags the dispatch sets must allow the run
	// zone and refuse the playbook definition tree.
	tags := context.WithValue(context.Background(), traceTagKey{}, map[string]string{
		"playbook": "mech", "stage": "01-fetch", "run": run.ID,
	})
	if guard := playbookWriteGuard(home, st.Outputs[0], tags); guard != "" {
		t.Fatalf("run-zone write rejected by guard: %s", guard)
	}
	if guard := playbookWriteGuard(home, filepath.Join(home, "playbooks", "mech", "CONTEXT.md"), tags); guard == "" {
		t.Fatal("playbook definition write not refused by guard")
	}
	// OBS-002: the execution is on the audit record.
	if types := auditEventTypes(t, core); types["script_stage"] != 1 {
		t.Fatalf("audit events = %v, want 1 script_stage", types)
	}
}

func TestScriptStageNonZeroExitFailsRunLoudly(t *testing.T) {
	home := t.TempDir()
	writeScriptPlaybook(t, home, "boom", []struct{ folder, script, context string }{
		{"01-fail", "#!/bin/bash\necho boom\nls /definitely/not/here\nexit 3\n", ""},
	})
	core, _ := scriptTestCore(t, home)

	result, err := RunPlaybook(context.Background(), core, "boom", "run it", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" || !strings.Contains(result.Reply, "stopped at stage 01-fail") || !strings.Contains(result.Reply, "exit 3") {
		t.Fatalf("result = %+v", result)
	}
	pb, err := loadPlaybookWorkspace(home, "boom")
	if err != nil {
		t.Fatal(err)
	}
	run := loadScriptRunState(t, pb)
	if run.Status != "failed" {
		t.Fatalf("run status = %q", run.Status)
	}
	st := run.Stages[0]
	if st.Status != "failed" || st.ExitCode != 3 || !strings.Contains(st.Error, "exit 3") || st.ScriptOutput == "" {
		t.Fatalf("stage record = %+v", st)
	}
	// The script's output is captured on disk — the failure is never silent.
	out, err := os.ReadFile(filepath.Join(playbookRunsDir(pb), run.ID, "stages", "01-fail", "script-output.txt"))
	if err != nil || !strings.Contains(string(out), "boom") {
		t.Fatalf("script-output.txt = %q, err=%v", out, err)
	}
	if types := auditEventTypes(t, core); types["script_stage_failed"] != 1 {
		t.Fatalf("audit events = %v, want 1 script_stage_failed", types)
	}
}

func TestScriptStageMissingDeclaredOutputFailsRun(t *testing.T) {
	home := t.TempDir()
	writeScriptPlaybook(t, home, "slacker", []struct{ folder, script, context string }{
		{"01-work", "#!/bin/bash\necho 'did nothing'\n", "# work\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Result | `output/result.md` | Markdown |\n"},
	})
	core, _ := scriptTestCore(t, home)

	result, err := RunPlaybook(context.Background(), core, "slacker", "run it", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" || !strings.Contains(result.Reply, "missing declared output") {
		t.Fatalf("result = %+v", result)
	}
	pb, err := loadPlaybookWorkspace(home, "slacker")
	if err != nil {
		t.Fatal(err)
	}
	run := loadScriptRunState(t, pb)
	if run.Status != "failed" || run.Stages[0].Status != "failed" {
		t.Fatalf("run = %+v", run)
	}
}

func TestScriptStageValidationSeam(t *testing.T) {
	home := t.TempDir()
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry}

	dir := filepath.Join(home, "playbooks", "check", "stages", "01-x")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "script.sh")
	good := "#!/bin/bash\nset -euo pipefail\n# mino exec write_file is a comment, not an invocation\nmkdir -p output\n"
	write := func(content string) {
		if err := os.WriteFile(script, []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Good script passes the shared seam.
	write(good)
	if err := validateScriptFile(core, script, "01-x"); err != nil {
		t.Fatalf("good script rejected: %v", err)
	}
	// Bad bash is rejected.
	write("#!/bin/bash\nif then\n")
	if err := validateScriptFile(core, script, "01-x"); err == nil || !strings.Contains(err.Error(), "bash -n") {
		t.Fatalf("bad bash accepted: %v", err)
	}
	// Undeclared tool references are rejected.
	write("#!/bin/bash\nmino exec read_file output/x\n")
	if err := validateScriptFile(core, script, "01-x"); err == nil || !strings.Contains(err.Error(), "unknown tool(s)") {
		t.Fatalf("unknown tool accepted: %v", err)
	}
	// Edit time: manage_playbook's validator rejects a playbook whose stage
	// script is broken, and accepts it once fixed.
	writeScriptPlaybook(t, home, "editcheck", []struct{ folder, script, context string }{
		{"01-x", "#!/bin/bash\nif then\n", ""},
	})
	if err := validateManagedPlaybook(core, "editcheck"); err == nil || !strings.Contains(err.Error(), "bash -n") {
		t.Fatalf("edit-time validation accepted bad script: %v", err)
	}
	writeScriptPlaybook(t, home, "editcheck", []struct{ folder, script, context string }{
		{"01-x", good, ""},
	})
	if err := validateManagedPlaybook(core, "editcheck"); err != nil {
		t.Fatalf("edit-time validation rejected good script: %v", err)
	}
}

func TestScriptStagePreRunValidationBlocksInvalidScript(t *testing.T) {
	// The pre-run path shares the seam: an invalid stage script fails the run
	// before anything executes — never silently skipped.
	home := t.TempDir()
	writeScriptPlaybook(t, home, "badpre", []struct{ folder, script, context string }{
		{"01-x", "#!/bin/bash\nif then\n", ""},
	})
	core, rec := scriptTestCore(t, home)
	if _, err := RunPlaybook(context.Background(), core, "badpre", "run it", "test", nil); err == nil || !strings.Contains(err.Error(), "bash -n") {
		t.Fatalf("pre-run validation error = %v", err)
	}
	if rec.count() != 0 {
		t.Fatalf("invalid script ran: %d LLM calls", rec.count())
	}
	if types := auditEventTypes(t, core); len(types) != 0 {
		t.Fatalf("unexpected audit events for blocked run: %v", types)
	}
}

func TestScriptStageHybridLLMStageStillRuns(t *testing.T) {
	// Hybrid playbook: script stage first, LLM synthesis stage second — the
	// LLM stage keeps the existing JSON tool-calling path, unchanged.
	home := t.TempDir()
	writeScriptPlaybook(t, home, "hybrid", []struct{ folder, script, context string }{
		{"01-mech", "#!/bin/bash\nmkdir -p output\nprintf 'digest' > output/result.md\n",
			"# mech\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Result | `output/result.md` | Markdown |\n"},
		{"02-synth", "",
			"# synth\n\n## Tools\n\n- write_file\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Post | `output/post.md` | Markdown |\n"},
	})
	core, rec := scriptTestCore(t, home)
	oldLoop := runPlaybookStageLoop
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		rec.record("llm")
		// Write the declared output the way the LLM path would (write_file
		// inside the stage) so the synthesis stage verifies complete.
		entries, err := os.ReadDir(filepath.Join(home, "playbooks", "hybrid", "runs"))
		if err != nil || len(entries) != 1 {
			t.Fatalf("run dirs: %v, err=%v", entries, err)
		}
		outPath := filepath.Join(home, "playbooks", "hybrid", "runs", entries[0].Name(), "stages", "02-synth", "output", "post.md")
		if err := os.MkdirAll(filepath.Dir(outPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(outPath, []byte("synthesized"), 0600); err != nil {
			t.Fatal(err)
		}
		return &LoopResult{Status: "complete", Reply: "synthesized", ToolCalls: []ToolCall{{Name: "write_file", Args: map[string]any{"path": outPath}}}}
	}
	defer func() { runPlaybookStageLoop = oldLoop }()

	result, err := RunPlaybook(context.Background(), core, "hybrid", "run it", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "complete" || result.StagesRun != 2 {
		t.Fatalf("result = %+v", result)
	}
	if rec.count() != 1 {
		t.Fatalf("LLM calls = %d, want exactly 1 (only the synthesis stage)", rec.count())
	}
	pb, err := loadPlaybookWorkspace(home, "hybrid")
	if err != nil {
		t.Fatal(err)
	}
	run := loadScriptRunState(t, pb)
	if run.Stages[0].Script != "script.sh" || run.Stages[0].ExitCode != 0 {
		t.Fatalf("script stage record = %+v", run.Stages[0])
	}
	if run.Stages[1].Script != "" {
		t.Fatalf("LLM stage got script dispatch: %+v", run.Stages[1])
	}
}
