package main

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
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxStageIterations = 10

// maxStageAttempts bounds stage-level retries within a single run. Retry-safe
// stages (read-only whitelist) get at most this many attempts; the failure of
// each attempt is fed back into the stage context.
const maxStageAttempts = 2

// PlaybookResult is the durable outcome returned by a playbook run.
type PlaybookResult struct {
	Name          string
	StagesRun     int
	Status        string
	Reply         string
	Outputs       []string
	ToolCalls     []ToolCall
	TokensIn      int
	TokensOut     int
	SelfCertified bool
}

func RunPlaybook(ctx context.Context, core *Core, name, request, sessionID string, obs Observer) (*PlaybookResult, error) {
	result, err := runWorkspacePlaybook(ctx, core, name, request, sessionID, obs)
	if err != nil {
		return nil, err
	}
	for _, output := range result.Outputs {
		if info, statErr := os.Stat(output); statErr == nil && core.Memory != nil {
			core.Memory.RecordArtifact(sessionID, name+" output", output, int(info.Size()))
		}
	}
	logTrace(core.Settings.Home, "playbook_run", map[string]any{"name": name, "status": result.Status, "stages": result.StagesRun, "self_certified": result.SelfCertified})
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
		if !e.IsDir() || e.Name()[0] == '.' {
			continue
		}
		if _, err := loadPlaybookWorkspace(home, e.Name()); err == nil {
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
			pb, err := loadPlaybookWorkspace(home, name)
			if err != nil {
				continue
			}
			// search description + name + all stage content
			searchText := strings.ToLower(pb.Description + " " + name)
			for _, s := range pb.Stages {
				searchText += " " + strings.ToLower(s.Context)
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
		pb, err := loadPlaybookWorkspace(home, name)
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
	if err := os.MkdirAll(filepath.Join(dir, "stages", "01-greet"), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "stages", "02-respond"), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "CONTEXT.md"), []byte("# Hello world\n\nA minimal autonomous greeting workflow.\n"), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "config.md"), []byte("description: A simple hello-world playbook that demonstrates durable runs\nstatus: active\n"), 0644); err != nil {
		return err
	}
	stage1 := "# Greet\n\n## Inputs\n\n| Source | File/Location | Section/Scope | Why |\n| --- | --- | --- | --- |\n\n## Process\n\n1. Write a friendly greeting with today's date.\n\n## Tools\n\n- write_file\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Greeting | `output/greeting.md` | Markdown |\n"
	if err := os.WriteFile(filepath.Join(dir, "stages", "01-greet", "CONTEXT.md"), []byte(stage1), 0644); err != nil {
		return err
	}
	stage2 := "# Respond\n\n## Inputs\n\n| Source | File/Location | Section/Scope | Why |\n| --- | --- | --- | --- |\n| Previous stage | `../01-greet/output/greeting.md` | Full file | Greeting to deliver |\n\n## Process\n\n1. Read the greeting.\n2. Write a concise response.\n\n## Tools\n\n- write_file\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Response | `output/response.md` | Markdown |\n"
	if err := os.WriteFile(filepath.Join(dir, "stages", "02-respond", "CONTEXT.md"), []byte(stage2), 0644); err != nil {
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
// makeCapturePlaybookTool compiles a playbook from evidence, not improvisation:
// the stage contract's Tools whitelist and Outputs come from the audit log's
// actual successful tool calls in the current session. The model supplies only
// the goal prose (name, context, process). This is the teach → compile flow:
// run the task, succeed, then capture it as a playbook.
func makeCapturePlaybookTool(core *Core) *Tool {
	return &Tool{
		Name:        "capture_playbook",
		Description: "Compile a playbook from the tool calls Mino actually made in the current session. Use after a task succeeded: the stage's Tools whitelist and Outputs are derived from the immutable audit log (real slugs, real paths) instead of being improvised. Supply name, root CONTEXT.md, and the process prose; the tool fills Tools/Outputs from evidence.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":      map[string]any{"type": "string", "description": "Short hyphenated playbook name"},
				"context":   map[string]any{"type": "string", "description": "Root CONTEXT.md content: purpose and routing"},
				"process":   map[string]any{"type": "string", "description": "Stage Process section prose: the steps in order, written in the imperative"},
				"stage":     map[string]any{"type": "string", "description": "Optional stage folder name without number (default: task)"},
				"max_calls": map[string]any{"type": "integer", "description": "Optional: how many recent tool calls to consider (default 60)"},
			},
			"required": []string{"name", "context", "process"},
		},
		ContextFn: func(ctx context.Context, args map[string]any) string {
			name, _ := args["name"].(string)
			context, _ := args["context"].(string)
			process, _ := args["process"].(string)
			stage, _ := args["stage"].(string)
			maxCalls := 60
			if v, ok := args["max_calls"].(float64); ok && v > 0 {
				maxCalls = int(v)
			}
			sid := ""
			if v := ctx.Value(sessionIDKey{}); v != nil {
				sid, _ = v.(string)
			}
			stageContext, err := buildCapturedStage(core, sid, stage, process, maxCalls)
			if err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			return createManagedPlaybook(core, name, map[string]any{
				"context": context,
				"stages":  []any{map[string]any{"name": "01-" + stageOrDefault(stage), "context": stageContext}},
			})
		},
	}
}

func stageOrDefault(stage string) string {
	if strings.TrimSpace(stage) == "" {
		return "task"
	}
	return stage
}

// buildCapturedStage derives a stage contract from the audit log: the Tools
// whitelist is the distinct set of successfully-executed tools in the session's
// current turn, and the Outputs are the write_file paths actually written
// (re-anchored to the run's output dir). Real slugs and real filenames — nothing
// invented, nothing stale from earlier turns.
func buildCapturedStage(core *Core, sessionID, stageName, process string, maxCalls int) (string, error) {
	data, err := os.ReadFile(filepath.Join(core.Settings.Home, "audit.jsonl"))
	if err != nil {
		return "", fmt.Errorf("cannot read audit log for capture: %v", err)
	}
	// Scope to the current turn: only events after the session's most recent
	// user message. Earlier turns in the same session (e.g. an old Gmail task)
	// must not pollute the captured contract with their tools and outputs.
	turnStart := lastUserMessageTime(core.DB, sessionID)
	var calls []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["session_id"] != sessionID || ev["status"] != "ok" {
			continue
		}
		if ts, ok := ev["timestamp"].(string); ok && turnStart != nil {
			if t, err := time.Parse(time.RFC3339, ts); err != nil || t.Before(*turnStart) {
				continue
			}
		}
		calls = append(calls, ev)
	}
	if len(calls) == 0 {
		return "", fmt.Errorf("no successful tool calls found in the audit log for this session's current turn — capture requires a task that actually ran")
	}
	if len(calls) > maxCalls {
		calls = calls[len(calls)-maxCalls:]
	}
	tools := make([]string, 0)
	seenTools := make(map[string]bool)
	outputs := make([]string, 0)
	seenOutputs := make(map[string]bool)
	for _, call := range calls {
		toolName, _ := call["tool_name"].(string)
		if toolName == "" {
			continue
		}
		if !seenTools[toolName] {
			seenTools[toolName] = true
			tools = append(tools, toolName)
		}
		if toolName != "write_file" {
			continue
		}
		if args, ok := call["args"].(map[string]any); ok {
			if path, ok := args["path"].(string); ok && path != "" {
				base := filepath.Base(path)
				if base != "." && base != string(filepath.Separator) && !seenOutputs[base] {
					seenOutputs[base] = true
					outputs = append(outputs, base)
				}
			}
		}
	}
	if len(outputs) == 0 {
		return "", fmt.Errorf("capture found no write_file calls in the audit log — a playbook stage must produce outputs")
	}
	// write_file is mandatory on every stage whitelist
	if !seenTools["write_file"] {
		tools = append(tools, "write_file")
	}
	// Re-anchor absolute paths in the process prose to the run's output dir:
	// stages write into their own run directory, never to captured absolute paths.
	process = reAnchorOutputPaths(process, outputs)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n## Inputs\n\n| Source | File/Location | Section/Scope | Why |\n| --- | --- | --- | --- |\n\n## Process\n\n%s\n\n## Tools\n\n", stageOrDefault(stageName), process)
	for _, tool := range tools {
		fmt.Fprintf(&b, "- %s\n", tool)
	}
	b.WriteString("\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n")
	for _, out := range outputs {
		fmt.Fprintf(&b, "| Result | `output/%s` | Markdown |\n", out)
	}
	return b.String(), nil
}

// lastUserMessageTime returns the timestamp of the session's most recent user
// message, or nil when the database is unavailable. chat_log.created_at is UTC.
func lastUserMessageTime(db *sql.DB, sessionID string) *time.Time {
	if db == nil {
		return nil
	}
	var created string
	err := db.QueryRow("SELECT created_at FROM chat_log WHERE session_id = ? AND role = 'user' ORDER BY id DESC LIMIT 1", sessionID).Scan(&created)
	if err != nil {
		return nil
	}
	// chat_log stores datetime('now') → "2006-01-02 15:04:05" UTC
	t, err := time.Parse("2006-01-02 15:04:05", created)
	if err != nil {
		return nil
	}
	return &t
}

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
			var b strings.Builder
			b.WriteString("Available playbooks:\n")
			for _, name := range names {
				pb, err := loadPlaybookWorkspace(home, name)
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

// manage_playbook owns definition changes so the model need not manipulate
// playbook internals or run state through generic filesystem tools.
func makeManagePlaybookTool(core *Core) *Tool {
	return &Tool{
		Name:        "manage_playbook",
		Description: "Create, inspect, validate, update, or permanently delete a playbook definition. Create requires a root CONTEXT.md and numbered stage contracts. Updates and deletion are refused while a run can resume; deletion is also refused while scheduled.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":        map[string]any{"type": "string", "enum": []string{"create", "inspect", "validate", "update", "delete"}},
				"name":          map[string]any{"type": "string", "description": "Short hyphenated playbook name"},
				"context":       map[string]any{"type": "string", "description": "Root CONTEXT.md content; required to create, optional to update"},
				"config":        map[string]any{"type": "string", "description": "config.md content; optional"},
				"stage_name":    map[string]any{"type": "string", "description": "NN-stage folder to add or update"},
				"stage_context": map[string]any{"type": "string", "description": "Stage CONTEXT.md content used with stage_name"},
				"stages":        map[string]any{"type": "array", "description": "Initial stages for create", "items": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}, "context": map[string]any{"type": "string"}}, "required": []string{"name", "context"}}},
			},
			"required": []string{"action", "name"},
		},
		Fn: func(args map[string]any) string {
			action, _ := args["action"].(string)
			name, _ := args["name"].(string)
			if !validPlaybookName(name) {
				return "Error: name must use lowercase letters, digits, and single hyphens"
			}
			switch action {
			case "create":
				return createManagedPlaybook(core, name, args)
			case "inspect":
				return inspectManagedPlaybook(core.Settings.Home, name)
			case "validate":
				if err := validateManagedPlaybook(core, name); err != nil {
					return fmt.Sprintf("Error: %v", err)
				}
				return fmt.Sprintf("Playbook %s is valid.", name)
			case "update":
				return updateManagedPlaybook(core, name, args)
			case "delete":
				return deleteManagedPlaybook(core, name)
			default:
				return "Error: action must be create, inspect, validate, update, or delete"
			}
		},
	}
}

var playbookNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// reAnchorOutputPaths rewrites captured absolute paths in process prose to the
// run-relative output dir: `/any/dir/report.md` → `output/report.md`. Stages
// write into their own run directory; captured absolute paths would be refused
// by the playbook write guard during execution. Only paths ending in a captured
// output basename are rewritten.
func reAnchorOutputPaths(process string, outputs []string) string {
	for _, out := range outputs {
		quoted := regexp.QuoteMeta(out)
		process = regexp.MustCompile(`(?:\S*/)?` + quoted).ReplaceAllString(process, "output/"+out)
	}
	return process
}

func validPlaybookName(name string) bool { return playbookNamePattern.MatchString(name) }

func validateManagedPlaybook(core *Core, name string) error {
	pb, err := loadPlaybookWorkspace(core.Settings.Home, name)
	if err != nil {
		return err
	}
	return validateWorkspaceStageTools(pb, core.Tools)
}

func createManagedPlaybook(core *Core, name string, args map[string]any) string {
	dir := filepath.Join(core.Settings.Home, "playbooks", name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Sprintf("Error: playbook %s already exists", name)
	}
	context, _ := args["context"].(string)
	stages, ok := args["stages"].([]any)
	if strings.TrimSpace(context) == "" || !ok || len(stages) == 0 {
		return "Error: create requires context and at least one stage"
	}
	if err := os.MkdirAll(filepath.Join(dir, "stages"), 0700); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if err := writePlaybookFile(filepath.Join(dir, "CONTEXT.md"), context); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Sprintf("Error: %v", err)
	}
	config, _ := args["config"].(string)
	if strings.TrimSpace(config) == "" {
		config = "status: active\n"
	}
	if err := writePlaybookFile(filepath.Join(dir, "config.md"), config); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Sprintf("Error: %v", err)
	}
	usedNames := make(map[string]bool)
	for index, item := range stages {
		stage, ok := item.(map[string]any)
		if !ok {
			_ = os.RemoveAll(dir)
			return "Error: each stage must contain name and context"
		}
		stageName, _ := stage["name"].(string)
		stageContext, _ := stage["context"].(string)
		canonicalName, err := canonicalManagedStageName(stageName, index+1)
		if err != nil || strings.TrimSpace(stageContext) == "" || usedNames[canonicalName] {
			_ = os.RemoveAll(dir)
			return "Error: each stage needs a unique name and non-empty context"
		}
		usedNames[canonicalName] = true
		stageContext = canonicalManagedStageContext(stageContext)
		if index > 0 {
			previous, _ := stages[index-1].(map[string]any)
			previousName, _ := previous["name"].(string)
			previousName, _ = canonicalManagedStageName(previousName, index)
			stageContext = canonicalManagedStageInputs(stageContext, previousName)
		}
		if err := writePlaybookFile(filepath.Join(dir, "stages", canonicalName, "CONTEXT.md"), stageContext); err != nil {
			_ = os.RemoveAll(dir)
			return fmt.Sprintf("Error: %v", err)
		}
	}
	if err := validateManagedPlaybook(core, name); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Sprintf("Error: invalid playbook: %v", err)
	}
	return fmt.Sprintf("Created and validated playbook %s.", name)
}

func canonicalManagedStageName(raw string, position int) (string, error) {
	raw = strings.Trim(strings.TrimSpace(raw), "`")
	name, number := raw, position
	parts := strings.SplitN(raw, "-", 2)
	if len(parts) == 2 {
		if parsed, err := strconv.Atoi(parts[0]); err == nil {
			number, name = parsed, parts[1]
		}
	}
	if number < 1 || !validPlaybookName(name) {
		return "", fmt.Errorf("stage %q must use a name like 01-research", raw)
	}
	return fmt.Sprintf("%02d-%s", number, name), nil
}

func canonicalManagedStageContext(raw string) string {
	raw = strings.ReplaceAll(raw, "## Do", "## Process")
	if strings.Contains(raw, "## Outputs") {
		return raw
	}
	section := extractSection(raw, "## Write")
	var path string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "-* "))
		if start := strings.Index(line, "`"); start >= 0 {
			if end := strings.Index(line[start+1:], "`"); end >= 0 {
				path = line[start+1 : start+1+end]
			}
		}
		if path == "" && strings.HasPrefix(line, "output/") {
			path = strings.Fields(line)[0]
		}
		if strings.HasPrefix(path, "output/") {
			break
		}
	}
	if path == "" {
		return raw
	}
	return strings.TrimSpace(raw) + fmt.Sprintf("\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Stage output | `%s` | Markdown |\n", path)
}

func canonicalManagedStageInputs(raw, previous string) string {
	if previous == "" {
		return raw
	}
	section := extractSection(raw, "## Read")
	if section == "" {
		return raw
	}
	updated := strings.ReplaceAll(section, "`output/", "`../"+previous+"/output/")
	updated = strings.ReplaceAll(updated, "- output/", "- ../"+previous+"/output/")
	return strings.Replace(raw, section, updated, 1)
}

func updateManagedPlaybook(core *Core, name string, args map[string]any) string {
	pb, err := loadPlaybookWorkspace(core.Settings.Home, name)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if run, err := latestResumablePlaybookRun(pb); err != nil {
		return fmt.Sprintf("Error: %v", err)
	} else if run != nil {
		return fmt.Sprintf("Error: playbook %s has resumable run %s; finish it before changing its contract", name, run.ID)
	}
	changes := managedPlaybookChanges(pb, args)
	if len(changes) == 0 {
		return "Error: update requires context, config, or stage_name with stage_context"
	}
	for _, change := range changes {
		if err := writePlaybookFile(change.path, change.content); err != nil {
			restoreManagedPlaybookChanges(changes)
			return fmt.Sprintf("Error: %v", err)
		}
	}
	if err := validateManagedPlaybook(core, name); err != nil {
		restoreManagedPlaybookChanges(changes)
		return fmt.Sprintf("Error: update rejected: %v", err)
	}
	return fmt.Sprintf("Updated and validated playbook %s.", name)
}

type managedPlaybookChange struct {
	path, content string
	old           []byte
	existed       bool
}

func managedPlaybookChanges(pb *PlaybookWorkspace, args map[string]any) []managedPlaybookChange {
	var changes []managedPlaybookChange
	add := func(path, content string) {
		old, err := os.ReadFile(path)
		changes = append(changes, managedPlaybookChange{path: path, content: content, old: old, existed: err == nil})
	}
	if context, ok := args["context"].(string); ok {
		add(filepath.Join(pb.Dir, "CONTEXT.md"), context)
	}
	if config, ok := args["config"].(string); ok {
		add(filepath.Join(pb.Dir, "config.md"), config)
	}
	stageName, hasName := args["stage_name"].(string)
	stageContext, hasContext := args["stage_context"].(string)
	if hasName && hasContext && validStageName(stageName) {
		add(filepath.Join(pb.Dir, "stages", stageName, "CONTEXT.md"), stageContext)
	}
	return changes
}

func restoreManagedPlaybookChanges(changes []managedPlaybookChange) {
	for _, change := range changes {
		if change.existed {
			_ = writePlaybookFile(change.path, string(change.old))
		} else {
			_ = os.RemoveAll(filepath.Dir(change.path))
		}
	}
}

func deleteManagedPlaybook(core *Core, name string) string {
	pb, err := loadPlaybookWorkspace(core.Settings.Home, name)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if run, err := latestResumablePlaybookRun(pb); err != nil {
		return fmt.Sprintf("Error: %v", err)
	} else if run != nil {
		return fmt.Sprintf("Error: playbook %s has resumable run %s; finish it before deletion", name, run.ID)
	}
	schedules, err := loadSchedules(core.Settings.Home)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	for _, schedule := range schedules {
		if schedule.Name == name {
			return fmt.Sprintf("Error: cancel %s's schedule before deletion", name)
		}
	}
	if err := os.RemoveAll(pb.Dir); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return fmt.Sprintf("Deleted playbook %s and its completed run history.", name)
}

func inspectManagedPlaybook(home, name string) string {
	pb, err := loadPlaybookWorkspace(home, name)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Playbook %s: %s (%s)\n", pb.Name, pb.Description, pb.Status)
	for _, stage := range pb.Stages {
		fmt.Fprintf(&b, "- %02d-%s: tools=%s outputs=%d\n", stage.Number, stage.Name, strings.Join(stage.Tools, ", "), len(stage.Outputs))
	}
	if run, err := latestPlaybookRun(pb); err == nil && run != nil {
		fmt.Fprintf(&b, "Latest run %s: %s\n", run.ID, run.Status)
	}
	return strings.TrimSpace(b.String())
}

func validStageName(name string) bool {
	parts := strings.SplitN(name, "-", 2)
	if len(parts) != 2 || len(parts[0]) != 2 || !validPlaybookName(parts[1]) {
		return false
	}
	_, err := strconv.Atoi(parts[0])
	return err == nil && parts[0] != "00"
}

func writePlaybookFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// PlaybookSchedule is one scheduled playbook entry in ~/.mino/schedules.json.
type PlaybookSchedule struct {
	Name      string `json:"name"`
	Time      string `json:"time"`                 // HH:MM local time
	Timezone  string `json:"timezone"`             // IANA timezone
	LastRun   string `json:"last_run"`             // RFC3339 of last execution, empty if never
	LastError string `json:"last_error,omitempty"` // last fire failure, empty when healthy
}

// schedulesMu serializes read-modify-write of schedules.json: the dispatch
// goroutine updates last_run/last_error while schedule_playbook and
// cancel_schedule tools edit the same file from the LLM thread.
var schedulesMu sync.Mutex

func scheduleFilePath(home string) string { return filepath.Join(home, "schedules.json") }

func loadSchedules(home string) ([]PlaybookSchedule, error) {
	schedulesMu.Lock()
	defer schedulesMu.Unlock()
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
	schedulesMu.Lock()
	defer schedulesMu.Unlock()
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
			if _, err := loadPlaybookWorkspace(home, name); err != nil {
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
	for _, name := range playbooks {
		pb, err := loadPlaybookWorkspace(home, name)
		if err != nil || pb.Status != "active" {
			continue
		}
		run, _ := latestPlaybookRun(pb)
		hasOutput, runStatus := false, "pending"
		if run != nil {
			runStatus = run.Status
			for _, stage := range run.Stages {
				if len(stage.Outputs) > 0 {
					hasOutput = true
					break
				}
			}
		}
		tasks = append(tasks, map[string]any{
			"goal":       pb.Description,
			"status":     pb.Status,
			"stages":     len(pb.Stages),
			"has_output": hasOutput,
			"run_status": runStatus,
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
