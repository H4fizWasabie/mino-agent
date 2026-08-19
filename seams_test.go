package main

// seams_test.go — the Prompt-Assembly Test Surface (REL-04, #134).
//
// The named seams below render model-visible text or parse model-facing
// contracts. Each must carry a table-driven test BEFORE any feature touching
// it ships. The presence check at the bottom enforces that mechanically: it
// fails `go test ./...` when a listed seam has no Test function naming it —
// killing the silent zero (2026-08-10's two prompt-assembly bugs had zero
// tests on the path while 366 tests sat elsewhere) without coverage noise.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- seam: parseStageInputs ---

func TestParseStageInputs(t *testing.T) {
	cases := []struct {
		name    string
		section string
		want    []StageInput
	}{
		{"absent", "", nil},
		{"header only", "| Source | File/Location | Section/Scope | Why |\n| --- | --- | --- | --- |", nil},
		{"single row", "| Runtime | Authoritative local date | Full | Date the post |",
			[]StageInput{{Source: "Runtime", Path: "Authoritative local date", Scope: "Full"}}},
		{"glob row", "| Logs | `" + "/x/*.md" + "` | Most recent 14 | Exclusions |",
			[]StageInput{{Source: "Logs", Path: "/x/*.md", Scope: "Most recent 14"}}},
		{"multiple rows", "| A | `x` | Full | a |\n| B | `y` | Full | b |",
			[]StageInput{{Source: "A", Path: "x", Scope: "Full"}, {Source: "B", Path: "y", Scope: "Full"}}},
		{"backticks trimmed from ends only", "| Logs | `a` and `b` | Full | both |",
			[]StageInput{{Source: "Logs", Path: "a` and `b", Scope: "Full"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseStageInputs(tc.section)
			if len(got) != len(tc.want) {
				t.Fatalf("parseStageInputs(%q) = %+v, want %+v", tc.section, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("parseStageInputs(%q)[%d] = %+v, want %+v", tc.section, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// --- seam: parseStageOutputs ---

func TestParseStageOutputs(t *testing.T) {
	cases := []struct {
		name    string
		section string
		want    []StageOutput
	}{
		{"absent", "", nil},
		{"declared output", "| Battle log | `output/threads-battle-log.md` | Markdown |",
			[]StageOutput{{Name: "Battle log", Path: "output/threads-battle-log.md"}}},
		{"quarantined absolute path", "| Digest | `/home/mino/.mino/data/x.md` | Markdown |",
			[]StageOutput{{Name: "Digest", Path: "/home/mino/.mino/data/x.md"}}},
		{"path traversal rejected", "| Bad | `../escape.md` | Markdown |", nil},
		{"non-output prefix rejected", "| Bad | `notes/x.md` | Markdown |", nil},
		{"bare output dir rejected", "| Bad | `output` | Markdown |", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseStageOutputs(tc.section)
			if len(got) != len(tc.want) {
				t.Fatalf("parseStageOutputs(%q) = %+v, want %+v", tc.section, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("parseStageOutputs(%q)[%d] = %+v, want %+v", tc.section, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// --- seam: workspaceInputPath ---

func TestWorkspaceInputPath(t *testing.T) {
	pb := &PlaybookWorkspace{Dir: "/home/mino/.mino/playbooks/tribal"}
	run := &PlaybookRun{ID: "20260810T000000Z"}
	stage := WorkspaceStage{Number: 1, Name: "provoke", Dir: "/home/mino/.mino/playbooks/tribal/stages/01-provoke"}

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"relative stays under playbook dir", "output/x.md", "/home/mino/.mino/playbooks/tribal/output/x.md"},
		{"references resolves under stage dir", "references/common.md", "/home/mino/.mino/playbooks/tribal/stages/01-provoke/references/common.md"},
		{"absolute path resolves as-is (issue #86 double-join regression)", "/home/mino/.mino/playbooks/shared/rules.md", "/home/mino/.mino/playbooks/shared/rules.md"},
		{"previous-stage output resolves under run", "../01-post/output/log.md", "/home/mino/.mino/playbooks/tribal/runs/20260810T000000Z/stages/01-post/output/log.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := workspaceInputPath(pb, run, stage, tc.raw); got != tc.want {
				t.Fatalf("workspaceInputPath(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// --- seam: renderWorkspaceInput ---

func TestRenderWorkspaceInput(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(logs, "a.md")
	new := filepath.Join(logs, "b.md")
	if err := os.WriteFile(old, []byte("OLD"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(new, []byte("NEW"), 0600); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	_ = os.Chtimes(old, base, base.Add(-time.Hour))
	_ = os.Chtimes(new, base, base)

	loc := time.FixedZone("MYT", 8*3600)
	now := time.Date(2026, 8, 10, 8, 30, 0, 0, loc)
	pb := &PlaybookWorkspace{Dir: dir}
	run := &PlaybookRun{ID: "run"}
	stage := WorkspaceStage{Number: 1, Name: "s", Dir: filepath.Join(dir, "stages", "01-s")}

	cases := []struct {
		name  string
		input StageInput
		want  string
	}{
		{"runtime renders the clock", StageInput{Source: "Runtime", Path: "Authoritative local date"}, "2026-08-10 (Monday)"},
		{"glob expands newest-first", StageInput{Source: "Logs", Path: filepath.Join(logs, "*.md")}, "--- " + new + " ---\nNEW\n\n--- " + old + " ---\nOLD"},
		{"empty glob is an empty list", StageInput{Source: "Logs", Path: filepath.Join(dir, "empty", "*.md")}, "No files matched."},
		{"literal missing file stays unavailable", StageInput{Source: "Logs", Path: filepath.Join(dir, "nope.md")}, "Unavailable:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderWorkspaceInput(pb, run, stage, tc.input, now, loc)
			if !strings.HasPrefix(got, tc.want) && !strings.Contains(got, tc.want) {
				t.Fatalf("renderWorkspaceInput(%+v) = %q, want substring %q", tc.input, got, tc.want)
			}
		})
	}
}

// --- seam: renderWorkspaceInputFiles ---

func TestRenderWorkspaceInputFiles(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "a.md")
	new := filepath.Join(dir, "b.md")
	if err := os.WriteFile(old, []byte("OLD"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(new, []byte("NEW"), 0600); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	_ = os.Chtimes(old, base, base.Add(-time.Hour))
	_ = os.Chtimes(new, base, base)

	got := renderWorkspaceInputFiles([]string{old, new})
	if i, j := strings.Index(got, "NEW"), strings.Index(got, "OLD"); i < 0 || j < 0 || i > j {
		t.Fatalf("renderWorkspaceInputFiles not newest-first: %q", got)
	}
	if !strings.Contains(got, "--- "+new+" ---") || !strings.Contains(got, "--- "+old+" ---") {
		t.Fatalf("renderWorkspaceInputFiles missing path headers: %q", got)
	}
}

// --- seam: truncateWorkspaceInput ---

func TestTruncateWorkspaceInput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"short stays", "hello", "hello"},
		{"whitespace trimmed", "  hello  ", "hello"},
		{"over 4000 chars truncated with marker", strings.Repeat("x", 5000), strings.Repeat("x", 4000) + "\n[truncated]"},
		{"exactly 4000 not truncated", strings.Repeat("x", 4000), strings.Repeat("x", 4000)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateWorkspaceInput(tc.in); got != tc.want {
				t.Fatalf("truncateWorkspaceInput(len=%d) = len %d, want len %d", len(tc.in), len(got), len(tc.want))
			}
		})
	}
}

// --- seam: buildWorkspaceStagePrompt ---

func TestBuildWorkspaceStagePromptIncludesContractAndRules(t *testing.T) {
	pb := &PlaybookWorkspace{Name: "tribal", Dir: "/x"}
	run := &PlaybookRun{ID: "20260810T000000Z", Request: "Scheduled run"}
	stage := WorkspaceStage{
		Number:  1,
		Name:    "provoke",
		Context: "# Arena Post\n\n## Process\n\n1. Post it.\n\n## Tools\n\n- threads_post\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Battle log | `output/battle.md` | Markdown |\n",
		Tools:    []string{"threads_post", "write_file"},
		Outputs:  []StageOutput{{Name: "Battle log", Path: "output/battle.md"}},
	}
	loc := time.FixedZone("MYT", 8*3600)
	now := time.Date(2026, 8, 10, 8, 30, 0, 0, loc)
	msg := buildWorkspaceStagePrompt(pb, run, stage, now, loc)

	for _, want := range []string{
		`You are executing playbook "tribal"`,
		"run 20260810T000000Z",
		"stage 01-provoke",
		"## Request",
		"Scheduled run",
		"## Stage Contract",
		"## Run Inputs",
		"## Required Outputs",
		"- Battle log: `" + filepath.Join("/x/runs/20260810T000000Z/stages/01-provoke/output/battle.md"),
		"## Rules",
		"Use only these tools: threads_post, write_file.",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("buildWorkspaceStagePrompt missing %q in:\n%s", want, msg)
		}
	}
}

// --- seam: verifyWorkspaceStageOutputs (incl. ## Success) ---

func TestVerifyWorkspaceStageOutputsSuccessOutcome(t *testing.T) {
	pb := &PlaybookWorkspace{Dir: t.TempDir()}
	run := &PlaybookRun{ID: "run"}
	stage := WorkspaceStage{
		Number: 1,
		Name:   "post",
		Dir:    filepath.Join(pb.Dir, "stages", "01-post"),
		Success: []StageSuccess{{Outcome: "Post published", Tool: "threads_post"}},
	}
	// The declared output must exist for the output check to pass; the
	// Success check is what we exercise.
	out := playbookRunOutputPath(pb, run, stage, StageOutput{Name: "Log", Path: "output/log.md"})
	if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte("Status: Posted"), 0600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		calls []ToolCall
		want  string // empty = pass; otherwise expected outcomeFailure fragment
	}{
		{"successful publish with 15+ digit ID passes",
			[]ToolCall{{Name: "write_file", Args: map[string]any{"path": out}, Output: "ok"},
				{Name: "threads_post", Output: `{"result": "18120295735852818"}`}}, ""},
		{"no publish call fails",
			[]ToolCall{{Name: "write_file", Args: map[string]any{"path": out}, Output: "ok"}}, "publish or say why"},
		{"errored publish call fails",
			[]ToolCall{{Name: "write_file", Args: map[string]any{"path": out}, Output: "ok"},
				{Name: "threads_post", Output: "error: no valid token"}}, "publish or say why"},
		{"short ID does not count",
			[]ToolCall{{Name: "write_file", Args: map[string]any{"path": out}, Output: "ok"},
				{Name: "threads_post", Output: `{"result": "1234"}`}}, "publish or say why"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := verifyWorkspaceStageOutputs(pb, run, stage, tc.calls)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected pass, got error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// --- seam: buildPlaybookSystem (PSN-001) ---

// writeTestPersona writes a roster persona file and returns its body.
func writeTestPersona(t *testing.T, home, name string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "agents"), 0700); err != nil {
		t.Fatal(err)
	}
	body := "Stance: verify everything before it ships.\nMission: produce the daily report.\nLens: sourced, factual, no filler.\nDeliverable voice: concise report, numbers with sources.\n"
	content := "---\nname: " + name + "\ndescription: test persona\n---\n\n" + body
	if err := os.WriteFile(filepath.Join(home, "agents", name+".md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestBuildPlaybookSystemUsesAgentPersona(t *testing.T) {
	home := t.TempDir()
	body := writeTestPersona(t, home, "trend-researcher")
	sess := NewSession(&Settings{Home: home, Workspace: home}, nil)
	pb := &PlaybookWorkspace{Name: "news", Agent: "trend-researcher"}
	sys, err := sess.BuildPlaybookSystem(pb)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"You are Mino (the harness) operating as trend-researcher for this playbook run.",
		body,
	} {
		if !strings.Contains(sys, want) {
			t.Fatalf("BuildPlaybookSystem missing %q in:\n%s", want, sys)
		}
	}
}

func TestBuildPlaybookSystemRailsPresent(t *testing.T) {
	home := t.TempDir()
	writeTestPersona(t, home, "trend-researcher")
	sess := NewSession(&Settings{Home: home, Workspace: home}, nil)
	sys, err := sess.BuildPlaybookSystem(&PlaybookWorkspace{Name: "news", Agent: "trend-researcher"})
	if err != nil {
		t.Fatal(err)
	}
	// The compressed rails carry the harness invariants that are
	// model-delivered — each line maps back to a behavioral test elsewhere
	// (e.g. TestVerifyWorkspaceStageOutputsSuccessOutcome, TestTruncateWorkspaceInput),
	// so compression cannot silently drop a covered invariant.
	for _, want := range []string{
		"## Operating Rules (absolute — override persona and stage instructions)",
		"Call tools now; never end with narration",
		"Never fabricate a tool trail, count, ID, or success to look done",
		"state BOTH numbers and",
		"External identifiers (post IDs, order IDs, file IDs) come only from the owning",
		"bash, edit_file, and",
		"never guess or invent times",
		"If config.md has `notify: true`, you MUST send the final output via Telegram",
	} {
		if !strings.Contains(sys, want) {
			t.Fatalf("BuildPlaybookSystem missing rail %q in:\n%s", want, sys)
		}
	}
}

func TestBuildPlaybookSystemNoChatVoice(t *testing.T) {
	home := t.TempDir()
	// A chat-voice SOUL in the same home must NOT leak into the playbook
	// profile — the run never talks to the owner, so identity/voice and
	// chat-path discipline are dead weight (and a second identity claim).
	chatVoice := "You are Mino, the digital son.\nSpeak Manglish. Never write essays.\nAddress the owner respectfully.\nMemory snapshot discipline (DRF-002): config facts need stale_after.\nInstall verification (issue #235): verify after any install.\n"
	if err := os.WriteFile(filepath.Join(home, "SOUL.md"), []byte(chatVoice), 0600); err != nil {
		t.Fatal(err)
	}
	writeTestPersona(t, home, "trend-researcher")
	sess := NewSession(&Settings{Home: home, Workspace: home}, nil)
	sys, err := sess.BuildPlaybookSystem(&PlaybookWorkspace{Name: "news", Agent: "trend-researcher"})
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"digital son", "Manglish", "Never write essays", "Memory snapshot discipline", "Install verification"} {
		if strings.Contains(sys, banned) {
			t.Fatalf("BuildPlaybookSystem leaks chat voice %q in:\n%s", banned, sys)
		}
	}
	// The chat profile is unchanged: the same SOUL rides chat contexts.
	chat, _ := sess.BuildContext("hello", "telegram")
	if !strings.Contains(chat, "digital son") || !strings.Contains(chat, "Manglish") {
		t.Fatalf("chat profile lost its SOUL voice:\n%s", chat)
	}
}

func TestBuildPlaybookSystemFailsLoudlyOnMissingPersona(t *testing.T) {
	// PSN-001 review finding: validation passes pre-run, but a roster file
	// deleted in the gap between validation and prompt build must fail loudly
	// — never silently degrade to a hatless rails-only run.
	home := t.TempDir()
	writeTestPersona(t, home, "trend-researcher")
	sess := NewSession(&Settings{Home: home, Workspace: home}, nil)
	pb := &PlaybookWorkspace{Name: "news", Agent: "trend-researcher"}
	// Validation passes while the file exists...
	if err := validatePlaybookPersona(home, pb); err != nil {
		t.Fatalf("pre-run validation should pass while the file exists: %v", err)
	}
	// ...then the roster file disappears before the prompt is built.
	if err := os.Remove(filepath.Join(home, "agents", "trend-researcher.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.BuildPlaybookSystem(pb); err == nil {
		t.Fatal("BuildPlaybookSystem must fail loudly when the bound persona is gone, not silently degrade")
	}
}

// --- seam: validatePlaybookScript (SCR-001, #272) ---

// writeScriptPlaybook lays down a minimal playbook dir with the given
// script.sh content and returns the home dir.
func writeScriptPlaybook(t *testing.T, home, name, script string) {
	t.Helper()
	dir := filepath.Join(home, "playbooks", name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CONTEXT.md"), []byte("# "+name+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if script != "" {
		if err := os.WriteFile(filepath.Join(dir, "script.sh"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidatePlaybookScript(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&Tool{Name: "post", Fn: func(map[string]any) string { return "" }})
	reg.Register(&Tool{Name: "log", Fn: func(map[string]any) string { return "" }})

	cases := []struct {
		name   string
		script string
		want   string // substring of the expected error; empty = valid
	}{
		{"missing script", "", "no script.sh present"},
		{"bash syntax error", "if then\n", "bash -n failed"},
		{"unknown tool", "mino exec post ...\nmino exec fake_tool {...}\n", "unknown tool(s) in script: fake_tool"},
		{"comment mentioning a tool is not an invocation", "# mino exec fake_tool would not run\nmino exec post {...}\n", ""},
		{"valid script", "#!/bin/bash\nmino exec post '{\"text\":\"hi\"}' || exit 1\nmino exec log {...}\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeScriptPlaybook(t, home, "daily", tc.script)
			core := &Core{Settings: &Settings{Home: home}, Tools: reg}
			err := validatePlaybookScript(core, "daily")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected valid, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// --- seam: runScheduledPlaybook (SCR-001, #272) ---

// TestRunScheduledPlaybookScriptDispatch covers the dispatch seam: a
// script-having playbook runs its script (exit 0 → complete); an invalid
// script is never executed — the run fails with the validation reason and
// the marker file proves bash never ran.
func TestRunScheduledPlaybookScriptDispatch(t *testing.T) {
	// Valid script: writes a marker, exits 0.
	home := t.TempDir()
	writeScriptPlaybook(t, home, "daily", "#!/bin/bash\ntouch "+filepath.Join(home, "ran")+"\n")
	core := &Core{Settings: &Settings{Home: home}, Tools: NewRegistry()}
	res, err := runScheduledPlaybook(context.Background(), core, "daily", "Scheduled run", "scheduled-daily", nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != "complete" || res.StagesRun != 1 {
		t.Fatalf("result = %+v, want complete/1 stage", res)
	}
	if _, err := os.Stat(filepath.Join(home, "ran")); err != nil {
		t.Fatal("script never executed")
	}

	// Invalid script: refused, and the marker proves bash never ran it.
	home2 := t.TempDir()
	writeScriptPlaybook(t, home2, "daily", "#!/bin/bash\nmino exec fake_tool {}\ntouch "+filepath.Join(home2, "ran")+"\n")
	core2 := &Core{Settings: &Settings{Home: home2}, Tools: NewRegistry()}
	res2, err2 := runScheduledPlaybook(context.Background(), core2, "daily", "Scheduled run", "scheduled-daily", nil)
	if err2 != nil {
		t.Fatalf("run: %v", err2)
	}
	if res2.Status != "failed" || !strings.Contains(res2.Reply, "script validation failed") {
		t.Fatalf("result = %+v, want failed with validation reason", res2)
	}
	if _, err := os.Stat(filepath.Join(home2, "ran")); !os.IsNotExist(err) {
		t.Fatal("invalid script must never be executed")
	}
}

// TestRunScheduledPlaybookNoScriptFallsThroughToLLMPath locks the other
// branch: a playbook without script.sh keeps the LLM runner. The proof is
// RunPlaybook's own stage validation error — the script path would fail
// with "no script.sh present", so a stage-level error can only come from
// the LLM runner (no provider call needed to reach it).
func TestRunScheduledPlaybookNoScriptFallsThroughToLLMPath(t *testing.T) {
	home := t.TempDir()
	writeScriptPlaybook(t, home, "daily", "")
	// One stage declaring an unknown tool: loadWorkspaceStage accepts it,
	// RunPlaybook's validation refuses it — the refusal text names the stage.
	stageDir := filepath.Join(home, "playbooks", "daily", "stages", "01-gather")
	if err := os.MkdirAll(stageDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "CONTEXT.md"), []byte("# Gather\n\n## Process\n\n1. Do it.\n\n## Tools\n\n- zzz_tool\n"), 0600); err != nil {
		t.Fatal(err)
	}
	core := &Core{Settings: &Settings{Home: home}, Tools: NewRegistry()}
	_, err := runScheduledPlaybook(context.Background(), core, "daily", "Scheduled run", "scheduled-daily", nil)
	if err == nil || strings.Contains(err.Error(), "no script.sh") || !strings.Contains(err.Error(), "stage") {
		t.Fatalf("err = %v, want RunPlaybook's stage validation error (LLM path)", err)
	}
}

// --- presence check (REL-04, #134) ---

// promptAssemblySeams are the named functions that render model-visible text
// or parse model-facing contracts. Append new seams when they are born.
var promptAssemblySeams = []string{
	"parseStageInputs",
	"parseStageOutputs",
	"parseStageSuccess",
	"buildWorkspaceStagePrompt",
	"renderWorkspaceInput",
	"renderWorkspaceInputFiles",
	"truncateWorkspaceInput",
	"workspaceInputPath",
	"cleanPlaybookRequest",
	"verifyWorkspaceStageOutputs",
	"buildPlaybookSystem",
	"alertScheduleHealth",
	"contextBudgetBlock",
	"taskIntentOffer",
	"buildTaskifyStages",
	"splitOfferText",
	"gatePauseReply",
	"approvePendingTaskGate",
	"runScheduledPlaybook",
	"validatePlaybookScript",
}

func TestPromptAssemblySeamsCovered(t *testing.T) {
	// Mechanical tripwire: a listed seam with no Test function naming it
	// fails the suite. Presence-only by design — quality stays with review.
	dir := t.TempDir()
	src := "package main\n" + promptAssemblySeamsSource()
	if err := os.WriteFile(filepath.Join(dir, "seams.go"), []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	// The real check runs over the actual tree; we resolve the package files
	// via the test's working directory instead of synthesizing them.
	_ = dir
	missing := uncoveredSeams()
	if len(missing) > 0 {
		t.Fatalf("prompt-assembly seams without a Test function: %v", missing)
	}
}

// uncoveredSeams returns the listed seams that no Test function names.
func uncoveredSeams() []string {
	var missing []string
	for _, seam := range promptAssemblySeams {
		found := false
		for _, tf := range allTestFuncNames() {
			// Seams are unexported (lowercase first letter); test names
			// capitalize it (TestParseStageInputs) — match case-insensitively.
			if strings.Contains(strings.ToLower(tf), strings.ToLower(seam)) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, seam)
		}
	}
	return missing
}

// allTestFuncNames collects the names of every Test* function in the package
// by scanning the current directory's _test.go files (the package is flat).
func allTestFuncNames() []string {
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(e.Name())
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "func Test") {
				name := strings.TrimPrefix(line, "func Test")
				if i := strings.Index(name, "("); i > 0 {
					names = append(names, "Test"+name[:i])
				}
			}
		}
	}
	return names
}

// promptAssemblySeamsSource is unused scaffolding kept out of the real check;
// the source listing lives in the variable above.
func promptAssemblySeamsSource() string { return "var _ = 0\n" }
