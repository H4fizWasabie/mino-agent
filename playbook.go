package main

// Playbook — numbered markdown stages executed by the runner.
// ~/.mino/playbooks/<name>/config.md + 01-<verb>.md + 02-<verb>.md + ...
// The filesystem is the executor. The runner is a for loop.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxStageRetries = 3
const maxStageIterations = 10 // bounded runtime iterations within one stage attempt

// stageNumberRe finds an explicit stage number in freeform stage files
// ("# Stage 3: Report") when the filename carries no NN- prefix.
var stageNumberRe = regexp.MustCompile(`(?m)^#+\s*Stage\s+(\d+)`)

// StageFile is one numbered stage in a playbook.
type StageFile struct {
	Number int
	Name   string // derived from filename: "fetch", "analyze", etc.
	Path   string // absolute path
	Raw    string // full file content
	Reads  []string
	Dos    []string
	Tools  []string // optional capability set; order remains LLM-controlled
	Write  string   // relative to playbook output/ dir
}

// Playbook is a loaded playbook ready to execute.
type Playbook struct {
	Name        string
	Dir         string
	Description string
	Schedule    string
	Status      string
	Config      map[string]string
	Stages      []StageFile
}

// PlaybookResult is the outcome of running a playbook.
type PlaybookResult struct {
	Name      string
	StagesRun int
	Status    string // "complete", "blocked", "failed"
	Reply     string
	Outputs   []string
	ToolCalls []ToolCall
	TokensIn  int
	TokensOut int
}

// --- Parser ---

// LoadPlaybook reads a playbook folder and returns the parsed stages.
func LoadPlaybook(playbooksDir, name string) (*Playbook, error) {
	dir := filepath.Join(playbooksDir, name)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("playbook not found: %s", name)
	}

	pb := &Playbook{
		Name:   name,
		Dir:    dir,
		Config: make(map[string]string),
	}

	// config.md (optional)
	if data, err := os.ReadFile(filepath.Join(dir, "config.md")); err == nil {
		parseConfig(data, pb)
	}

	// find stage files: numbered NN-*.md at top level, plus any *.md under
	// stages/. README.md, PLAYBOOK_PROTOCOL.md, config.md etc. are not stages.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var stageFiles []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if len(e.Name()) >= 2 && e.Name()[0] >= '0' && e.Name()[0] <= '9' && e.Name()[1] >= '0' && e.Name()[1] <= '9' {
			stageFiles = append(stageFiles, e.Name())
		}
	}
	if subEntries, err := os.ReadDir(filepath.Join(dir, "stages")); err == nil {
		for _, e := range subEntries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				stageFiles = append(stageFiles, filepath.Join("stages", e.Name()))
			}
		}
	}

	// absolute output paths may only point under the home dir that owns the
	// playbooks dir (<home>/.mino/playbooks); anything else fails fast at load
	// instead of failing verification after 3 LLM attempts.
	home := filepath.Dir(filepath.Dir(playbooksDir))

	for _, fname := range stageFiles {
		stage, err := parseStage(dir, fname)
		if err != nil {
			slog.Warn("playbook stage parse error", "file", fname, "error", err)
			continue
		}
		if filepath.IsAbs(stage.Write) {
			rel, relErr := filepath.Rel(home, stage.Write)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil, fmt.Errorf("playbook %s: stage %s output path %q is outside the allowed root %q", name, stage.Name, stage.Write, home)
			}
		}
		pb.Stages = append(pb.Stages, stage)
	}

	if len(pb.Stages) == 0 {
		return nil, fmt.Errorf("no stage files in playbook: %s", name)
	}

	// stages/ files carry no filename number; their declared "# Stage N"
	// heading (or the filename) decides execution order.
	sort.SliceStable(pb.Stages, func(i, j int) bool {
		if pb.Stages[i].Number != pb.Stages[j].Number {
			return pb.Stages[i].Number < pb.Stages[j].Number
		}
		return pb.Stages[i].Name < pb.Stages[j].Name
	})

	return pb, nil
}

func parseConfig(data []byte, pb *Playbook) {
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "description":
			pb.Description = val
		case "schedule":
			pb.Schedule = val
		case "status":
			pb.Status = val
		default:
			pb.Config[key] = val
		}
	}
}

func parseStage(dir, fname string) (StageFile, error) {
	data, err := os.ReadFile(filepath.Join(dir, fname))
	if err != nil {
		return StageFile{}, err
	}
	raw := string(data)

	// extract number: "01-fetch.md" → 1; fall back to a declared
	// "# Stage N" heading in freeform files ("stages/search.md").
	num := 0
	base := filepath.Base(fname)
	if len(base) >= 2 && base[0] >= '0' && base[0] <= '9' && base[1] >= '0' && base[1] <= '9' {
		fmt.Sscanf(base[:2], "%d", &num)
	} else if m := stageNumberRe.FindStringSubmatch(raw); m != nil {
		fmt.Sscanf(m[1], "%d", &num)
	}
	// extract name: "01-fetch.md" → "fetch"; "scan-and-trash.md" stays
	name := strings.TrimSuffix(base, ".md")
	for len(name) > 0 && name[0] >= '0' && name[0] <= '9' {
		name = name[1:]
	}
	name = strings.TrimPrefix(name, "-")

	stage := StageFile{
		Number: num,
		Name:   name,
		Path:   filepath.Join(dir, fname),
		Raw:    raw,
	}

	// parse sections: ## Read, ## Do, ## Tools, ## Write
	readSection := extractSection(raw, "## Read")
	doSection := extractSection(raw, "## Do")
	toolsSection := extractSection(raw, "## Tools")
	writeSection := extractSection(raw, "## Write")

	if readSection != "" {
		for _, line := range strings.Split(readSection, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// strip leading "- " or "-" prefix
			line = strings.TrimPrefix(line, "- ")
			line = strings.TrimPrefix(line, "-")
			if line != "" {
				stage.Reads = append(stage.Reads, line)
			}
		}
	}

	if doSection != "" {
		for _, line := range strings.Split(doSection, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// strip numbered prefix like "1. " or "1)"
			for len(line) > 0 && (line[0] >= '0' && line[0] <= '9') {
				line = line[1:]
			}
			line = strings.TrimPrefix(line, ". ")
			line = strings.TrimPrefix(line, ". ")
			line = strings.TrimPrefix(line, ") ")
			if line != "" {
				stage.Dos = append(stage.Dos, line)
			}
		}
	}

	if toolsSection != "" {
		for _, line := range strings.Split(toolsSection, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "- ")
			line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
			// the bullet is "name", optionally followed by prose: take the
			// first token only ("bash (to check existence)" → "bash").
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			tool := fields[0]
			if tool == "None" || tool == "none" {
				continue
			}
			stage.Tools = append(stage.Tools, tool)
		}
	}

	if writeSection != "" {
		// extract path from backticks or raw. Only the FIRST backtick pair is
		// authoritative: later bullets are commentary and must not change the
		// verified output path (the scheduler incident showed an LLM appending
		// a second Write bullet mid-run to move the verification goalpost).
		writeSection = strings.TrimSpace(writeSection)
		// handle `output/filename.md` format
		if strings.Contains(writeSection, "`") {
			start := strings.Index(writeSection, "`")
			rest := writeSection[start+1:]
			if end := strings.Index(rest, "`"); end > 0 {
				stage.Write = rest[:end]
			}
		} else {
			stage.Write = strings.SplitN(writeSection, "\n", 2)[0]
		}
		stage.Write = strings.TrimSpace(stage.Write)
	}

	return stage, nil
}

// extractSection returns content between a heading and the next heading or EOF.
func extractSection(raw, heading string) string {
	idx := strings.Index(raw, heading)
	if idx == -1 {
		return ""
	}
	start := idx + len(heading)
	rest := raw[start:]

	// find next heading (## or #)
	end := len(rest)
	for i, c := range rest {
		if c == '#' && i+1 < len(rest) && rest[i+1] == '#' {
			end = i
			break
		}
		// lone # heading
		if c == '#' && (i == 0 || rest[i-1] == '\n') {
			if i+1 >= len(rest) || rest[i+1] != '#' {
				end = i
				break
			}
		}
	}
	return strings.TrimSpace(rest[:end])
}

// --- Builder ---

// buildStagePrompt creates the user message for executing a stage.
// loc expands date templates (YYYY-MM-DD) in the same zone the verifier uses.
func buildStagePrompt(pb *Playbook, stage StageFile, userMessage string, loc ...*time.Location) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("You are executing the **%s** playbook, stage %d: **%s**.\n\n", pb.Name, stage.Number, stage.Name))
	if userMessage != "" {
		b.WriteString("## User Request\n\n")
		b.WriteString(userMessage)
		b.WriteString("\n\n")
	}

	if stage.Raw != "" {
		b.WriteString("## Stage Instructions\n\n")
		b.WriteString(stage.Raw)
		b.WriteString("\n\n")
	}

	outPath := outputPath(pb, stage, loc...)
	b.WriteString("## Output File\n\n")
	b.WriteString(fmt.Sprintf("Write your final output to this exact path: `%s`\n", outPath))
	b.WriteString(fmt.Sprintf("The output directory already exists: `%s`\n\n", filepath.Dir(outPath)))

	// Anchor the playbook dir: relative paths like `output/02-summary.md` in
	// ## Read/## Do sections resolve against it. Without this the stage LLM
	// guesses the base dir and fails to find sibling stage outputs.
	b.WriteString("## Playbook Context\n\n")
	b.WriteString(fmt.Sprintf("The playbook directory is `%s`. Relative paths such as `output/...` are relative to it.\n\n", pb.Dir))

	b.WriteString("## Rules\n\n")
	b.WriteString("- Follow the instructions above. Work step by step.\n")
	b.WriteString("- If you encounter an error, try to fix it. If you cannot fix it after a reasonable attempt, write the error to the output file and stop.\n")
	b.WriteString("- When all steps are complete, write to the output file path above and say DONE.\n")
	b.WriteString("- Do NOT call a tool and then narrate what you'll do next. Just do it.\n")
	b.WriteString("- Do not repeat successful tool calls.\n")
	if len(stage.Tools) > 0 {
		b.WriteString("- You may use the following tools as needed; the order is your judgment, not a fixed script: ")
		b.WriteString(strings.Join(stage.Tools, ", "))
		b.WriteString(".\n")
	}

	return b.String()
}

// outputPath returns the absolute path for a stage's output file.
// Absolute declared paths are honored as-is (validated at load time); relative
// ones are rebased into the playbook output/ dir. Date templates (YYYY-MM-DD)
// expand in loc (nil → local time).
func outputPath(pb *Playbook, stage StageFile, loc ...*time.Location) string {
	zone := time.Local
	if len(loc) > 0 && loc[0] != nil {
		zone = loc[0]
	}
	outputDir := filepath.Join(pb.Dir, "output")
	if stage.Write != "" {
		expanded := strings.ReplaceAll(stage.Write, "YYYY-MM-DD", time.Now().In(zone).Format("2006-01-02"))
		if filepath.IsAbs(expanded) {
			return expanded
		}
		os.MkdirAll(outputDir, 0700)
		return filepath.Join(outputDir, filepath.Base(expanded))
	}
	os.MkdirAll(outputDir, 0700)
	return filepath.Join(outputDir, fmt.Sprintf("%02d-%s.md", stage.Number, stage.Name))
}

type outputState struct {
	exists   bool
	size     int64
	modified time.Time
}

func inspectOutput(path string) outputState {
	info, err := os.Stat(path)
	if err != nil {
		return outputState{}
	}
	return outputState{exists: true, size: info.Size(), modified: info.ModTime()}
}

func (after outputState) changedSince(before outputState) bool {
	return after.exists && (!before.exists || after.size != before.size || !after.modified.Equal(before.modified))
}

// --- Stage executor ---

// executeStage runs one stage with up to maxStageRetries attempts.
// Each attempt: feed stage to LLM, let it call tools, repeat until done.
// Returns the LLM's final reply and any error.
func executeStage(
	ctx context.Context,
	client LLMClient,
	sessionID string,
	system string,
	pb *Playbook,
	stage StageFile,
	userMessage string,
	baseMessages []Message,
	tools *Registry,
	maxTokens int,
	obs Observer,
	traceHome string,
	loc ...*time.Location,
) (string, []ToolCall, int, int, error) {
	userMsg := buildStagePrompt(pb, stage, userMessage, loc...)
	tokensIn, tokensOut := 0, 0
	var allCalls []ToolCall

	for attempt := 1; attempt <= maxStageRetries; attempt++ {
		outPath := outputPath(pb, stage, loc...)
		before := inspectOutput(outPath)
		messages := append([]Message(nil), baseMessages...)
		messages = append(messages, Message{Role: "user", Content: userMsg})

		stageTools := tools.Static()
		if len(stage.Tools) > 0 {
			names := stage.Tools
			hasReadFile := false
			for _, n := range names {
				if n == "read_file" {
					hasReadFile = true
					break
				}
			}
			if !hasReadFile {
				names = append(names, "read_file")
			}
			stageTools = tools.Only(names...)
		}
		stageResult := RunLoopContext(ctx, client, sessionID, system, messages, stageTools,
			maxStageIterations, maxTokens, obs, true, traceHome, nil)
		tokensIn += stageResult.TokensIn
		tokensOut += stageResult.TokensOut
		allCalls = append(allCalls, stageResult.ToolCalls...)

		freshOutput := inspectOutput(outPath).changedSince(before)
		if freshOutput && (stageResult.Status == "complete" || stageResult.Status == "iteration_limit") {
			slog.Info("playbook stage complete", "playbook", pb.Name, "stage", stage.Number, "attempt", attempt, "runtime_status", stageResult.Status)
			if stageResult.Status == "iteration_limit" {
				return "", allCalls, tokensIn, tokensOut, nil
			}
			return stageResult.Reply, allCalls, tokensIn, tokensOut, nil
		}

		if stageResult.Status != "complete" {
			return stageResult.Reply, allCalls, tokensIn, tokensOut, fmt.Errorf("stage runtime %s: %s", stageResult.Status, stageResult.Reply)
		}

		// A completed model turn without a fresh output does not satisfy the stage.
		slog.Warn("playbook stage output unchanged", "playbook", pb.Name, "stage", stage.Number, "attempt", attempt, "expected", outPath)
		if attempt < maxStageRetries {
			userMsg = buildStagePrompt(pb, stage, userMessage, loc...) + fmt.Sprintf("\n## Retry\n\nThe output file `%s` was not created or updated by the previous attempt. Complete the remaining steps and write the output file. Do NOT repeat steps that already succeeded.\n", outPath)
		}
	}

	return "", allCalls, tokensIn, tokensOut, fmt.Errorf("stage %d failed after %d attempts: output not created or updated", stage.Number, maxStageRetries)
}

// --- Runner ---

// RunPlaybook loads and executes a playbook by name.
// Uses Core's existing client, tools, and session infrastructure.
func RunPlaybook(
	ctx context.Context,
	core *Core,
	name string,
	userMessage string,
	sessionID string,
	obs Observer,
) (*PlaybookResult, error) {
	playbooksDir := filepath.Join(core.Settings.Home, "playbooks")
	pb, err := LoadPlaybook(playbooksDir, name)
	if err != nil {
		return nil, err
	}

	slog.Info("playbook started", "name", name, "stages", len(pb.Stages))
	logTrace(core.Settings.Home, "playbook_start", map[string]any{"name": name, "stages": len(pb.Stages)})

	result := &PlaybookResult{Name: name}
	conversation := core.Sessions.Get(sessionID)
	system := conversation.Session.BuildPlaybookSystem(userMessage, "")
	system = appendSystemTime(system, time.Now(), core.Settings.Location())
	baseMessages := conversation.Session.PlaybookContext(system)

	for i, stage := range pb.Stages {
		slog.Info("playbook stage executing", "playbook", name, "stage", stage.Number, "name", stage.Name)

		reply, calls, ti, to, err := executeStage(
			ctx, core.Client, sessionID, system,
			pb, stage, userMessage, baseMessages, core.Tools, core.Settings.MaxTokens,
			obs, core.Settings.Home, core.Settings.Location(),
		)
		result.TokensIn += ti
		result.TokensOut += to
		result.ToolCalls = append(result.ToolCalls, calls...)
		result.Reply = reply
		result.StagesRun = i + 1

		if err != nil {
			result.Status = "failed"
			result.Reply = fmt.Sprintf("Stage %d (%s) failed: %v", stage.Number, stage.Name, err)
			slog.Error("playbook stage failed", "playbook", name, "stage", stage.Number, "error", err)
			logTrace(core.Settings.Home, "playbook_stage_failed", map[string]any{"stage": stage.Number, "error": err.Error()})
			return result, nil // not an error — playbook result carries the failure
		}

		outPath := outputPath(pb, stage, core.Settings.Location())
		result.Outputs = append(result.Outputs, outPath)
		if core.Memory != nil {
			if info, err := os.Stat(outPath); err == nil {
				core.Memory.RecordArtifact(sessionID, fmt.Sprintf("%s stage %d output", name, stage.Number), outPath, int(info.Size()))
			}
		}

		// check if stage wants human input ("Stop here. Ask Abah.")
		if strings.Contains(strings.ToLower(reply), "stop here") ||
			strings.Contains(strings.ToLower(reply), "ask abah") {
			result.Status = "blocked"
			logTrace(core.Settings.Home, "playbook_blocked", map[string]any{"stage": stage.Number})
			return result, nil
		}
	}

	result.Status = "complete"
	logTrace(core.Settings.Home, "playbook_complete", map[string]any{"name": name, "stages": result.StagesRun})
	return result, nil
}

func appendSystemTime(system string, now time.Time, location *time.Location) string {
	local := now.In(location)
	zone, offset := local.Zone()
	return system + fmt.Sprintf("\n[System time: %s %s (UTC%+03d:%02d). Today is %s.]",
		local.Format("Monday, 2006-01-02 15:04:05"), zone, offset/3600, (abs(offset)%3600)/60,
		local.Format("2006-01-02"))
}

// --- Playbook discovery ---

// ListPlaybooks returns all available playbook names.
func ListPlaybooks(home string) []string {
	dir := filepath.Join(home, "playbooks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && e.Name()[0] != '.' {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// MatchPlaybook finds the best playbook for a prompt using embeddings.
// Returns name, description, and score. Falls back to keyword match.
func MatchPlaybook(home, prompt string, es *EmbeddingStore) (string, string, float64) {
	playbooks := ListPlaybooks(home)
	if len(playbooks) == 0 {
		return "", "", 0
	}

	// if no embedding store, return best match by keyword overlap
	if es == nil {
		promptLower := strings.ToLower(prompt)
		promptWords := make(map[string]bool)
		for _, w := range strings.Fields(promptLower) {
			if len(w) >= 3 {
				promptWords[w] = true
			}
		}
		bestName, bestDesc, bestScore := "", "", 0.0
		for _, name := range playbooks {
			pb, err := LoadPlaybook(filepath.Join(home, "playbooks"), name)
			if err != nil {
				continue
			}
			// search description + name + all stage content
			searchText := strings.ToLower(pb.Description + " " + name)
			for _, s := range pb.Stages {
				searchText += " " + strings.ToLower(s.Raw)
			}
			textWords := make(map[string]bool)
			for _, w := range strings.Fields(searchText) {
				if len(w) >= 3 {
					textWords[w] = true
				}
			}
			// count overlapping words
			overlap := 0
			for w := range promptWords {
				if textWords[w] {
					overlap++
				}
			}
			// also check if prompt contains playbook name directly
			if strings.Contains(promptLower, strings.ToLower(name)) {
				overlap += 2 // boost for direct name match
			}
			// substring check: does any prompt word appear as substring in searchText?
			for w := range promptWords {
				if strings.Contains(searchText, w) {
					overlap++
				}
			}
			if overlap > 0 && float64(overlap) > bestScore {
				bestName, bestDesc, bestScore = name, pb.Description, float64(overlap)
			}
		}
		if bestName != "" {
			// Score reflects match strength: 0.3 = weak (hint), 0.5+ = strong (auto-run)
			score := math.Min(1.0, bestScore/10.0)
			if score < 0.3 {
				score = 0.3 // minimum for any match
			}
			return bestName, bestDesc, score
		}
		return "", "", 0
	}

	// embed prompt and compare against playbook descriptions
	promptEmb, err := es.Embed(prompt)
	if err != nil {
		return "", "", 0
	}

	type candidate struct {
		name  string
		desc  string
		score float64
	}
	var candidates []candidate

	for _, name := range playbooks {
		pb, err := LoadPlaybook(filepath.Join(home, "playbooks"), name)
		if err != nil {
			continue
		}
		descEmb, err := es.Embed(pb.Description)
		if err != nil {
			continue
		}
		score := cosineSimilarity(promptEmb, descEmb)
		if score > 0.3 {
			candidates = append(candidates, candidate{name: name, desc: pb.Description, score: score})
		}
	}

	if len(candidates) == 0 {
		return "", "", 0
	}

	// return best match
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.score > best.score {
			best = c
		}
	}
	return best.name, best.desc, best.score
}

// CreateExamplePlaybook scaffolds a minimal playbook for testing.
func CreateExamplePlaybook(home string) error {
	dir := filepath.Join(home, "playbooks", "hello-world")
	os.MkdirAll(filepath.Join(dir, "output"), 0700)

	config := `description: A simple hello-world playbook that demonstrates the stage system
schedule: every 24h
status: active
`
	if err := os.WriteFile(filepath.Join(dir, "config.md"), []byte(config), 0644); err != nil {
		return err
	}

	stage1 := `# Write a greeting

## Read

(no previous stage — this is stage 1)

## Do

1. Write a friendly greeting message with today's date
2. Include a random fun fact

## Write

` + "`output/01-greeting.md`" + `
`
	if err := os.WriteFile(filepath.Join(dir, "01-greet.md"), []byte(stage1), 0644); err != nil {
		return err
	}

	stage2 := `# Read and respond

## Read

- ` + "`output/01-greeting.md`" + ` (the greeting from stage 1)

## Do

1. Read the greeting file
2. Respond to the user with the greeting content
3. Say "Hello from Mino playbooks!"

## Write

` + "`output/02-response.md`" + `
`
	if err := os.WriteFile(filepath.Join(dir, "02-respond.md"), []byte(stage2), 0644); err != nil {
		return err
	}

	slog.Info("example playbook created", "path", dir)
	return nil
}

// --- Tool ---

// makeRunPlaybookTool creates the run_playbook tool.
// When the LLM calls this, the playbook runner executes the stages.
func makeRunPlaybookTool(core *Core) *Tool {
	return &Tool{
		Name:        "run_playbook",
		Description: "Execute a playbook by name. Use when the user asks to run a specific workflow or task. If unsure which playbook, call list_playbooks first.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string", "description": "The playbook name (folder name under ~/.mino/playbooks/)"},
			},
			"required": []string{"name"},
		},
		ContextFn: func(ctx context.Context, args map[string]any) string {
			name, _ := args["name"].(string)
			if name == "" {
				return "Error: playbook name is required"
			}
			sid := ""
			if v := ctx.Value(sessionIDKey{}); v != nil {
				sid, _ = v.(string)
			}
			request, _ := ctx.Value(userMessageKey{}).(string)
			output, err := runPlaybookWithResponsibility(ctx, core, name, request, sid, RunPlaybook, time.Now().UTC())
			if err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			return output
		},
	}
}

// makeListPlaybooksTool creates the list_playbooks tool.
func makeListPlaybooksTool(home string) *Tool {
	return &Tool{
		Name:        "list_playbooks",
		Description: "List all available playbooks with their descriptions.",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Fn: func(args map[string]any) string {
			names := ListPlaybooks(home)
			if len(names) == 0 {
				return "No playbooks found. Create one in ~/.mino/playbooks/<name>/"
			}
			playbooksDir := filepath.Join(home, "playbooks")
			var b strings.Builder
			b.WriteString("Available playbooks:\n")
			for _, name := range names {
				pb, err := LoadPlaybook(playbooksDir, name)
				if err != nil {
					fmt.Fprintf(&b, "- %s (error: %v)\n", name, err)
					continue
				}
				desc := pb.Description
				if desc == "" {
					desc = "(no description)"
				}
				fmt.Fprintf(&b, "- **%s**: %s (%d stages)\n", name, desc, len(pb.Stages))
			}
			return b.String()
		},
	}
}

// PlaybookSchedule is one scheduled playbook entry in ~/.mino/schedules.json.
type PlaybookSchedule struct {
	Name      string `json:"name"`
	Time      string `json:"time"`                 // HH:MM local time
	Timezone  string `json:"timezone"`             // IANA timezone
	LastRun   string `json:"last_run"`             // RFC3339 of last execution, empty if never
	LastError string `json:"last_error,omitempty"` // last fire failure, empty when healthy
}

func scheduleFilePath(home string) string { return filepath.Join(home, "schedules.json") }

func loadSchedules(home string) ([]PlaybookSchedule, error) {
	path := scheduleFilePath(home)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var scheds []PlaybookSchedule
	if err := json.Unmarshal(data, &scheds); err != nil {
		return nil, err
	}
	return scheds, nil
}

func saveSchedules(home string, scheds []PlaybookSchedule) error {
	path := scheduleFilePath(home)
	if len(scheds) == 0 {
		os.Remove(path)
		return nil
	}
	data, err := json.MarshalIndent(scheds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func makeSchedulePlaybookTool(home, timezone string) *Tool {
	return &Tool{
		Name:        "schedule_playbook",
		Description: "Schedule an existing playbook to run daily at a local time. Mino's in-process scheduler will execute it and the output will be visible in the dashboard under the scheduled-<name> session.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":     map[string]any{"type": "string", "description": "Existing playbook folder name"},
				"time":     map[string]any{"type": "string", "description": "Daily local time in HH:MM format"},
				"timezone": map[string]any{"type": "string", "description": "IANA timezone, defaulting to Mino's configured timezone"},
			},
			"required": []string{"name", "time"},
		},
		ContextFn: func(ctx context.Context, args map[string]any) string {
			name, _ := args["name"].(string)
			at, _ := args["time"].(string)
			zone, _ := args["timezone"].(string)
			if zone == "" {
				zone = timezone
			}
			if filepath.Base(name) != name || name == "." || name == ".." {
				return "Error: invalid playbook name"
			}
			if !regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`).MatchString(at) {
				return "Error: time must use HH:MM format"
			}
			if _, err := time.LoadLocation(zone); err != nil {
				return fmt.Sprintf("Error: invalid timezone %q", zone)
			}
			if _, err := LoadPlaybook(filepath.Join(home, "playbooks"), name); err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			scheds, err := loadSchedules(home)
			if err != nil {
				return fmt.Sprintf("Error reading schedules: %v", err)
			}
			// update or append
			found := false
			for i, s := range scheds {
				if s.Name == name {
					scheds[i].Time = at
					scheds[i].Timezone = zone
					found = true
					break
				}
			}
			if !found {
				scheds = append(scheds, PlaybookSchedule{Name: name, Time: at, Timezone: zone})
			}
			if err := saveSchedules(home, scheds); err != nil {
				return fmt.Sprintf("Error saving schedule: %v", err)
			}
			return fmt.Sprintf("Scheduled %s daily at %s (%s). Output will appear in the dashboard under session scheduled-%s.", name, at, zone, name)
		},
	}
}

func makeListSchedulesTool(home string) *Tool {
	return &Tool{
		Name:        "list_schedules",
		Description: "List all scheduled playbook runs.",
		Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
		ContextFn: func(ctx context.Context, args map[string]any) string {
			scheds, err := loadSchedules(home)
			if err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			if len(scheds) == 0 {
				return "No scheduled playbooks."
			}
			var b strings.Builder
			b.WriteString("Scheduled playbooks:\n")
			for _, s := range scheds {
				last := "never"
				if s.LastRun != "" {
					last = s.LastRun
				}
				if s.LastError != "" {
					b.WriteString(fmt.Sprintf("- %s: daily at %s %s (last run: %s) ⚠ last fire FAILED: %s\n", s.Name, s.Time, s.Timezone, last, s.LastError))
				} else {
					b.WriteString(fmt.Sprintf("- %s: daily at %s %s (last run: %s)\n", s.Name, s.Time, s.Timezone, last))
				}
			}
			return b.String()
		},
	}
}

func makeCancelScheduleTool(home string) *Tool {
	return &Tool{
		Name:        "cancel_schedule",
		Description: "Cancel a scheduled playbook by name.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string", "description": "Playbook name to unschedule"},
			},
			"required": []string{"name"},
		},
		ContextFn: func(ctx context.Context, args map[string]any) string {
			name, _ := args["name"].(string)
			scheds, err := loadSchedules(home)
			if err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			found := false
			filtered := make([]PlaybookSchedule, 0, len(scheds))
			for _, s := range scheds {
				if s.Name == name {
					found = true
					continue
				}
				filtered = append(filtered, s)
			}
			if !found {
				return fmt.Sprintf("No schedule found for '%s'.", name)
			}
			if err := saveSchedules(home, filtered); err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			return fmt.Sprintf("Cancelled schedule for %s. %d schedule(s) remain.", name, len(filtered))
		},
	}
}

func makeSystemCheckTool(db *sql.DB, home string) *Tool {
	return &Tool{
		Name:        "system_check",
		Description: "Inspect schedules, reminders, playbooks, and the user crontab so state-changing work can be verified before replying.",
		Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
		Behavior:    BehaviorObserve,
		Fn: func(map[string]any) string {
			schedules, scheduleErr := loadSchedules(home)
			playbooks := ListPlaybooks(home)
			pending := 0
			if db != nil {
				if err := db.QueryRow("SELECT COUNT(*) FROM reminders WHERE status = 'pending'").Scan(&pending); err != nil {
					return fmt.Sprintf("Error checking reminders: %v", err)
				}
			}
			cron := "unavailable"
			if out, err := exec.Command("crontab", "-l").Output(); err == nil {
				cron = strings.TrimSpace(string(out))
				if cron == "" {
					cron = "empty"
				}
			}
			var b strings.Builder
			if scheduleErr != nil {
				fmt.Fprintf(&b, "schedules: error (%v)\n", scheduleErr)
			} else {
				fmt.Fprintf(&b, "schedules: %d\n", len(schedules))
				for _, s := range schedules {
					if s.LastError != "" {
						fmt.Fprintf(&b, "  - %s: last fire FAILED — %s\n", s.Name, s.LastError)
					}
				}
			}
			// runtime truth: systemd service state and recent errors from the real
			// log. journald held the exact error that broke every schedule; the LLM
			// never looked there because nothing told it journald is its log.
			out, err := exec.Command("systemctl", "is-active", "mino").Output()
			svc := strings.TrimSpace(string(out))
			if err != nil && svc == "" {
				svc = "not-a-systemd-service"
			}
			fmt.Fprintf(&b, "service: mino=%s\n", svc)
			if out, err := exec.Command("journalctl", "-u", "mino", "-p", "err", "--since", "1 hour ago", "-n", "10", "--no-pager").Output(); err == nil && len(out) > 0 {
				fmt.Fprintf(&b, "recent_errors:\n%s", out)
			} else {
				b.WriteString("recent_errors: none\n")
			}
			fmt.Fprintf(&b, "pending_reminders: %d\nplaybooks: %d\ncrontab: %s", pending, len(playbooks), cron)
			return b.String()
		},
	}
}

func formatPlaybookResult(result *PlaybookResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Playbook: %s — %s\n\n", result.Name, result.Status))
	b.WriteString(fmt.Sprintf("Stages completed: %d\n", result.StagesRun))
	b.WriteString(fmt.Sprintf("Tokens used: %d in / %d out\n", result.TokensIn, result.TokensOut))
	if len(result.Outputs) > 0 {
		b.WriteString("Outputs:\n")
		for _, output := range result.Outputs {
			b.WriteString("- " + output + "\n")
		}
	}
	if result.Reply != "" {
		b.WriteString("\n")
		b.WriteString(result.Reply)
	}
	return b.String()
}

// listActiveTasksPlaybook returns active playbook runs for the dashboard.
// Replaces the old checkpoint-based ListActiveTasks.
func listActiveTasksPlaybook(home string) []map[string]any {
	playbooks := ListPlaybooks(home)
	var tasks []map[string]any
	playbooksDir := filepath.Join(home, "playbooks")
	for _, name := range playbooks {
		pb, err := LoadPlaybook(playbooksDir, name)
		if err != nil || pb.Status != "active" {
			continue
		}
		// check if there's in-progress output
		outputDir := filepath.Join(pb.Dir, "output")
		entries, _ := os.ReadDir(outputDir)
		hasOutput := len(entries) > 0
		tasks = append(tasks, map[string]any{
			"goal":       pb.Description,
			"status":     pb.Status,
			"stages":     len(pb.Stages),
			"has_output": hasOutput,
			"playbook":   name,
		})
	}
	return tasks
}

// runScheduleDispatcher checks schedules.json every minute and fires due playbooks.
func runScheduleDispatcher(core *Core) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		dispatchDueSchedules(core)
	}
}

func dispatchDueSchedules(core *Core) {
	dispatchDueSchedulesAt(core, time.Now(), RunPlaybook)
}

type scheduledPlaybookRunner func(context.Context, *Core, string, string, string, Observer) (*PlaybookResult, error)

func dispatchDueSchedulesAt(core *Core, now time.Time, run scheduledPlaybookRunner) {
	scheds, err := loadSchedules(core.Settings.Home)
	if err != nil || len(scheds) == 0 {
		return
	}
	updated := false
	for i, s := range scheds {
		loc, err := time.LoadLocation(s.Timezone)
		if err != nil {
			continue
		}
		// parse scheduled time in the target timezone
		schedTime, err := time.ParseInLocation("15:04", s.Time, loc)
		if err != nil {
			continue
		}
		// rebase to today in that timezone
		nowInLoc := now.In(loc)
		today := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), schedTime.Hour(), schedTime.Minute(), 0, 0, loc)
		// schedule window: current time is within [scheduled, scheduled+1min)
		if nowInLoc.Before(today) || nowInLoc.After(today.Add(time.Minute)) {
			continue
		}
		// already ran today?
		if s.LastRun != "" {
			last, err := time.Parse(time.RFC3339, s.LastRun)
			if err == nil {
				lastInLoc := last.In(loc)
				if lastInLoc.Year() == today.Year() && lastInLoc.YearDay() == today.YearDay() {
					continue
				}
			}
		}
		slog.Info("schedule firing playbook", "name", s.Name, "time", s.Time)
		sessionID := "scheduled-" + s.Name
		if err := core.Responsibilities.startRoutine(s, now); err != nil {
			slog.Error("schedule responsibility start failed", "name", s.Name, "error", err)
			// Never fail silently: the incident that broke every schedule was
			// exactly this error landing only in journald. Surface it in the
			// trace, the audit log, and schedules.json so the LLM and the user
			// can both see it.
			logTrace(core.Settings.Home, "schedule_fire_failed", map[string]any{"name": s.Name, "time": s.Time, "error": err.Error()})
			core.auditLog(sessionID, "schedule_fire_failed", err.Error(), 0)
			scheds[i].LastError = err.Error()
			updated = true
			continue
		}
		scheds[i].LastError = ""
		result, err := run(context.Background(), core, s.Name, "Scheduled run", sessionID, nil)
		if err != nil {
			slog.Error("schedule playbook failed", "name", s.Name, "error", err)
		}
		if result != nil {
			slog.Info("schedule playbook result", "name", s.Name, "status", result.Status, "stages", result.StagesRun)
		}
		finishedAt := time.Now().UTC()
		if recordErr := core.Responsibilities.finishRoutine(core.Settings.Home, sessionID, s, result, err, finishedAt); recordErr != nil {
			slog.Error("schedule responsibility finish failed", "name", s.Name, "error", recordErr)
		}
		scheds[i].LastRun = finishedAt.Format(time.RFC3339)
		updated = true
	}
	if updated {
		saveSchedules(core.Settings.Home, scheds)
	}
}

// ensure playbook types are compatible with existing interfaces
var _ = time.Now // use time somewhere for future timestamp features
