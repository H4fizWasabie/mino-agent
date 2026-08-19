package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// playbook_script.go — script-backed playbook stages (SCR-001, #272).
//
// A playbook becomes deterministic when its directory carries script.sh:
// the scheduler (SCH-002 machinery) runs the script instead of paying
// per-stage LLM turns. The script is a committed, reviewed owner artifact —
// never generated at runtime. Every non-zero exit lands in runs/<id>/ and
// the journal, and the SCH-002 health path pages once (never-silent).

// scriptFileName is the committed artifact name inside a playbook dir.
const scriptFileName = "script.sh"

// scriptRunTimeout bounds one script run (scheduled pipeline work; the
// failed LLM run observed in design burned 12 minutes, so 30 is sane
// headroom). A var so tests can shorten it. ponytail: not a config knob
// yet — add one when the pilot phase actually needs it.
var scriptRunTimeout = 30 * time.Minute

// minoExecToolRe finds `mino exec <tool>` invocations for the tool-name
// scan in validatePlaybookScript. Case-insensitive: MCP tools register
// under uppercase names (MCP_composio_*), and the registry lookup stays
// exact-case — a lowercase "mcp_..." invocation correctly reports unknown.
var minoExecToolRe = regexp.MustCompile(`(?i)\bmino exec\s+([a-z0-9_-]+)`)

// scriptEnv is the minimal environment for script children. The mino
// process runs with EnvironmentFile=mino.env (systemd), so os.Environ()
// carries every provider key and the bot token — a committed script must
// never inherit those. Only the essentials ride along; secrets stay in the
// binary, resolved internally by `mino exec` (SCR-001).
func scriptEnv(sessionID string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TZ=" + os.Getenv("TZ"),
		"LANG=" + os.Getenv("LANG"),
		"MINO_EXEC_SESSION=" + sessionID,
	}
}

// hasPlaybookScript reports whether the playbook carries script.sh.
func hasPlaybookScript(home, name string) bool {
	info, err := os.Stat(filepath.Join(home, "playbooks", name, scriptFileName))
	return err == nil && !info.IsDir()
}

// validatePlaybookScript is the single validation seam (named, REL-04):
// the manage_playbook script validate action, the schedule_playbook gate,
// and the boot re-check all call it — one behavior everywhere, so the
// checks cannot drift. bash -n proves syntax; the tool-name scan proves
// every `mino exec <tool>` in the script resolves in the registry.
// Comment lines are skipped: a comment mentioning a tool is not an
// invocation.
func validatePlaybookScript(core *Core, name string) error {
	return validateScriptFile(core, filepath.Join(core.Settings.Home, "playbooks", name, scriptFileName), name)
}

// runScheduledPlaybook is the scheduler's dispatch seam (named, REL-04):
// script-backed playbooks run their script; everything else keeps the LLM
// path. fireSchedule passes this runner to both the tick and the boot
// catch-up, so a scheduled script can never be skipped silently.
func runScheduledPlaybook(ctx context.Context, core *Core, name, request, sessionID string, obs Observer) (*PlaybookResult, error) {
	if !hasPlaybookScript(core.Settings.Home, name) {
		return RunPlaybook(ctx, core, name, request, sessionID, obs)
	}
	if err := validatePlaybookScript(core, name); err != nil {
		// Never launch an invalid script. Report it as a failed run so the
		// SCH-002 health path pages once and the failure is on the record —
		// loud, and exactly one notice per day (the schedule's own rule).
		return &PlaybookResult{Name: name, Status: "failed", Reply: "script validation failed: " + err.Error()}, nil
	}
	return runScriptPlaybook(ctx, core, name, request, sessionID)
}

// runScriptPlaybook executes the committed script once and records the run
// in runs/<id>/ (state.json + script-output.txt) like any playbook run.
// Exit 0 → complete; anything else → failed with the exit code on the
// record. MINO_EXEC_SESSION exports the run session so every `mino exec`
// inside the script lands in tool_calls + audit under this run.
func runScriptPlaybook(ctx context.Context, core *Core, name, request, sessionID string) (*PlaybookResult, error) {
	home := core.Settings.Home
	pbDir := filepath.Join(home, "playbooks", name)
	script := filepath.Join(pbDir, scriptFileName)
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("playbook %s: %s missing: %v", name, scriptFileName, err)
	}
	now := time.Now().UTC()
	runID := now.Format("20060102T150405.000000000Z")
	run := &PlaybookRun{
		ID:        runID,
		Playbook:  name,
		Request:   request,
		SessionID: sessionID,
		Status:    "running",
		Script:    scriptFileName,
		CreatedAt: now,
		UpdatedAt: now,
		Stages:    []PlaybookRunStage{{Number: 1, Name: scriptFileName, Status: "running", StartedAt: now}},
	}
	dir := filepath.Join(pbDir, "runs", runID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := writeRunStateFile(filepath.Join(dir, "state.json"), *run); err != nil {
		return nil, err
	}

	out, code, runErr := runScriptCommand(ctx, script, pbDir, sessionID)
	outPath := filepath.Join(dir, "script-output.txt")
	if werr := os.WriteFile(outPath, out, 0600); werr != nil {
		return nil, werr
	}
	run.ScriptOutput = filepath.Join("runs", runID, "script-output.txt")

	result := &PlaybookResult{Name: name, StagesRun: 1, Status: "complete"}
	run.Status, run.Stages[0].Status = "complete", "complete"
	if runErr != nil {
		// Spawn failure / timeout — never silent.
		run.Status, result.Status = "failed", "failed"
		run.Stages[0].Status, run.Stages[0].Error = "failed", runErr.Error()
		result.Reply = runErr.Error()
	} else if code != 0 {
		reason := fmt.Sprintf("%s exited %d", scriptFileName, code)
		run.Status, result.Status = "failed", "failed"
		run.Stages[0].Status, run.Stages[0].Error = "failed", reason
		result.Reply = reason
	}
	run.ExitCode = code
	run.Stages[0].EndedAt = time.Now().UTC()
	run.UpdatedAt = run.Stages[0].EndedAt
	if err := writeRunStateFile(filepath.Join(dir, "state.json"), *run); err != nil {
		return nil, err
	}
	logTrace(home, "script_run", map[string]any{"playbook": name, "run_id": runID, "status": run.Status, "exit_code": code})
	core.auditLog(sessionID, "script_run", fmt.Sprintf("%s: script exit %d, run %s", name, code, runID), 0)
	return result, nil
}

// runScriptCommand spawns the script with the run session attributed.
func runScriptCommand(ctx context.Context, script, dir, sessionID string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, scriptRunTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, script)
	cmd.Dir = dir
	cmd.Env = scriptEnv(sessionID)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return out, 1, fmt.Errorf("script timed out after %s", scriptRunTimeout)
	}
	if err == nil {
		return out, 0, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return out, ee.ExitCode(), nil
	}
	return out, 1, err
}

// recheckScheduledScriptsAt runs once at boot (loud skip): a scheduled
// script that fails validation is never run — the schedule entry records
// why, the trace and audit log carry it, and runScheduledPlaybook's gate
// keeps refusing it at fire time until the script is fixed.
func recheckScheduledScriptsAt(core *Core) {
	scheds, err := loadSchedules(core.Settings.Home)
	if err != nil || len(scheds) == 0 {
		return
	}
	for _, s := range scheds {
		if !hasPlaybookScript(core.Settings.Home, s.Name) {
			continue
		}
		if err := validatePlaybookScript(core, s.Name); err == nil {
			continue
		} else {
			msg := "skipped: script validation failed: " + err.Error()
			slog.Error("scheduled script invalid; skipping", "playbook", s.Name, "reason", err)
			logTrace(core.Settings.Home, "script_invalid", map[string]any{"playbook": s.Name, "reason": err.Error()})
			core.auditLog("scheduled-"+s.Name, "script_invalid", err.Error(), 0)
			recordScheduleError(core.Settings.Home, s.Name, msg)
		}
	}
}
// runScriptStage executes a stage's committed script.sh (SCR-003 hybrid
// runner) and records the run like any stage: output to
// runs/<id>/stages/<NN-name>/script-output.txt, exit code on the stage
// record. Fail-fast by design: scripts are deterministic — a non-zero exit
// or a missing declared output fails the stage, no retry (owner decision c).
// Returns the combined output and the exit code.
func runScriptStage(ctx context.Context, core *Core, pb *PlaybookWorkspace, run *PlaybookRun, stage *WorkspaceStage, sessionID string) (string, int, error) {
	script := filepath.Join(stage.Dir, stage.Script)
	if _, err := os.Stat(script); err != nil {
		return "", 1, fmt.Errorf("stage %02d-%s: %s missing: %v", stage.Number, stage.Name, stage.Script, err)
	}
	stageDir := filepath.Join(core.Settings.Home, "playbooks", pb.Name, "runs", run.ID, "stages", fmt.Sprintf("%02d-%s", stage.Number, stage.Name))
	outPath := filepath.Join(stageDir, "script-output.txt")
	if err := os.MkdirAll(filepath.Join(stageDir, "output"), 0700); err != nil {
		return "", 1, err
	}
	ctx, cancel := context.WithTimeout(ctx, scriptRunTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, script)
	// cwd = the run-scoped stage dir: the script's relative paths (output/,
	// ../NN-name/output/) resolve inside the run record — consistent with the
	// LLM path's outputs, and the hybrid payload seam (LLM stage writes
	// output/payload.json, the next script stage reads ../NN-name/output/)
	// works without any absolute paths in committed scripts.
	cmd.Dir = stageDir
	cmd.Env = scriptEnv(sessionID)
	out, err := cmd.CombinedOutput()
	output := string(out)
	if werr := os.WriteFile(outPath, out, 0600); werr != nil {
		return output, 1, werr
	}
	if ctx.Err() != nil {
		return output, 1, fmt.Errorf("stage script timed out after %s", scriptRunTimeout)
	}
	if err == nil {
		return output, 0, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return output, ee.ExitCode(), nil
	}
	return output, 1, err
}

// missingStageOutputFiles lists the stage's declared outputs absent on disk
// after a script stage ran — the deterministic equivalent of the LLM path's
// output verification (a script that does not produce its contract fails).
func missingStageOutputFiles(pb *PlaybookWorkspace, run *PlaybookRun, stage *WorkspaceStage) []string {
	var missing []string
	for _, o := range stage.Outputs {
		p := playbookRunOutputPath(pb, run, *stage, o)
		if _, err := os.Stat(p); err != nil {
			missing = append(missing, p)
		}
	}
	return missing
}

// validateStageScripts runs the shared validator over every stage's script.sh
// (SCR-003): the schedule gate and boot re-check cover hybrid playbooks via
// this single seam, called at run start — an invalid stage script fails the
// run loudly, never silently skips.
func validateStageScripts(core *Core, pb *PlaybookWorkspace) error {
	for _, stage := range pb.Stages {
		if stage.Script == "" {
			continue
		}
		if err := validateScriptFile(core, filepath.Join(stage.Dir, stage.Script), fmt.Sprintf("%02d-%s", stage.Number, stage.Name)); err != nil {
			return err
		}
	}
	return nil
}

// validateScriptFile is the shared single-script check (bash -n + tool scan),
// used by the playbook-level validator and each stage validator.
func validateScriptFile(core *Core, path, label string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: no %s present", label, filepath.Base(path))
	}
	if out, err := exec.Command("bash", "-n", path).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: bash -n failed: %v\n%s", label, err, strings.TrimSpace(string(out)))
	}
	known := map[string]bool{}
	for _, tool := range core.Tools.Catalog() {
		known[tool.Name] = true
	}
	var unknown []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range minoExecToolRe.FindAllStringSubmatch(line, -1) {
			if !known[m[1]] {
				unknown = append(unknown, m[1])
			}
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("%s: unknown tool(s) in script: %s", label, strings.Join(unknown, ", "))
	}
	return nil
}

// tailOf returns the last n bytes of a string — for failure diagnostics.
func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// existingStageOutputs lists the stage's declared outputs present on disk.
func existingStageOutputs(pb *PlaybookWorkspace, run *PlaybookRun, stage *WorkspaceStage) []string {
	var found []string
	for _, o := range stage.Outputs {
		p := playbookRunOutputPath(pb, run, *stage, o)
		if _, err := os.Stat(p); err == nil {
			found = append(found, p)
		}
	}
	return found
}
