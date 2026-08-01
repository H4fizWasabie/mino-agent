package main

// Playbook workspace definitions and durable run state live in the filesystem.
// A definition describes stages; each run owns its own stage outputs and state.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type PlaybookWorkspace struct {
	Name        string
	Dir         string
	Description string
	Schedule    string
	Status      string
	Config      map[string]string
	Stages      []WorkspaceStage
}

type WorkspaceStage struct {
	Number  int
	Name    string
	Dir     string
	Context string
	Inputs  []StageInput
	Tools   []string
	Outputs []StageOutput
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
	return RunLoopContext(ctx, client, sessionID, system, messages, tools, maxIterations, maxTokens, obs, true, traceHome, nil)
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
	pb := &PlaybookWorkspace{Name: name, Dir: dir, Description: firstHeading(string(root)), Status: "active", Config: map[string]string{}}
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
	sort.Slice(pb.Stages, func(i, j int) bool { return pb.Stages[i].Number < pb.Stages[j].Number })
	for i := range pb.Stages {
		if pb.Stages[i].Number == 0 {
			return nil, fmt.Errorf("playbook %s stage %q must start with NN-", name, pb.Stages[i].Name)
		}
		if len(pb.Stages[i].Outputs) == 0 {
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
		default:
			pb.Config[key] = value
		}
	}
}

func loadWorkspaceStage(dir, folder string) (*WorkspaceStage, error) {
	data, err := os.ReadFile(filepath.Join(dir, "CONTEXT.md"))
	if err != nil {
		return nil, fmt.Errorf("stage %s requires CONTEXT.md", folder)
	}
	parts := strings.SplitN(folder, "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("stage folder %q must be NN-name", folder)
	}
	number, err := strconv.Atoi(parts[0])
	if err != nil || number < 1 {
		return nil, fmt.Errorf("stage folder %q must be NN-name", folder)
	}
	context := string(data)
	stage := &WorkspaceStage{Number: number, Name: parts[1], Dir: dir, Context: context}
	stage.Inputs = parseStageInputs(extractSection(context, "## Inputs"))
	stage.Tools = parseStageTools(extractSection(context, "## Tools"))
	stage.Outputs = parseStageOutputs(extractSection(context, "## Outputs"))
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

func parseStageTools(section string) []string {
	var tools []string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if fields := strings.Fields(line); len(fields) > 0 && !strings.EqualFold(fields[0], "none") {
			tools = append(tools, fields[0])
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

func loadOrCreatePlaybookRun(pb *PlaybookWorkspace, request, sessionID string, now time.Time) (*PlaybookRun, error) {
	if err := os.MkdirAll(playbookRunsDir(pb), 0700); err != nil {
		return nil, err
	}
	if run, err := latestResumablePlaybookRun(pb); err != nil {
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

func latestResumablePlaybookRun(pb *PlaybookWorkspace) (*PlaybookRun, error) {
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
		if json.Unmarshal(data, &run) == nil && (run.Status == "running" || run.Status == "failed") {
			return &run, nil
		}
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

func nextPlaybookStage(run *PlaybookRun) *PlaybookRunStage {
	for i := range run.Stages {
		if run.Stages[i].Status != "complete" {
			return &run.Stages[i]
		}
	}
	return nil
}

func playbookRunOutputPath(pb *PlaybookWorkspace, run *PlaybookRun, stage WorkspaceStage, output StageOutput) string {
	return filepath.Join(playbookRunsDir(pb), run.ID, "stages", fmt.Sprintf("%02d-%s", stage.Number, stage.Name), filepath.FromSlash(output.Path))
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
	}
	return nil
}

func buildWorkspaceStagePrompt(pb *PlaybookWorkspace, run *PlaybookRun, stage WorkspaceStage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are executing playbook %q, run %s, stage %02d-%s.\n\n", pb.Name, run.ID, stage.Number, stage.Name)
	fmt.Fprintf(&b, "## Request\n\n%s\n\n", run.Request)
	b.WriteString("## Stage Contract\n\n")
	b.WriteString(stage.Context)
	b.WriteString("\n\n## Run Inputs\n")
	for _, input := range stage.Inputs {
		path := workspaceInputPath(pb, run, stage, input.Path)
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(&b, "\n### %s\nUnavailable: %v\n", input.Path, err)
			continue
		}
		content := strings.TrimSpace(string(data))
		if len(content) > 4000 {
			content = content[:4000] + "\n[truncated]"
		}
		fmt.Fprintf(&b, "\n### %s\n%s\n", input.Path, content)
	}
	b.WriteString("\n## Required Outputs\n")
	for _, output := range stage.Outputs {
		fmt.Fprintf(&b, "- %s: `%s`\n", output.Name, playbookRunOutputPath(pb, run, stage, output))
	}
	b.WriteString("\n## Rules\n\n- Execute only this stage.\n- Use the declared inputs; do not infer missing state.\n- Complete the stage audit before writing outputs.\n- This playbook is an autonomous contract; do not request approval.\n- If the contract cannot be fulfilled or verified, state the failure plainly and do not claim success.\n")
	if len(stage.Tools) > 0 {
		fmt.Fprintf(&b, "- Use only these tools: %s.\n", strings.Join(stage.Tools, ", "))
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
	return filepath.Join(pb.Dir, filepath.FromSlash(clean))
}

func runWorkspacePlaybook(ctx context.Context, core *Core, name, request, sessionID string, obs Observer) (*PlaybookResult, error) {
	pb, err := loadPlaybookWorkspace(core.Settings.Home, name)
	if err != nil {
		return nil, err
	}
	if err := validateWorkspaceStageTools(pb, core.Tools); err != nil {
		return nil, err
	}
	run, err := loadOrCreatePlaybookRun(pb, request, sessionID, time.Now())
	if err != nil {
		return nil, err
	}
	result := &PlaybookResult{Name: name}
	conversation := core.Sessions.Get(sessionID)
	system := appendSystemTime(conversation.Session.BuildPlaybookSystem(run.Request, ""), time.Now(), core.Settings.Location())
	baseMessages := conversation.Session.PlaybookContext(system)

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
		stage, ok := workspaceStage(pb, state.Number)
		if !ok {
			return nil, fmt.Errorf("playbook %s run %s references missing stage %d", name, run.ID, state.Number)
		}
		state.Status, state.Error = "running", ""
		state.Attempts++
		state.StartedAt = time.Now().UTC()
		if err := savePlaybookRun(pb, run); err != nil {
			return nil, err
		}
		stageTools := core.Tools
		if len(stage.Tools) > 0 {
			stageTools = core.Tools.Only(stage.Tools...)
		}
		messages := append([]Message(nil), baseMessages...)
		messages = append(messages, Message{Role: "user", Content: buildWorkspaceStagePrompt(pb, run, stage)})
		stageResult := runPlaybookStageLoop(ctx, core.Client, sessionID, system, messages, stageTools, maxStageIterations, core.Settings.MaxTokens, obs, core.Settings.Home)
		result.TokensIn += stageResult.TokensIn
		result.TokensOut += stageResult.TokensOut
		result.ToolCalls = append(result.ToolCalls, stageResult.ToolCalls...)
		result.Reply = stageResult.Reply
		result.StagesRun++
		state.EndedAt = time.Now().UTC()
		outputs, verifyErr := verifyWorkspaceStageOutputs(pb, run, stage)
		if stageResult.Status != "complete" || verifyErr != nil {
			state.Status = "failed"
			if stageResult.Status != "complete" {
				state.Error = fmt.Sprintf("runtime %s: %s", stageResult.Status, stageResult.Reply)
			} else {
				state.Error = verifyErr.Error()
			}
			run.Status = "failed"
			_ = savePlaybookRun(pb, run)
			result.Status = "failed"
			result.Reply = fmt.Sprintf("Run %s stopped at stage %02d-%s: %s", run.ID, stage.Number, stage.Name, state.Error)
			return result, nil
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

func verifyWorkspaceStageOutputs(pb *PlaybookWorkspace, run *PlaybookRun, stage WorkspaceStage) ([]string, error) {
	outputs := make([]string, 0, len(stage.Outputs))
	for _, output := range stage.Outputs {
		path := playbookRunOutputPath(pb, run, stage, output)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Size() == 0 {
			return nil, fmt.Errorf("required output %q was not written", output.Path)
		}
		outputs = append(outputs, path)
	}
	return outputs, nil
}
