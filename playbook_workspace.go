package main

// Playbook workspace definitions and durable run state live in the filesystem.
// A definition describes stages; each run owns its own stage outputs and state.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type PlaybookWorkspace struct {
	Name        string
	Dir         string
	Description string
	// Agents is the workspace Layer 0 map and policy document. It is optional
	// during migration; migrated workspaces must provide AGENTS.md.
	Agents        string
	agentsPresent bool
	// RootContext is the workspace Layer 1 routing document.
	RootContext string
	Schedule    string
	Status      string
	// Agent is the config.md `agent:` binding — the roster persona this
	// playbook's runs wear (PSN-001). Deterministic binding, never
	// fuzzy-matched; resolved by validatePlaybookPersona.
	Agent  string
	Config map[string]string
	Stages []WorkspaceStage
}

type WorkspaceStage struct {
	Number  int
	Name    string
	Dir     string
	Context string
	// Script is script.sh when the stage is script-backed (issue #304): the
	// harness executes it directly, zero inference. "" = LLM stage.
	Script  string
	Inputs  []StageInput
	Tools   []string
	Outputs []StageOutput
	Audit   string
	Success []StageSuccess
}

// StageSuccess is a declared outcome from the optional ## Success section: the
// harness verifies a successful call to the named tool whose result carries a
// 15+ digit ID, so a run that claims victory without publishing fails.
type StageSuccess struct {
	Outcome string
	Tool    string
}

type StageInput struct {
	Source string
	Path   string
	Scope  string
}

type StageOutput struct {
	Name string
	Path string
}

type PlaybookRun struct {
	ID        string             `json:"id"`
	Playbook  string             `json:"playbook"`
	Request   string             `json:"request"`
	SessionID string             `json:"session_id"`
	Status    string             `json:"status"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	Stages    []PlaybookRunStage `json:"stages"`
	// #310: content hash of the stage contracts at run start. A resume whose
	// on-disk contracts differ fails loudly instead of continuing with stale
	// in-memory logic (the 2026-08-20 franken-run class).
	ContractHash string `json:"contract_hash,omitempty"`
	// #310: why a run was interrupted ("cancelled by owner" / "daemon shutting down").
	InterruptReason string `json:"interrupt_reason,omitempty"`
}

type PlaybookRunStage struct {
	Number    int       `json:"number"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Attempts  int       `json:"attempts"`
	Outputs   []string  `json:"outputs,omitempty"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	// Script-stage records (issue #304): additive, schema-light.
	Script       string `json:"script,omitempty"`        // script.sh when script-backed
	ScriptOutput string `json:"script_output,omitempty"` // runs/<id>/stages/<NN-name>/script-output.txt
	ExitCode     int    `json:"exit_code"`               // script exit status (0 when unset)
}

func validWeekday(d string) bool {
	for _, wd := range []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"} {
		if d == wd {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func extractSection(raw, heading string) string {
	start := strings.Index(raw, heading)
	if start < 0 {
		return ""
	}
	rest := raw[start+len(heading):]
	for i, line := range strings.Split(rest, "\n") {
		if i > 0 && strings.HasPrefix(line, "## ") {
			return strings.TrimSpace(strings.Join(strings.Split(rest, "\n")[:i], "\n"))
		}
	}
	return strings.TrimSpace(rest)
}

// runPlaybookStageLoop is an internal seam: production uses the canonical loop;
// focused run-state tests can substitute a deterministic stage outcome.
var runPlaybookStageLoop = func(ctx context.Context, client LLMClient, sessionID, system string, messages []Message, tools *Registry, maxIterations, maxTokens int, obs Observer, traceHome string) *LoopResult {
	return RunLoopContext(ctx, client, sessionID, system, messages, tools, maxIterations, maxTokens, obs, true, traceHome)
}

func loadPlaybookWorkspace(home, name string) (*PlaybookWorkspace, error) {
	if !validPlaybookName(name) {
		return nil, fmt.Errorf("invalid playbook name: %q", name)
	}
	dir := filepath.Join(home, "playbooks", name)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("playbook not found: %s", name)
	}
	root, err := os.ReadFile(filepath.Join(dir, "CONTEXT.md"))
	if err != nil {
		return nil, fmt.Errorf("playbook %s requires CONTEXT.md", name)
	}
	pb := &PlaybookWorkspace{Name: name, Dir: dir, Description: firstHeading(string(root)), RootContext: string(root), Status: "active", Config: map[string]string{}}
	if agents, readErr := os.ReadFile(filepath.Join(dir, "AGENTS.md")); readErr == nil {
		pb.Agents = string(agents)
		pb.agentsPresent = true
	} else if !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("playbook %s: read AGENTS.md: %w", name, readErr)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "config.md")); err == nil {
		parseWorkspaceConfig(data, pb)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "stages"))
	if err != nil {
		return nil, fmt.Errorf("playbook %s requires stages/: %w", name, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		stage, err := loadWorkspaceStage(filepath.Join(dir, "stages", entry.Name()), entry.Name())
		if err != nil {
			return nil, fmt.Errorf("playbook %s: %w", name, err)
		}
		pb.Stages = append(pb.Stages, *stage)
	}
	if len(pb.Stages) == 0 {
		return nil, fmt.Errorf("playbook %s has no stages", name)
	}
	sort.Slice(pb.Stages, func(i, j int) bool {
		if pb.Stages[i].Number != pb.Stages[j].Number {
			return pb.Stages[i].Number < pb.Stages[j].Number
		}
		// Stable tiebreak on name keeps execution order deterministic.
		return pb.Stages[i].Name < pb.Stages[j].Name
	})
	for i := range pb.Stages {
		if pb.Stages[i].Number < 0 {
			return nil, fmt.Errorf("playbook %s stage %q must start with NN-", name, pb.Stages[i].Name)
		}
		if len(pb.Stages[i].Outputs) == 0 && pb.Stages[i].Script == "" {
			// issue #304: a script-backed stage without CONTEXT.md declares no
			// outputs — its contract is the script itself (exit code + stdout).
			// With a CONTEXT.md, declared outputs are verified.
			return nil, fmt.Errorf("playbook %s stage %d has no declared outputs", name, pb.Stages[i].Number)
		}
	}
	return pb, nil
}

func parseWorkspaceConfig(data []byte, pb *PlaybookWorkspace) {
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		switch key {
		case "description":
			pb.Description = value
		case "status":
			pb.Status = value
		case "schedule":
			pb.Schedule = value
		case "agent":
			pb.Agent = value
		default:
			pb.Config[key] = value
		}
	}
}

func loadWorkspaceStage(dir, folder string) (*WorkspaceStage, error) {
	data, err := os.ReadFile(filepath.Join(dir, "CONTEXT.md"))
	if err != nil {
		// issue #304: a script-backed stage (script.sh in the stage dir) has
		// no CONTEXT.md — the script IS the stage contract.
		if _, serr := os.Stat(filepath.Join(dir, scriptFileName)); serr != nil {
			return nil, fmt.Errorf("stage %s requires CONTEXT.md", folder)
		}
	}
	parts := strings.SplitN(folder, "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("stage folder %q must be NN-name", folder)
	}
	number, err := strconv.Atoi(parts[0])
	if err != nil || number < 0 {
		return nil, fmt.Errorf("stage folder %q must be NN-name", folder)
	}
	context := string(data)
	stage := &WorkspaceStage{Number: number, Name: parts[1], Dir: dir, Context: context}
	if info, serr := os.Stat(filepath.Join(dir, scriptFileName)); serr == nil && !info.IsDir() {
		stage.Script = scriptFileName
	}
	stage.Inputs = parseStageInputs(extractSection(context, "## Inputs"))
	stage.Tools = parseStageTools(extractSection(context, "## Tools"))
	stage.Outputs = parseStageOutputs(extractSection(context, "## Outputs"))
	stage.Audit = extractSection(context, "## Audit")
	stage.Success = parseStageSuccess(extractSection(context, "## Success"))
	return stage, nil
}

func parseStageInputs(section string) []StageInput {
	var inputs []StageInput
	for _, line := range strings.Split(section, "\n") {
		cells := tableCells(line)
		if len(cells) < 3 || strings.EqualFold(cells[0], "source") || strings.HasPrefix(cells[0], "---") {
			continue
		}
		inputs = append(inputs, StageInput{Source: cells[0], Path: strings.Trim(cells[1], "`"), Scope: cells[2]})
	}
	return inputs
}

func parseStageSuccess(section string) []StageSuccess {
	var success []StageSuccess
	for _, line := range strings.Split(section, "\n") {
		cells := tableCells(line)
		if len(cells) < 2 || strings.EqualFold(cells[0], "outcome") || strings.HasPrefix(cells[0], "---") {
			continue
		}
		// Tool name is the first backticked span in the Required tool call
		// column ("`threads_post` returned a post ID"); fall back to the first
		// word when the contract omits backticks.
		tool := ""
		if start := strings.Index(cells[1], "`"); start >= 0 {
			if end := strings.Index(cells[1][start+1:], "`"); end >= 0 {
				tool = strings.TrimSpace(cells[1][start+1 : start+1+end])
			}
		}
		if tool == "" {
			if fields := strings.Fields(cells[1]); len(fields) > 0 {
				tool = strings.Trim(fields[0], "`,")
			}
		}
		if tool != "" {
			success = append(success, StageSuccess{Outcome: cells[0], Tool: tool})
		}
	}
	return success
}

func parseStageTools(section string) []string {
	var tools []string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if fields := strings.Fields(line); len(fields) > 0 {
			name := strings.Trim(fields[0], "` ,")
			if !strings.EqualFold(name, "none") {
				tools = append(tools, name)
			}
		}
	}
	return tools
}

func parseStageOutputs(section string) []StageOutput {
	var outputs []StageOutput
	for _, line := range strings.Split(section, "\n") {
		cells := tableCells(line)
		if len(cells) < 2 || strings.EqualFold(cells[0], "artifact") || strings.HasPrefix(cells[0], "---") {
			continue
		}
		path := strings.Trim(cells[1], "`")
		// Absolute paths are quarantined outputs (issue #22): enforced like
		// declared outputs but resolved outside the run workspace, so they
		// never enter the ALL_PLATFORMS glob or the distill queue.
		if filepath.IsAbs(path) {
			if !strings.Contains(filepath.ToSlash(path), "../") {
				outputs = append(outputs, StageOutput{Name: cells[0], Path: filepath.Clean(path)})
			}
			continue
		}
		clean := filepath.ToSlash(filepath.Clean(path))
		if !strings.HasPrefix(clean, "output/") || strings.Contains(clean, "../") || clean == "output" {
			continue
		}
		outputs = append(outputs, StageOutput{Name: cells[0], Path: clean})
	}
	return outputs
}

func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") {
		return nil
	}
	parts := strings.Split(strings.Trim(line, "|"), "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func firstHeading(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "# ") {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "# "))
		}
	}
	return ""
}

func playbookRunsDir(pb *PlaybookWorkspace) string { return filepath.Join(pb.Dir, "runs") }

func loadOrCreatePlaybookRun(pb *PlaybookWorkspace, registry *Registry, request, sessionID string, now time.Time) (*PlaybookRun, error) {
	if err := os.MkdirAll(playbookRunsDir(pb), 0700); err != nil {
		return nil, err
	}
	if run, err := latestResumablePlaybookRun(pb, registry); err != nil {
		return nil, err
	} else if run != nil {
		return run, nil
	}
	run := &PlaybookRun{ID: now.UTC().Format("20060102T150405.000000000Z"), Playbook: pb.Name, Request: request, SessionID: sessionID, Status: "running", CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	for _, stage := range pb.Stages {
		run.Stages = append(run.Stages, PlaybookRunStage{Number: stage.Number, Name: stage.Name, Status: "pending"})
	}
	return run, savePlaybookRun(pb, run)
}

func latestResumablePlaybookRun(pb *PlaybookWorkspace, registry *Registry) (*PlaybookRun, error) {
	entries, err := os.ReadDir(playbookRunsDir(pb))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	for _, id := range ids {
		data, err := os.ReadFile(filepath.Join(playbookRunsDir(pb), id, "state.json"))
		if err != nil {
			continue
		}
		var run PlaybookRun
		if json.Unmarshal(data, &run) != nil {
			continue
		}
		if run.Status != "running" && run.Status != "failed" {
			continue
		}
		// Resume safety: a stage that already ran (status failed or running) with
		// non-retry-safe tools may have executed external side effects — never
		// resume it (the VPS duplicate-Threads-post incident). A stage that never
		// started (pending) executed nothing and can resume freely.
		next := nextPlaybookStage(&run)
		if next != nil {
			stage, ok := workspaceStageFor(pb, next.Number, next.Name)
			if !ok {
				continue
			}
			if (next.Status == "failed" || next.Status == "running") && !stageRetrySafe(registry, stage) {
				continue
			}
		}
		return &run, nil
	}
	return nil, nil
}

func latestPlaybookRun(pb *PlaybookWorkspace) (*PlaybookRun, error) {
	entries, err := os.ReadDir(playbookRunsDir(pb))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	for _, id := range ids {
		data, err := os.ReadFile(filepath.Join(playbookRunsDir(pb), id, "state.json"))
		if err != nil {
			continue
		}
		var run PlaybookRun
		if json.Unmarshal(data, &run) == nil {
			return &run, nil
		}
	}
	return nil, nil
}

func savePlaybookRun(pb *PlaybookWorkspace, run *PlaybookRun) error {
	run.UpdatedAt = time.Now().UTC()
	dir := filepath.Join(playbookRunsDir(pb), run.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "state.json.tmp")
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "state.json"))
}

// writeRunStateFile persists a run's state.json atomically (tmp + rename).
func writeRunStateFile(path string, run PlaybookRun) error {
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReconcileInterruptedRuns marks playbook runs stuck in "running" across a
// restart as "interrupted" (OBS-001) — the 2026-08-14 orphan class: a crashed
// run stayed state.json:"running" forever and needed a manual quarantine.
// Returns the number reconciled.
func ReconcileInterruptedRuns(home string) int {
	root := filepath.Join(home, "playbooks")
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		runsDir := filepath.Join(root, e.Name(), "runs")
		rd, err := os.ReadDir(runsDir)
		if err != nil {
			continue
		}
		for _, r := range rd {
			if !r.IsDir() {
				continue
			}
			path := filepath.Join(runsDir, r.Name(), "state.json")
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var run PlaybookRun
			if json.Unmarshal(data, &run) != nil || run.Status != "running" {
				continue
			}
			run.Status = "interrupted"
			run.UpdatedAt = time.Now().UTC()
			if err := writeRunStateFile(path, run); err == nil {
				n++
			}
		}
	}
	if n > 0 {
		slog.Info("reconciled interrupted playbook runs", "count", n)
	}
	return n
}

// playbookRunRetention bounds how long a finished run directory survives on
// disk. 30 days matches the spill and trace retention horizons (loop.go).
const playbookRunRetention = 30 * 24 * time.Hour

// prunePlaybookRuns sweeps playbooks/*/runs/ (DATA-006, #404): trace and
// spill retention already bound their stores, but completed run directories
// accumulated with no ceiling. A run is only ever pruned if all of these
// hold:
//   - it isn't the newest run for its playbook (the resume path always looks
//     at the newest run first, so an older run is never the resume target
//     even if it happens to carry a resumable status)
//   - its status isn't "running" (an in-flight run is never touched)
//   - it is older than playbookRunRetention
//
// A run with an unreadable or corrupt state.json is left alone rather than
// guessed at — the same caution ReconcileInterruptedRuns takes.
//
// Before removal, a one-line summary is appended to runs-archive.jsonl in
// the playbook directory so the run's outcome survives even though its
// artifacts don't (DATA-006's "recoverable or leaves a durable summary"
// requirement).
func prunePlaybookRuns(home string) {
	root := filepath.Join(home, "playbooks")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-playbookRunRetention)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		prunePlaybookWorkspaceRuns(filepath.Join(root, e.Name()), cutoff)
	}
}

func prunePlaybookWorkspaceRuns(pbDir string, cutoff time.Time) {
	runsDir := filepath.Join(pbDir, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	if len(ids) <= 1 {
		return // nothing to prune, or only the (protected) newest run exists
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids))) // newest first
	for _, id := range ids[1:] {                   // newest is always protected
		runDir := filepath.Join(runsDir, id)
		data, err := os.ReadFile(filepath.Join(runDir, "state.json"))
		if err != nil {
			continue // unreadable state: leave it, don't guess
		}
		var run PlaybookRun
		if json.Unmarshal(data, &run) != nil {
			continue
		}
		if run.Status == "running" {
			continue
		}
		if run.CreatedAt.After(cutoff) {
			continue
		}
		if err := archivePlaybookRun(pbDir, &run); err != nil {
			continue // archive failed: keep the run rather than lose the record
		}
		os.RemoveAll(runDir)
	}
}

// archivePlaybookRun appends a durable one-line summary of run to
// runs-archive.jsonl before prunePlaybookWorkspaceRuns deletes its directory.
func archivePlaybookRun(pbDir string, run *PlaybookRun) error {
	f, err := os.OpenFile(filepath.Join(pbDir, "runs-archive.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	summary := struct {
		ID         string    `json:"id"`
		Status     string    `json:"status"`
		Request    string    `json:"request,omitempty"`
		CreatedAt  time.Time `json:"created_at"`
		UpdatedAt  time.Time `json:"updated_at"`
		Stages     int       `json:"stages"`
		ArchivedAt time.Time `json:"archived_at"`
	}{run.ID, run.Status, run.Request, run.CreatedAt, run.UpdatedAt, len(run.Stages), time.Now().UTC()}
	data, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

func nextPlaybookStage(run *PlaybookRun) *PlaybookRunStage {
	for i := range run.Stages {
		if run.Stages[i].Status != "complete" {
			return &run.Stages[i]
		}
	}
	return nil
}

func playbookRunOutputPath(pb *PlaybookWorkspace, run *PlaybookRun, stage WorkspaceStage, output StageOutput) string {
	// Absolute output paths are quarantined outputs (issue #22): they are
	// enforced like any declared output but live OUTSIDE the run workspace
	// (e.g. ~/.mino/data/...), so they never enter the ALL_PLATFORMS glob or
	// the distill queue. Run-workspace paths are joined as before.
	if filepath.IsAbs(output.Path) {
		return output.Path
	}
	return filepath.Join(playbookRunsDir(pb), run.ID, "stages", fmt.Sprintf("%02d-%s", stage.Number, stage.Name), filepath.FromSlash(output.Path))
}

// stageSelfCertified reports whether the stage's Audit section declares `self`
// (category 3: the model judges its own non-mechanically-verifiable work). The
// run is then labeled self-certified so the audit trail marks it as unverified.
func stageSelfCertified(stage WorkspaceStage) bool {
	lower := strings.ToLower(stage.Audit)
	for _, line := range strings.Split(lower, "\n") {
		line = strings.TrimSpace(line)
		if line == "self" || strings.HasPrefix(line, "self:") || line == "- self" {
			return true
		}
	}
	return false
}

// playbookWriteGuard keeps run artifacts attributed to their executing run.
// Playbook definitions are intentionally editable by Mino: they are the ICM
// workspace source that the agent maintains when evidence calls for repair.
// Returns an error message, or "" when the write is allowed.
func playbookWriteGuard(home, path string, ctx context.Context) string {
	clean := filepath.Clean(path)
	root := filepath.Clean(filepath.Join(home, "playbooks"))
	// Doubled-home hallucination guard: models sometimes emit paths that repeat
	// the home directory (e.g. /home/mino/.mino/.mino/playbooks/...). These are
	// fabricated paths — writing there succeeds but the file never lands where
	// verification looks, causing false-failed runs and duplicate side effects
	// (the VPS ai-news triple-send). Reject the whole class: home followed by
	// the home directory's own basename.
	homeClean := filepath.Clean(home)
	if strings.Contains(clean, homeClean+string(filepath.Separator)+filepath.Base(homeClean)) {
		return fmt.Sprintf("suspicious doubled-path: %s (home directory appears more than once; use the exact declared output path)", path)
	}
	// Stray relative .mino prefix (issue #42): ".mino/playbooks/..." resolves
	// via CWD to the right location but the recorded arg path never matches
	// the declared absolute output, so stage-output attribution fails (the
	// VPS reddit karma-log run, 2026-08-09). Reject with the corrected path.
	if strings.HasPrefix(clean, ".mino"+string(filepath.Separator)) {
		corrected := filepath.Join(home, strings.TrimPrefix(clean, ".mino"+string(filepath.Separator)))
		return fmt.Sprintf("stray relative .mino prefix: %s — use the absolute path %s", path, corrected)
	}
	if clean == root || !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		return ""
	}
	tags := traceTagsFromCtx(ctx)
	relative := strings.TrimPrefix(clean, root+string(filepath.Separator))
	parts := strings.SplitN(relative, string(filepath.Separator), 3)
	if len(parts) < 2 || parts[1] != "runs" {
		return ""
	}
	if tags["playbook"] == "" {
		return fmt.Sprintf("playbook run artifacts are writable only during stage execution: %s", path)
	}
	runDir := filepath.Join(root, tags["playbook"], "runs", tags["run"])
	if !strings.HasPrefix(clean, runDir+string(filepath.Separator)) {
		return fmt.Sprintf("playbook contract is read-only during execution: %s (only the current run's outputs are writable)", path)
	}
	return ""
}

// stageRetrySafe reports whether a stage may retry within a run: true only when
// its whitelist is non-empty and every declared tool is read-only
// (BehaviorObserve) or write_file (the required output mechanism). Destructive
// or unclassifiable tools (bash, MCP mutations, edit_file, send_message) make
// retry unsafe: a partially-executed destructive stage retried is how
// double-deletions happen. An empty whitelist means unrestricted access — not
// retry-safe.
func stageRetrySafe(registry *Registry, stage WorkspaceStage) bool {
	if len(stage.Tools) == 0 {
		return false
	}
	for _, name := range stage.Tools {
		if name == "write_file" {
			continue
		}
		if registry.BehaviorFor(name, nil) != BehaviorObserve {
			return false
		}
	}
	return true
}

func validateWorkspaceStageTools(pb *PlaybookWorkspace, registry *Registry) error {
	known := make(map[string]bool)
	for _, tool := range registry.Catalog() {
		known[tool.Name] = true
	}
	for _, stage := range pb.Stages {
		if len(stage.Tools) > 0 && !containsString(stage.Tools, "write_file") {
			return fmt.Errorf("playbook %s: stage %d (%s) declares tools without write_file", pb.Name, stage.Number, stage.Name)
		}
		for _, name := range stage.Tools {
			if !known[name] {
				return fmt.Errorf("playbook %s: stage %d (%s) declares unknown tool %q", pb.Name, stage.Number, stage.Name, name)
			}
		}
		// Autonomy rule: playbooks are standing orders that run without a human
		// present. A stage that defers to a human ("ask the owner", "request
		// approval", "wait for the user") is a conversation, not a playbook.
		lower := strings.ToLower(stage.Context)
		for _, pattern := range []string{"ask abah", "ask the user", "request approval", "ask for approval", "wait for approval", "human checkpoint", "stop here"} {
			if strings.Contains(lower, pattern) {
				return fmt.Errorf("playbook %s: stage %d (%s) requires a human checkpoint (%q) — playbooks are autonomous; if the task needs the owner, it is not a playbook", pb.Name, stage.Number, stage.Name, pattern)
			}
		}
	}
	return nil
}

// stageInputBudget caps the total rendered stage-input section of a stage
// prompt. Per-input rendering keeps its own 4000-char cap; this bounds the sum
// so a stage declaring many inputs cannot build an unbounded prompt.
const stageInputBudget = 20000

func buildWorkspaceStagePrompt(pb *PlaybookWorkspace, run *PlaybookRun, stage WorkspaceStage, now time.Time, loc *time.Location) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are executing playbook %q, run %s, stage %02d-%s.\n\n", pb.Name, run.ID, stage.Number, stage.Name)
	fmt.Fprintf(&b, "## Request\n\n%s\n\n", run.Request)
	b.WriteString("## Stage Contract\n\n")
	b.WriteString(stage.Context)
	b.WriteString("## Run Inputs\n")
	// Shared budget across all declared inputs: the per-input cap bounds a
	// single input, but N inputs were unbounded in total (10 × 4k = 40k chars).
	// Declaration order is the playbook author's priority — first inputs get
	// the budget first; once exhausted, later inputs render an explicit marker
	// instead of being silently dropped (issue #96 / wayfinder map #88).
	budget := stageInputBudget
	for _, input := range stage.Inputs {
		header := fmt.Sprintf("\n### %s\n", input.Path)
		if budget <= 0 {
			b.WriteString(header)
			b.WriteString("[omitted — stage input budget exceeded]\n")
			continue
		}
		block := header + renderWorkspaceInput(pb, run, stage, input, now, loc) + "\n"
		if len(block) > budget {
			block = block[:budget] + "\n[truncated — stage input budget exceeded]\n"
		}
		b.WriteString(block)
		budget -= len(block)
	}
	b.WriteString("\n## Required Outputs\n")
	for _, output := range stage.Outputs {
		fmt.Fprintf(&b, "- %s: `%s`\n", output.Name, playbookRunOutputPath(pb, run, stage, output))
	}
	b.WriteString("\n## Rules\n\n- Execute only this stage.\n- Use the declared inputs; do not infer missing state.\n- Complete the stage audit before writing outputs.\n- This playbook is an autonomous contract; do not request approval.\n- If the contract cannot be fulfilled or verified, state the failure plainly and do not claim success.\n")
	if len(stage.Tools) > 0 {
		fmt.Fprintf(&b, "- Declared capabilities for this stage: %s. Runtime policy remains authoritative.\n", strings.Join(stage.Tools, ", "))
	}
	return b.String()
}

func workspaceInputPath(pb *PlaybookWorkspace, run *PlaybookRun, stage WorkspaceStage, raw string) string {
	clean := filepath.ToSlash(filepath.Clean(raw))
	if strings.HasPrefix(clean, "../") {
		parts := strings.Split(clean, "/")
		if len(parts) >= 4 && parts[2] == "output" {
			return filepath.Join(playbookRunsDir(pb), run.ID, "stages", parts[1], filepath.FromSlash(strings.Join(parts[2:], "/")))
		}
	}
	if strings.HasPrefix(clean, "references/") {
		return filepath.Join(stage.Dir, filepath.FromSlash(clean))
	}
	// Absolute declared paths are deliberate absolute references (e.g. the
	// ALL_PLATFORMS exclusion glob); filepath.Join would mangle them into a
	// doubled path under the playbook dir (issue #86).
	if filepath.IsAbs(clean) {
		return filepath.FromSlash(clean)
	}
	return filepath.Join(pb.Dir, filepath.FromSlash(clean))
}

// renderWorkspaceInput renders one declared stage input. Runtime sources are
// answered from the run clock; paths with glob metacharacters are expanded to
// their matches (newest first, each prefixed with its path, bounded by the
// same 4000-char cap as a single file); everything else is read literally.
// An empty glob is a valid state for an exclusion list — "no files matched"
// is not an error, because "Unavailable" was being misread by the model as a
// skip reason (issue #86).
func renderWorkspaceInput(pb *PlaybookWorkspace, run *PlaybookRun, stage WorkspaceStage, input StageInput, now time.Time, loc *time.Location) string {
	if strings.EqualFold(input.Source, "Runtime") {
		local := now.In(loc)
		return fmt.Sprintf("%s (%s)", local.Format("2006-01-02"), local.Format("Monday"))
	}
	path := workspaceInputPath(pb, run, stage, input.Path)
	if strings.ContainsAny(path, "*?[") {
		matches, err := filepath.Glob(path)
		if err != nil {
			return "Unavailable: " + err.Error()
		}
		if len(matches) == 0 {
			return "No files matched."
		}
		return renderWorkspaceInputFiles(matches)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "Unavailable: " + err.Error()
	}
	return truncateWorkspaceInput(string(data))
}

// renderWorkspaceInputFiles concatenates glob matches newest-first (mtime,
// path as tiebreak), each under a path header so the model can attribute a log
// to its playbook/platform, then applies the shared truncation cap.
func renderWorkspaceInputFiles(matches []string) string {
	sort.Slice(matches, func(i, j int) bool {
		ti, tj := workspaceFileModTime(matches[i]), workspaceFileModTime(matches[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return matches[i] < matches[j]
	})
	var b strings.Builder
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil || len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n--- %s ---\n%s\n", m, strings.TrimSpace(string(data)))
	}
	return truncateWorkspaceInput(b.String())
}

func workspaceFileModTime(path string) time.Time {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

func truncateWorkspaceInput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4000 {
		s = s[:4000] + "\n[truncated]"
	}
	return s
}

func runWorkspacePlaybook(ctx context.Context, core *Core, name, request, sessionID string, obs Observer) (*PlaybookResult, error) {
	pb, err := loadPlaybookWorkspace(core.Settings.Home, name)
	if err != nil {
		return nil, err
	}
	if err := validateWorkspaceStageTools(pb, core.Tools); err != nil {
		return nil, err
	}
	// issue #304: every stage script.sh passes the shared validation seam
	// (bash -n + tool scan) before the run — an invalid script fails the run
	// loudly, never silently skips.
	if err := validateStageScripts(core, pb); err != nil {
		return nil, err
	}
	// PSN-001: a roster file deleted out from under a playbook fails pre-run,
	// not mid-stage — the persona is bound before the run starts.
	if err := validatePlaybookPersona(core.Settings.Home, pb); err != nil {
		return nil, err
	}
	run, err := loadOrCreatePlaybookRun(pb, core.Tools, request, sessionID, time.Now())
	if err != nil {
		return nil, err
	}
	result := &PlaybookResult{Name: name}
	// CTX-018: design-time audit gate — inject risk flags ONLY when the
	// playbook is new, its last run failed, or its contract changed since the
	// last run. A stable, recently-successful playbook runs without the audit
	// (no wasted resources). Computed once per run; injected per stage.
	doAudit := needsPlaybookAudit(pb)
	conversation := core.Sessions.Get(sessionID)
	// Cache stability: the system prompt must be byte-stable across all
	// iterations of a stage so the provider's prefix cache stays warm. The
	// clock goes into the stage prompt (user role) instead — the same intent
	// the main loop documents ("Time is injected as user message for cache
	// stability"). Before this fix the timestamp in system forced a full cache
	// rewrite on every call (~63% of playbook input billed at full rate).
	system, err := conversation.Session.BuildPlaybookSystem(pb)
	if err != nil {
		// PSN-001 review: the bound persona failed to load AFTER pre-run
		// validation passed (roster file deleted in the gap). Fail loudly —
		// never run the stage hatless. The run is saved failed so the resume
		// path does not treat it as a stuck "running" run.
		for i := range run.Stages {
			run.Stages[i].Status = "failed"
			run.Stages[i].Error = err.Error()
		}
		run.Status = "failed"
		_ = savePlaybookRun(pb, run)
		return nil, fmt.Errorf("playbook %s: %w", name, err)
	}
	baseMessages := conversation.Session.PlaybookContext(system)
	// Label the run self-certified when any stage's Audit declares `self` — the
	// audit trail then marks that no machine verified those outputs.
	for _, stage := range pb.Stages {
		if stageSelfCertified(stage) {
			result.SelfCertified = true
			break
		}
	}

	// #310: bind the stage contracts at run start. A resume whose on-disk
	// contracts differ from this hash fails loudly instead of continuing with
	// stale in-memory logic (the 2026-08-20 franken-run class).
	if run.ContractHash == "" {
		run.ContractHash = stageContractHash(pb, run)
	} else if run.ContractHash != stageContractHash(pb, run) {
		// A repaired workspace supersedes a failed run. Preserve its evidence,
		// then start cleanly with the current source instead of retrying stale
		// stage state forever.
		run.Status = "superseded"
		run.InterruptReason = "playbook definition changed; starting a fresh run with the repaired workspace"
		if err := savePlaybookRun(pb, run); err != nil {
			return nil, err
		}
		run, err = loadOrCreatePlaybookRun(pb, core.Tools, request, sessionID, time.Now())
		if err != nil {
			return nil, err
		}
		run.ContractHash = stageContractHash(pb, run)
	}

	// #310: cancellable run context — the cancel_run tool and the shutdown
	// hook both cancel here; the stage loop checks ctx.Done() at each boundary
	// and marks the run interrupted cleanly. The parent is detachCancel'd
	// (#316): manual runs inherit the caller's VALUES (session, trace tags,
	// audit/snapshot callbacks) but NOT the caller's cancellation — a client
	// disconnect mid-run no longer kills the playbook; cancel_run does.
	runCtx, cancelRunCtx := context.WithCancel(detachCancel(ctx))
	defer cancelRunCtx()
	deregister := registerRun(run.ID, cancelRunCtx)
	defer deregister()

	for {
		state := nextPlaybookStage(run)
		if state == nil {
			run.Status = "complete"
			if err := savePlaybookRun(pb, run); err != nil {
				return nil, err
			}
			result.Status = "complete"
			return result, nil
		}
		// #310: cancellation check at the stage boundary — a cancelled run
		// marks itself interrupted (distinct from failed) with the reason,
		// keeps partial outputs on disk, never silent.
		if runCtx.Err() != nil {
			run.Status = "interrupted"
			if reason := takeRunInterruptReason(run.ID); reason != "" {
				run.InterruptReason = reason
			} else if run.InterruptReason == "" {
				run.InterruptReason = "cancelled"
			}
			for _, s := range run.Stages {
				if s.Status == "running" {
					s.Status = "interrupted"
				}
			}
			_ = savePlaybookRun(pb, run)
			result.Status = "interrupted"
			result.Reply = fmt.Sprintf("Run %s interrupted: %s", run.ID, run.InterruptReason)
			logTrace(core.Settings.Home, "run_interrupted", map[string]any{"playbook": name, "run": run.ID, "reason": run.InterruptReason})
			core.auditLog(sessionID, "run_interrupted", run.InterruptReason, 0)
			return result, nil
		}
		// #310: vanished run dir guard — the run record must still exist where
		// the runner believes it is; if someone moved/archived it, fail loud.
		if _, err := os.Stat(filepath.Join(playbookRunsDir(pb), run.ID)); err != nil {
			run.Status = "failed"
			run.InterruptReason = "run dir vanished during execution"
			result.Status = "failed"
			result.Reply = fmt.Sprintf("Run %s failed: run directory vanished during execution — manual review required", run.ID)
			logTrace(core.Settings.Home, "run_dir_vanished", map[string]any{"playbook": name, "run": run.ID})
			core.auditLog(sessionID, "run_dir_vanished", run.ID, 0)
			return result, nil
		}

		stage, ok := workspaceStageFor(pb, state.Number, state.Name)
		if !ok {
			return nil, fmt.Errorf("playbook %s run %s references missing stage %d", name, run.ID, state.Number)
		}
		// issue #304: a script-backed stage is deterministic — fail-fast, no
		// retry (owner decision c). Non-zero exit or a missing declared
		// output fails the stage and the run, never silently.
		if stage.Script != "" {
			state.Attempts++
			state.StartedAt = time.Now().UTC()
			state.Status = "running"
			state.Error = ""
			state.Script = stage.Script
			if err := savePlaybookRun(pb, run); err != nil {
				return nil, err
			}
			// Tag trace events inside this stage with its playbook/stage/run
			// identity so the dashboard can group stage work instead of
			// flattening it; the tags also put any tool-path write under the
			// playbookWriteGuard's run-scoped writable zone. Built from runCtx,
			// not ctx (issue #438): ctx is the caller's request context, which
			// #316 detached specifically so the stage's own work survives a
			// client disconnect or the outer turn's context ending — wrapping
			// ctx here silently undid that isolation for every stage's actual
			// execution, leaving only the stage-boundary check upstream
			// protected.
			stageCtx := context.WithValue(runCtx, traceTagKey{}, map[string]string{
				"playbook": pb.Name,
				"stage":    fmt.Sprintf("%02d-%s", stage.Number, stage.Name),
				"run":      run.ID,
			})
			out, code, runErr := runScriptStage(stageCtx, core, pb, run, &stage)
			state.EndedAt = time.Now().UTC()
			state.ScriptOutput = filepath.Join("runs", run.ID, "stages", fmt.Sprintf("%02d-%s", stage.Number, stage.Name), "script-output.txt")
			state.ExitCode = code
			result.StagesRun++
			missing := missingStageOutputFiles(pb, run, &stage)
			if runErr != nil || code != 0 || len(missing) > 0 {
				reason := fmt.Sprintf("script stage %02d-%s: exit %d", stage.Number, stage.Name, code)
				if len(missing) > 0 {
					reason += fmt.Sprintf(", missing declared output(s): %s", strings.Join(missing, ", "))
				}
				if runErr != nil {
					reason += ": " + runErr.Error()
				}
				state.Status = "failed"
				state.Error = reason
				run.Status = "failed"
				_ = savePlaybookRun(pb, run)
				result.Status = "failed"
				result.Reply = fmt.Sprintf("Run %s stopped at stage %02d-%s: %s", run.ID, stage.Number, stage.Name, reason)
				logTrace(core.Settings.Home, "script_stage_failed", map[string]any{
					"playbook": pb.Name, "stage": fmt.Sprintf("%02d-%s", stage.Number, stage.Name),
					"run": run.ID, "exit_code": code, "output_tail": tailOf(out, 200),
				})
				core.auditLog(sessionID, "script_stage_failed", reason, 0)
				return result, nil
			}
			state.Status = "complete"
			state.Outputs = existingStageOutputs(pb, run, &stage)
			run.Status = "running"
			result.Outputs = append(result.Outputs, state.Outputs...)
			if err := savePlaybookRun(pb, run); err != nil {
				return nil, err
			}
			logTrace(core.Settings.Home, "script_stage", map[string]any{
				"playbook": pb.Name, "stage": fmt.Sprintf("%02d-%s", stage.Number, stage.Name),
				"run": run.ID, "exit_code": code, "output_chars": len(out),
			})
			// OBS-002: every script-stage execution is an auditable action.
			core.auditLog(sessionID, "script_stage", fmt.Sprintf("%s stage %02d-%s: script exit %d, run %s", pb.Name, stage.Number, stage.Name, code, run.ID), 0)
			continue
		}
		// Stage tools expand the normal loop's tool surface; they are not a
		// whitelist. Core safety, approval, and registered-tool boundaries stay
		// authoritative while the model retains its always-available and sliding
		// choices.
		stageTools := core.Tools
		messages := append([]Message(nil), baseMessages...)
		stagePrompt := buildWorkspaceStagePrompt(pb, run, stage, time.Now(), core.Settings.Location()) + "\n\n" + appendSystemTime("", time.Now(), core.Settings.Location())
		if doAudit {
			if flags := stageRiskFlags(stage); flags != "" {
				stagePrompt += "\n\n" + flags
			}
		}
		messages = append(messages, Message{Role: "user", Content: stagePrompt})
		retrySafe := stageRetrySafe(core.Tools, stage)

		var outputs []string
		var verifyErr error
		for attempt := 1; attempt <= maxStageAttempts; attempt++ {
			state.Attempts++
			state.StartedAt = time.Now().UTC()
			state.Status = "running"
			state.Error = ""
			if err := savePlaybookRun(pb, run); err != nil {
				return nil, err
			}
			// Tag every trace event inside this stage with its playbook/stage identity
			// so the dashboard can group stage work instead of flattening it. Built
			// from runCtx, not ctx (issue #438) — see the script-stage branch above
			// for why: ctx is the caller's request context, and wrapping it here
			// re-exposed the stage's LLM loop to whatever cancels the caller even
			// though #316 detached the run specifically to prevent that.
			stageCtx := context.WithValue(runCtx, traceTagKey{}, map[string]string{
				"playbook": pb.Name,
				"stage":    fmt.Sprintf("%02d-%s", stage.Number, stage.Name),
				"run":      run.ID,
			})
			// Stage contract travels with the context: the loop refuses to declare
			// a stage turn complete while its declared outputs are missing, and
			// pushes the model to write them instead of ending silently.
			outPaths := make([]string, 0, len(stage.Outputs))
			for _, o := range stage.Outputs {
				outPaths = append(outPaths, playbookRunOutputPath(pb, run, stage, o))
			}
			stageCtx = context.WithValue(stageCtx, stageOutputsKey{}, outPaths)
			stageCtx = context.WithValue(stageCtx, stageToolNamesKey{}, append([]string(nil), stage.Tools...))
			// DRF-002 provenance honesty: facts saved during a playbook run are
			// model-distilled, not user-authored — save_note consults this
			// counter and stamps "model-distill" instead of "user".
			playbookDepth.Add(1)
			stageResult := runPlaybookStageLoop(stageCtx, core.Client, sessionID, system, messages, stageTools, maxStageIterations, core.Settings.MaxTokens, obs, core.Settings.Home)
			playbookDepth.Add(-1)
			result.TokensIn += stageResult.TokensIn
			result.TokensOut += stageResult.TokensOut
			result.ToolCalls = append(result.ToolCalls, stageResult.ToolCalls...)
			result.Reply = stageResult.Reply
			result.StagesRun++
			state.EndedAt = time.Now().UTC()
			outputs, verifyErr = verifyWorkspaceStageOutputs(pb, run, stage, stageResult.ToolCalls, state.StartedAt)
			if flags := stageDeviationFlags(pb, run, stage, stageResult.ToolCalls, verifyErr); len(flags) > 0 {
				reportStageDeviations(core, sessionID, pb, run, stage, flags)
			}
			// A stage's contract is its verified outputs: a runtime error after
			// the work is written (e.g. a flaked final model call) must not fail
			// the stage — only a cancelled run or unverified outputs do.
			// Observed 2026-08-08: the 09:30 run failed with "all vision
			// providers failed" while the post was already published and the
			// log written.
			if verifyErr == nil && (stageResult.Status == "complete" || stageResult.Status == "error") {
				if stageResult.Status == "error" {
					result.Reply = fmt.Sprintf("(work verified complete; final model call failed: %s)", stageResult.Reply)
				}
				break
			}
			// Failed attempt. Retry only when the stage is retry-safe (read-only
			// whitelist) and the run was not cancelled — a user stop must never be
			// overridden by an automatic retry.
			if !retrySafe || stageResult.Status == "cancelled" || attempt >= maxStageAttempts {
				state.Status = "failed"
				if stageResult.Status != "complete" {
					state.Error = fmt.Sprintf("runtime %s: %s", stageResult.Status, stageResult.Reply)
				} else {
					state.Error = verifyErr.Error()
				}
				// Outcome failures (## Success) get a dedicated trace event so the
				// dashboard can surface "ran but did not publish" separately from
				// missing artifacts.
				var of *outcomeFailure
				if errors.As(verifyErr, &of) {
					logTrace(core.Settings.Home, "stage_outcome_failed", map[string]any{
						"playbook": pb.Name,
						"stage":    fmt.Sprintf("%02d-%s", stage.Number, stage.Name),
						"run":      run.ID,
						"outcome":  of.Outcome,
						"tool":     of.Tool,
					})
				}
				run.Status = "failed"
				_ = savePlaybookRun(pb, run)
				result.Status = "failed"
				result.Reply = fmt.Sprintf("Run %s stopped at stage %02d-%s: %s", run.ID, stage.Number, stage.Name, state.Error)
				return result, nil
			}
			// Feed the failure back into the stage context and retry.
			reason := ""
			if stageResult.Status != "complete" {
				reason = fmt.Sprintf("runtime %s: %s", stageResult.Status, stageResult.Reply)
			} else {
				reason = verifyErr.Error()
			}
			messages = append(messages, Message{Role: "user", Content: fmt.Sprintf("[System: stage attempt %d failed — %s. Fix the issue and complete the stage properly.]", attempt, reason)})
		}
		state.Status, state.Outputs = "complete", outputs
		run.Status = "running"
		result.Outputs = append(result.Outputs, outputs...)
		if err := savePlaybookRun(pb, run); err != nil {
			return nil, err
		}
	}
}

func workspaceStage(pb *PlaybookWorkspace, number int) (WorkspaceStage, bool) {
	for _, stage := range pb.Stages {
		if stage.Number == number {
			return stage, true
		}
	}
	return WorkspaceStage{}, false
}

// workspaceStageFor resolves a run-state stage to its workspace contract by
// number AND name — checkpoint splits add a second stage with the act stage's
// number (02-act then 02-act-b), so number-only lookup would resolve both to
// 02-act. Falls back to number-only for runs predating the split.
func workspaceStageFor(pb *PlaybookWorkspace, number int, name string) (WorkspaceStage, bool) {
	if name != "" {
		for _, stage := range pb.Stages {
			if stage.Number == number && stage.Name == name {
				return stage, true
			}
		}
	}
	return workspaceStage(pb, number)
}

// outcomeID is the harness's proof-of-publication: a 15+ digit platform ID in
// the tool result. The model cannot satisfy it by editing logs — only a real
// publish call returns one.
var outcomeID = regexp.MustCompile(`\d{15,}`)

// outcomeFailure marks a failed ## Success verification so the caller can
// trace it as stage_outcome_failed instead of a generic output miss.
type outcomeFailure struct {
	Outcome string
	Tool    string
}

func (e *outcomeFailure) Error() string {
	return fmt.Sprintf("required outcome %q: no successful %s call recorded — publish or say why", e.Outcome, e.Tool)
}

// verifyWorkspaceStageOutputs enforces write-attributed completion: a declared
// output passes only if it exists, is non-empty, AND was genuinely produced
// during this stage attempt — either a write_file call recorded inside this
// stage's own tool log (fast path), or, tool-agnostically, an mtime at or
// after the attempt started (issue #460: a stage that writes its declared
// output via bash heredoc/redirection, edit_file, or sync_file instead of
// write_file was false-failing here even though the file was genuinely
// produced this attempt). Pre-seeded files (main loop doing the work, then
// run_playbook rubber-stamping) still fail attribution either way, since
// their mtime predates the attempt.
// Declared ## Success outcomes are checked next: a successful call to the named
// tool whose result carries a 15+ digit ID. Absent section = unchanged behavior.
func verifyWorkspaceStageOutputs(pb *PlaybookWorkspace, run *PlaybookRun, stage WorkspaceStage, calls []ToolCall, attemptStart time.Time) ([]string, error) {
	wrote := make(map[string]bool)
	for _, call := range calls {
		if call.Name != "write_file" {
			continue
		}
		if path, _ := call.Args["path"].(string); path != "" {
			wrote[filepath.Clean(path)] = true
		}
	}
	outputs := make([]string, 0, len(stage.Outputs))
	for _, output := range stage.Outputs {
		path := playbookRunOutputPath(pb, run, stage, output)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Size() == 0 {
			return nil, fmt.Errorf("required output %q was not written", output.Path)
		}
		if !wrote[filepath.Clean(path)] && info.ModTime().Before(attemptStart) {
			return nil, fmt.Errorf("required output %q exists but was not written by this stage's tools", output.Path)
		}
		outputs = append(outputs, path)
	}
	for _, want := range stage.Success {
		verified := false
		for _, call := range calls {
			if call.Name == want.Tool && toolOutputStatus(call.Output) == "ok" && outcomeID.MatchString(call.Output) {
				verified = true
				break
			}
		}
		if !verified {
			return nil, &outcomeFailure{Outcome: want.Outcome, Tool: want.Tool}
		}
	}
	return outputs, nil
}

// stageDeviationFlags compares a completed stage attempt with its mechanical
// output contract. Stage.Tools expands the model's choices; it is not an
// execution whitelist. The existing registry and output verifier remain the
// enforcement boundaries.
func stageDeviationFlags(pb *PlaybookWorkspace, run *PlaybookRun, stage WorkspaceStage, calls []ToolCall, verificationErr error) []string {
	var flags []string
	seen := make(map[string]bool)
	add := func(flag string) {
		if !seen[flag] {
			seen[flag] = true
			flags = append(flags, flag)
		}
	}
	for _, call := range calls {
		if call.Name != "write_file" {
			continue
		}
		path, _ := call.Args["path"].(string)
		if path == "" {
			continue
		}
		declared := false
		for _, output := range stage.Outputs {
			if filepath.Clean(path) == filepath.Clean(playbookRunOutputPath(pb, run, stage, output)) {
				declared = true
				break
			}
		}
		if !declared {
			add("write_file targeted an undeclared stage output path")
		}
	}
	if verificationErr != nil {
		add("contract verification failed: " + truncate(verificationErr.Error(), 240))
	}
	return flags
}

// reportStageDeviations records and pages a mechanical deviation without
// changing the stage result. The local trace, audit table, and outbox are the
// existing inspectable owner-facing channels.
func reportStageDeviations(core *Core, sessionID string, pb *PlaybookWorkspace, run *PlaybookRun, stage WorkspaceStage, flags []string) {
	if core == nil || core.Settings == nil || len(flags) == 0 {
		return
	}
	stageName := fmt.Sprintf("%02d-%s", stage.Number, stage.Name)
	reason := strings.Join(flags, "; ")
	message := fmt.Sprintf("⚠️ Playbook contract deviation: %s stage %s\n%s\nThe run was not blocked; inspect the stage trace and run artifacts.", pb.Name, stageName, reason)
	queueOutbox(core.Settings.Home, "owner", message)
	core.auditLog(sessionID, "stage_deviation", fmt.Sprintf("%s stage %s run %s: %s", pb.Name, stageName, run.ID, reason), 0)
	logTrace(core.Settings.Home, "stage_deviation", map[string]any{
		"playbook": pb.Name,
		"stage":    stageName,
		"run":      run.ID,
		"flags":    flags,
	})
}
