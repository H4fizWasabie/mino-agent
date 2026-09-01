package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// playbook_script.go — script-backed playbook stages (issue #304, PA-007).
//
// A stage dir carrying script.sh is a script-backed stage: the harness
// executes it directly — zero inference tokens, no model call. The script is
// a committed, reviewed owner artifact, never generated at runtime. A
// non-zero exit or a missing declared output fails the stage and the run,
// never silently.
//
// Ported deliberately from the parked v2.19 hybrid runner
// (backup/v2.16-v2.20-era, SCR-003) onto the pre-v2.16 baseline, which has no
// script-stage machinery at all. The `mino exec` subprocess layer, code mode,
// and stub module of that era are OUT of scope and stay parked.

// scriptFileName is the committed artifact name inside a stage dir.
const scriptFileName = "script.sh"

func scriptFileNameFor(goos string) string {
	if goos == "windows" {
		return "script.ps1"
	}
	return scriptFileName
}

func scriptCandidatesFor(goos string) []string {
	if goos == "windows" {
		return []string{"script.ps1", "script.sh"}
	}
	return []string{"script.sh"}
}

// scriptRunTimeout bounds one script run (scheduled pipeline work; the
// failed LLM run observed in design burned 12 minutes, so 30 is sane
// headroom). A var so tests can shorten it. ponytail: not a config knob
// yet — add one when the pilot phase actually needs it.
var scriptRunTimeout = 30 * time.Minute

// minoExecToolRe finds `mino exec <tool>` invocations for the tool-name
// scan in validateScriptFile. Case-insensitive: MCP tools register under
// uppercase names (MCP_composio_*), and the registry lookup stays
// exact-case — a lowercase "mcp_..." invocation correctly reports unknown.
var minoExecToolRe = regexp.MustCompile(`(?i)\bmino exec\s+([a-z0-9_-]+)`)

// scriptEnv is the minimal environment for script children. The mino
// process runs with EnvironmentFile=mino.env (systemd), so os.Environ()
// carries every provider key and the bot token — a committed script must
// never inherit those. Only the essentials ride along.
func scriptEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TZ=" + os.Getenv("TZ"),
		"LANG=" + os.Getenv("LANG"),
	}
}

// validateStageScripts runs the shared validator over every stage's
// script.sh: the edit-time path (manage_playbook) and the pre-run path
// (runWorkspacePlaybook) call this one seam — one behavior everywhere, so the
// checks cannot drift. An invalid stage script fails the run loudly, never
// silently skips.
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

// validateScriptFile is the shared single-script check (bash -n + tool scan).
// bash -n proves syntax; the tool-name scan proves every `mino exec <tool>`
// in the script resolves in the registry. Comment lines are skipped: a
// comment mentioning a tool is not an invocation.
func validateScriptFile(core *Core, path, label string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: no %s present", label, filepath.Base(path))
	}
	if runtime.GOOS == "windows" {
		if filepath.Ext(path) == ".sh" && exec.Command("bash", "-n", path).Run() == nil {
			// Git Bash is a supported best-effort compatibility shell.
		} else if filepath.Ext(path) != ".ps1" {
			return fmt.Errorf("%s: %s is a Bash script; provide a native .ps1 script on Windows", label, filepath.Base(path))
		}
	} else if out, err := exec.Command("bash", "-n", path).CombinedOutput(); err != nil {
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

// runScriptStage executes a stage's committed script.sh and records the run
// like any stage: output to runs/<id>/stages/<NN-name>/script-output.txt,
// exit code on the stage record. Fail-fast by design: scripts are
// deterministic — a non-zero exit or a missing declared output fails the
// stage, no retry (owner decision c). Returns the combined output and the
// exit code.
func runScriptStage(ctx context.Context, core *Core, pb *PlaybookWorkspace, run *PlaybookRun, stage *WorkspaceStage) (string, int, error) {
	script := filepath.Join(stage.Dir, stage.Script)
	if _, err := os.Stat(script); err != nil {
		return "", 1, fmt.Errorf("stage %02d-%s: %s missing: %v", stage.Number, stage.Name, stage.Script, err)
	}
	stageDir := filepath.Join(playbookRunsDir(pb), run.ID, "stages", fmt.Sprintf("%02d-%s", stage.Number, stage.Name))
	outPath := filepath.Join(stageDir, "script-output.txt")
	if err := os.MkdirAll(filepath.Join(stageDir, "output"), 0700); err != nil {
		return "", 1, err
	}
	ctx, cancel := context.WithTimeout(ctx, scriptRunTimeout)
	defer cancel()
	cmd := nativeScriptCommand(ctx, script)
	// cwd = the run-scoped stage dir: the script's relative paths (output/,
	// ../NN-name/output/) resolve inside the run record — consistent with the
	// LLM path's outputs, and exactly the zone playbookWriteGuard allows for
	// the tagged run, so a script's writes need no absolute paths.
	cmd.Dir = stageDir
	cmd.Env = scriptEnv()
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

func nativeScriptCommand(ctx context.Context, path string) *exec.Cmd {
	if runtime.GOOS == "windows" && filepath.Ext(path) == ".ps1" {
		return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
	}
	return exec.CommandContext(ctx, "bash", path)
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

// stageContractHash returns a content hash of the stage contracts the run
// references (names, scripts, tools, outputs) — the run-start binding that
// makes franken-resume impossible (#310). A run referencing 1-news-report
// while disk now has 1-judgment must fail loudly.
func stageContractHash(pb *PlaybookWorkspace, run *PlaybookRun) string {
	h := sha256.New()
	for _, rs := range run.Stages {
		// Strict number+name match ONLY — never the number-fallback: the
		// 2026-08-20 franken-resume ran a new stage under an old record via
		// workspaceStageFor's fallback. A stage the run references that does
		// not exist under its exact name hashes as MISSING and fails resume.
		var stage *WorkspaceStage
		for i := range pb.Stages {
			if pb.Stages[i].Number == rs.Number && pb.Stages[i].Name == rs.Name {
				stage = &pb.Stages[i]
				break
			}
		}
		if stage == nil {
			fmt.Fprintf(h, "%d\x00%s\x00MISSING\x00", rs.Number, rs.Name)
			continue
		}
		fmt.Fprintf(h, "%d\x00%s\x00%s\x00", stage.Number, stage.Name, stage.Script)
		for _, t := range stage.Tools {
			fmt.Fprintf(h, "t:%s\x00", t)
		}
		for _, o := range stage.Outputs {
			fmt.Fprintf(h, "o:%s\x00", o.Path)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// tailOf returns the last n bytes of a string — for failure diagnostics.
func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
