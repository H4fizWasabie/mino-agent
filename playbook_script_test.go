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

// SCR-001 script runner mechanics: run recording, exit codes, session
// attribution, timeout, and the boot re-check. The dispatch/validation
// seams themselves live in seams_test.go.

func scriptCore(t *testing.T, home string) *Core {
	t.Helper()
	return &Core{Settings: &Settings{Home: home}, Tools: NewRegistry()}
}

func loadRunState(t *testing.T, pbDir, runID string) PlaybookRun {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(pbDir, "runs", runID, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var run PlaybookRun
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	return run
}

// SCR-001: the LLM path must refuse script-backed playbooks instead of
// "completing" a zero-stage run with no deliverables.
func TestRunPlaybookRefusesScriptBackedPlaybook(t *testing.T) {
	home := t.TempDir()
	writeScriptPlaybook(t, home, "daily", "#!/bin/bash\necho ok\n")
	core := &Core{Settings: &Settings{Home: home}, Tools: NewRegistry()}
	_, err := RunPlaybook(context.Background(), core, "daily", "run me", "cli", nil)
	if err == nil || !strings.Contains(err.Error(), "script-backed") {
		t.Fatalf("err = %v, want script-backed refusal", err)
	}
}

func TestRunScriptPlaybookRecordsRunAndOutput(t *testing.T) {
	home := t.TempDir()
	writeScriptPlaybook(t, home, "daily", "#!/bin/bash\necho 'hello from script'\ntouch out.txt\n")
	core := scriptCore(t, home)

	res, err := runScriptPlaybook(context.Background(), core, "daily", "Scheduled run", "scheduled-daily")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "complete" || res.StagesRun != 1 {
		t.Fatalf("result = %+v", res)
	}

	// The fresh run lands in runs/<id>/ with state + captured output.
	dir := filepath.Join(home, "playbooks", "daily", "runs")
	ids, err := os.ReadDir(dir)
	if err != nil || len(ids) != 1 {
		t.Fatalf("runs dir = %v (err %v), want exactly one run", ids, err)
	}
	runID := ids[0].Name()
	run := loadRunState(t, filepath.Join(home, "playbooks", "daily"), runID)
	if run.Status != "complete" || run.ExitCode != 0 || run.Script != "script.sh" || run.SessionID != "scheduled-daily" {
		t.Fatalf("state = %+v", run)
	}
	if len(run.Stages) != 1 || run.Stages[0].Status != "complete" || run.Stages[0].Name != "script.sh" {
		t.Fatalf("stages = %+v", run.Stages)
	}
	out, err := os.ReadFile(filepath.Join(dir, runID, "script-output.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "hello from script") {
		t.Fatalf("script output = %q", out)
	}
}

func TestRunScriptPlaybookNonZeroExitFailsLoudly(t *testing.T) {
	home := t.TempDir()
	writeScriptPlaybook(t, home, "daily", "#!/bin/bash\necho 'boom' >&2\nexit 3\n")
	core := scriptCore(t, home)

	res, err := runScriptPlaybook(context.Background(), core, "daily", "Scheduled run", "scheduled-daily")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "failed" || !strings.Contains(res.Reply, "exited 3") {
		t.Fatalf("result = %+v, want failed with exit 3", res)
	}
	ids, _ := os.ReadDir(filepath.Join(home, "playbooks", "daily", "runs"))
	if len(ids) != 1 {
		t.Fatalf("runs dir entries = %d", len(ids))
	}
	run := loadRunState(t, filepath.Join(home, "playbooks", "daily"), ids[0].Name())
	if run.Status != "failed" || run.ExitCode != 3 || !strings.Contains(run.Stages[0].Error, "exited 3") {
		t.Fatalf("state = %+v", run)
	}
}

func TestRunScriptCommandCarriesRunSession(t *testing.T) {
	// The script must see MINO_EXEC_SESSION so every `mino exec` inside it
	// lands in tool_calls + audit under the run, not under cli-exec.
	script := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(script, []byte("#!/bin/bash\necho \"$MINO_EXEC_SESSION\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, code, err := runScriptCommand(context.Background(), script, filepath.Dir(script), "scheduled-daily")
	if err != nil || code != 0 {
		t.Fatalf("run: code %d err %v", code, err)
	}
	if got := strings.TrimSpace(string(out)); got != "scheduled-daily" {
		t.Fatalf("session env = %q, want scheduled-daily", got)
	}
}

func TestRunScriptCommandTimeout(t *testing.T) {
	old := scriptRunTimeout
	scriptRunTimeout = 200 * time.Millisecond
	defer func() { scriptRunTimeout = old }()

	script := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(script, []byte("#!/bin/bash\nsleep 5\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, code, err := runScriptCommand(context.Background(), script, filepath.Dir(script), "scheduled-daily")
	if code != 1 || err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("code %d err %v, want timed out failure", code, err)
	}
}

func TestRecheckScheduledScriptsAtLoudSkip(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	core := &Core{Settings: &Settings{Home: home}, DB: db, Tools: NewRegistry()}

	// Broken script + valid script + LLM-only playbook, all scheduled.
	writeScriptPlaybook(t, home, "broken", "#!/bin/bash\nif then\n")
	writeScriptPlaybook(t, home, "good", "#!/bin/bash\necho ok\n")
	writeScriptPlaybook(t, home, "plain", "")
	scheds := []PlaybookSchedule{
		{Name: "broken", Time: "09:00", Timezone: "Asia/Kuala_Lumpur"},
		{Name: "good", Time: "09:00", Timezone: "Asia/Kuala_Lumpur"},
		{Name: "plain", Time: "09:00", Timezone: "Asia/Kuala_Lumpur"},
	}
	if err := saveSchedules(home, scheds); err != nil {
		t.Fatal(err)
	}

	recheckScheduledScriptsAt(core)

	loaded, err := loadSchedules(home)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, s := range loaded {
		byName[s.Name] = s.LastError
	}
	if !strings.Contains(byName["broken"], "skipped: script validation failed") {
		t.Fatalf("broken LastError = %q, want loud skip record", byName["broken"])
	}
	if byName["good"] != "" || byName["plain"] != "" {
		t.Fatalf("good/plain LastError must stay empty: %q / %q", byName["good"], byName["plain"])
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_events WHERE event_type='script_invalid'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("script_invalid audit rows = %d, want 1", n)
	}
}

func TestSchedulePlaybookRefusesInvalidScript(t *testing.T) {
	home := t.TempDir()
	writeScriptPlaybook(t, home, "broken", "#!/bin/bash\nmino exec fake_tool {}\n")
	writeScriptPlaybook(t, home, "plain", "")
	// The LLM-only playbook needs a real stage to be schedulable.
	stageDir := filepath.Join(home, "playbooks", "plain", "stages", "01-gather")
	if err := os.MkdirAll(stageDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "CONTEXT.md"), []byte("# Gather\n\n## Process\n\n1. Do it.\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Result | `output/result.md` | Markdown |\n"), 0600); err != nil {
		t.Fatal(err)
	}
	core := &Core{Settings: &Settings{Home: home, Timezone: "Asia/Kuala_Lumpur"}, Tools: NewRegistry()}
	tool := makeSchedulePlaybookTool(core)

	got := tool.ContextFn(context.Background(), map[string]any{"name": "broken", "time": "09:00"})
	if !strings.Contains(got, "not scheduling broken") || !strings.Contains(got, "unknown tool") {
		t.Fatalf("broken schedule reply = %q", got)
	}
	if _, err := loadSchedules(home); err == nil {
		if scheds, _ := loadSchedules(home); len(scheds) != 0 {
			t.Fatalf("schedules written for refused playbook: %+v", scheds)
		}
	}

	got2 := tool.ContextFn(context.Background(), map[string]any{"name": "plain", "time": "09:00"})
	if !strings.Contains(got2, "Scheduled plain") {
		t.Fatalf("valid script schedule reply = %q", got2)
	}
}

func TestManagePlaybookScriptValidateAction(t *testing.T) {
	home := t.TempDir()
	writeScriptPlaybook(t, home, "bad", "#!/bin/bash\nmino exec nope {}\n")
	writeScriptPlaybook(t, home, "good", "#!/bin/bash\n")
	reg := NewRegistry()
	reg.Register(&Tool{Name: "post", Fn: func(map[string]any) string { return "" }})
	core := &Core{Settings: &Settings{Home: home}, Tools: reg}
	tool := makeManagePlaybookTool(core)

	bad := tool.Fn(map[string]any{"action": "script", "script_action": "validate", "name": "bad"})
	if !strings.Contains(bad, "Error:") || !strings.Contains(bad, "nope") {
		t.Fatalf("bad validation reply = %q", bad)
	}
	good := tool.Fn(map[string]any{"action": "script", "script_action": "validate", "name": "good"})
	if !strings.Contains(good, "Script for good is valid") {
		t.Fatalf("good validation reply = %q", good)
	}
	badSub := tool.Fn(map[string]any{"action": "script", "name": "good"})
	if !strings.Contains(badSub, "Error: script action must be validate") {
		t.Fatalf("missing script_action reply = %q", badSub)
	}
}