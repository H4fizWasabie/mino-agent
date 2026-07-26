package main

// Playbook — numbered markdown stages executed by the runner.
// ~/.mino/playbooks/<name>/config.md + 01-<verb>.md + 02-<verb>.md + ...
// The filesystem is the executor. The runner is a for loop.

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxStageRetries = 3
const maxToolRounds = 8 // max tool-call rounds within one stage attempt

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

	// find numbered stage files
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var stageFiles []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if e.Name() == "config.md" {
			continue
		}
		stageFiles = append(stageFiles, e.Name())
	}
	sort.Strings(stageFiles)

	for _, fname := range stageFiles {
		stage, err := parseStage(dir, fname)
		if err != nil {
			slog.Warn("playbook stage parse error", "file", fname, "error", err)
			continue
		}
		pb.Stages = append(pb.Stages, stage)
	}

	if len(pb.Stages) == 0 {
		return nil, fmt.Errorf("no stage files in playbook: %s", name)
	}

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

	// extract number: "01-fetch.md" → 1
	num := 0
	if len(fname) >= 2 && fname[0] >= '0' && fname[0] <= '9' && fname[1] >= '0' && fname[1] <= '9' {
		fmt.Sscanf(fname[:2], "%d", &num)
	}
	// extract name: "01-fetch.md" → "fetch"
	name := strings.TrimSuffix(fname, ".md")
	if idx := strings.Index(name, "-"); idx >= 0 {
		name = name[idx+1:]
	}

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
			if line != "" {
				stage.Tools = append(stage.Tools, line)
			}
		}
	}

	if writeSection != "" {
		// extract path from backticks or raw
		writeSection = strings.TrimSpace(writeSection)
		// handle `output/filename.md` format
		if strings.Contains(writeSection, "`") {
			start := strings.Index(writeSection, "`")
			end := strings.LastIndex(writeSection, "`")
			if start >= 0 && end > start {
				stage.Write = writeSection[start+1 : end]
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
func buildStagePrompt(pb *Playbook, stage StageFile, userMessage string) string {
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

	outPath := outputPath(pb, stage)
	b.WriteString("## Output File\n\n")
	b.WriteString(fmt.Sprintf("Write your final output to this exact path: `%s`\n", outPath))
	b.WriteString(fmt.Sprintf("The output directory already exists: `%s`\n\n", filepath.Dir(outPath)))

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
func outputPath(pb *Playbook, stage StageFile) string {
	outputDir := filepath.Join(pb.Dir, "output")
	os.MkdirAll(outputDir, 0700)
	if stage.Write != "" {
		return filepath.Join(outputDir, filepath.Base(stage.Write))
	}
	return filepath.Join(outputDir, fmt.Sprintf("%02d-%s.md", stage.Number, stage.Name))
}

// --- Mini-loop executor ---

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
	tools *Registry,
	maxTokens int,
	obs Observer,
	traceHome string,
) (string, []ToolCall, int, int, error) {
	userMsg := buildStagePrompt(pb, stage, userMessage)
	tokensIn, tokensOut := 0, 0
	var allCalls []ToolCall

	for attempt := 1; attempt <= maxStageRetries; attempt++ {
		messages := []Message{
			{Role: "system", Content: system},
			{Role: "user", Content: userMsg},
		}

		stageTools := tools
		if len(stage.Tools) > 0 {
			stageTools = tools.Only(stage.Tools...)
		}
		toolCalls, ti, to, done := runToolLoop(ctx, client, sessionID, messages, stageTools, maxTokens, obs, traceHome, attempt)
		tokensIn += ti
		tokensOut += to
		allCalls = append(allCalls, toolCalls...)

		if strings.HasPrefix(done, "BLOCKED:") || done == "max tool rounds reached" {
			return done, allCalls, tokensIn, tokensOut, fmt.Errorf("%s", done)
		}

		// check output file
		outPath := outputPath(pb, stage)
		if _, err := os.Stat(outPath); err == nil {
			slog.Info("playbook stage complete", "playbook", pb.Name, "stage", stage.Number, "attempt", attempt)
			return done, allCalls, tokensIn, tokensOut, nil
		}

		// output file not found — retry
		slog.Warn("playbook stage output missing", "playbook", pb.Name, "stage", stage.Number, "attempt", attempt, "expected", outPath)
		if attempt < maxStageRetries {
			userMsg = buildStagePrompt(pb, stage, userMessage) + fmt.Sprintf("\n## Retry\n\nThe output file `%s` was not created. Complete the remaining steps and write the output file. Do NOT repeat steps that already succeeded.\n", outputPath(pb, stage))
		}
	}

	return "", allCalls, tokensIn, tokensOut, fmt.Errorf("stage %d failed after %d attempts: output not written", stage.Number, maxStageRetries)
}

// runToolLoop feeds the LLM, executes tools, and repeats until the LLM stops.
// Returns tool calls made, token counts, and the final text response.
func runToolLoop(
	ctx context.Context,
	client LLMClient,
	sessionID string,
	messages []Message,
	tools *Registry,
	maxTokens int,
	obs Observer,
	traceHome string,
	attempt int,
) ([]ToolCall, int, int, string) {
	allSchemas := tools.Schemas()
	tokensIn, tokensOut := 0, 0
	var allCalls []ToolCall
	cache := make(map[string]string)
	repeats := make(map[string]int)

	for round := 1; round <= maxToolRounds; round++ {
		if ctx.Err() != nil {
			return allCalls, tokensIn, tokensOut, "cancelled"
		}

		resp, err := client.Stream(sessionID, MainModel, messages, maxTokens, "", allSchemas, func(delta string) {
			notify(obs, "text", map[string]any{"delta": delta})
		})
		if err != nil {
			slog.Warn("playbook LLM error", "round", round, "error", err)
			return allCalls, tokensIn, tokensOut, fmt.Sprintf("LLM error: %v", err)
		}

		tokensIn += resp.Usage.InputTokens
		tokensOut += resp.Usage.OutputTokens

		notify(obs, "llm", map[string]any{
			"stage_attempt": attempt,
			"round":         round,
			"usage":         map[string]int{"in": resp.Usage.InputTokens, "out": resp.Usage.OutputTokens},
		})

		// extract tool uses
		toolUses := extractToolUses(resp.Content)
		if len(toolUses) == 0 {
			toolUses = extractTextToolUses(extractText(resp.Content))
		}

		// append assistant turn
		messages = append(messages, Message{Role: "assistant", Content: assembleAssistantContent(resp.Content)})

		// no tool calls = LLM says it's done
		if len(toolUses) == 0 {
			logTrace(traceHome, "playbook_stage_done", map[string]any{"round": round, "attempt": attempt})
			return allCalls, tokensIn, tokensOut, extractText(resp.Content)
		}

		// execute tools
		toolResults := make([]map[string]any, 0)
		for _, tc := range toolUses {
			args, _ := tc.Input.(map[string]any)
			key := dedupKey(tc.Name, args)
			output, cached := cache[key]
			if cached {
				repeats[key]++
				if repeats[key] >= 2 {
					logTrace(traceHome, "playbook_circuit_breaker", map[string]any{"reason": "repeated tool call", "tool": tc.Name, "round": round})
					return allCalls, tokensIn, tokensOut, fmt.Sprintf("BLOCKED: repeated identical call to %s", tc.Name)
				}
				output = "[already executed] " + output
			} else {
				output = tools.ExecuteContext(ctx, tc.Name, args)
				cache[key] = output
			}
			if ctx.Err() != nil {
				return allCalls, tokensIn, tokensOut, "cancelled"
			}
			status := toolOutputStatus(output)
			output = prepareToolOutput(traceHome, sessionID, round, tc.Name, output)
			output = appendActionReceipt(output, tc.Name, key, status, cached)

			allCalls = append(allCalls, ToolCall{Name: tc.Name, Args: args, Output: output})

			notify(obs, "tool", map[string]any{"tool": tc.Name, "args": args, "status": status})
			logTrace(traceHome, "tool", map[string]any{"tool": tc.Name, "args": args, "status": status})

			toolResults = append(toolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": tc.ID,
				"tool":        tc.Name,
				"status":      status,
				"content":     output,
			})
		}
		messages = append(messages, Message{Role: "user", Content: formatToolResults(toolResults)})
	}

	return allCalls, tokensIn, tokensOut, "max tool rounds reached"
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
	system := loadSoul(core.Settings.Home) + fmt.Sprintf("\nLOCAL WORKSPACE: %s", core.Settings.Workspace)

	for i, stage := range pb.Stages {
		slog.Info("playbook stage executing", "playbook", name, "stage", stage.Number, "name", stage.Name)

		reply, calls, ti, to, err := executeStage(
			ctx, core.Client, sessionID, system,
			pb, stage, userMessage, core.Tools, core.Settings.MaxTokens,
			obs, core.Settings.Home,
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

		// check if stage wants human input ("Stop here. Ask Abah.")
		if strings.Contains(strings.ToLower(reply), "stop here") ||
			strings.Contains(strings.ToLower(reply), "ask abah") ||
			strings.Contains(strings.ToLower(reply), "awaiting approval") {
			result.Status = "blocked"
			logTrace(core.Settings.Home, "playbook_blocked", map[string]any{"stage": stage.Number})
			return result, nil
		}
	}

	result.Status = "complete"
	logTrace(core.Settings.Home, "playbook_complete", map[string]any{"name": name, "stages": result.StagesRun})
	return result, nil
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
			result, err := RunPlaybook(ctx, core, name, request, sid, nil)
			if err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			return formatPlaybookResult(result)
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

func makeSchedulePlaybookTool(home, timezone string) *Tool {
	return &Tool{
		Name:        "schedule_playbook",
		Description: "Schedule an existing playbook daily at a local time. The external systemd dispatcher will run it and deliver its output to Telegram.",
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
			configPath := filepath.Join(home, "playbooks", name, "config.md")
			if _, err := LoadPlaybook(filepath.Join(home, "playbooks"), name); err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			data, err := os.ReadFile(configPath)
			if err != nil {
				return fmt.Sprintf("Error reading config: %v", err)
			}
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			set := map[string]string{"schedule:": fmt.Sprintf("schedule: %s %s", at, zone), "notify:": "notify: true", "status:": "status: active"}
			seen := make(map[string]bool)
			for i, line := range lines {
				key := strings.Fields(line)
				if len(key) == 0 {
					continue
				}
				if replacement, ok := set[key[0]]; ok {
					lines[i], seen[key[0]] = replacement, true
				}
			}
			for _, key := range []string{"schedule:", "notify:", "status:"} {
				if !seen[key] {
					lines = append(lines, set[key])
				}
			}
			tmp := configPath + ".tmp"
			if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
				return fmt.Sprintf("Error writing config: %v", err)
			}
			if err := os.Rename(tmp, configPath); err != nil {
				os.Remove(tmp)
				return fmt.Sprintf("Error installing config: %v", err)
			}
			return fmt.Sprintf("Scheduled %s daily at %s (%s); systemd will deliver the output to Telegram.", name, at, zone)
		},
	}
}

func formatPlaybookResult(result *PlaybookResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Playbook: %s — %s\n\n", result.Name, result.Status))
	b.WriteString(fmt.Sprintf("Stages completed: %d\n", result.StagesRun))
	b.WriteString(fmt.Sprintf("Tokens used: %d in / %d out\n\n", result.TokensIn, result.TokensOut))
	if result.Reply != "" {
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

// ensure playbook types are compatible with existing interfaces
var _ = time.Now // use time somewhere for future timestamp features
