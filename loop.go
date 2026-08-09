package main

// Mino — loop/agent.py — Core's exact loop.
// The loop remains observe → plan → act once → record proof → observe → repeat.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type LoopResult struct {
	Reply      string
	Status     string
	ToolCalls  []ToolCall
	Iterations int
	TokensIn   int
	TokensOut  int
}

// Observer matches Core's Observer callback
type Observer func(kind string, data map[string]any)

// notify helper for observers
func notify(obs Observer, kind string, data map[string]any) {
	if obs != nil {
		obs(kind, data)
	}
}

// LLMClient is the interface RunLoop needs to call the model.
// One real implementation (ProviderManager), one fake for tests.
type LLMClient interface {
	Create(session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef) (*LLMResponse, error)
	Stream(session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef, onText func(string)) (*LLMResponse, error)
}

type contextLLMClient interface {
	CreateContext(context.Context, string, ModelRole, []Message, int, string, []ToolDef) (*LLMResponse, error)
	StreamContext(context.Context, string, ModelRole, []Message, int, string, []ToolDef, func(string)) (*LLMResponse, error)
}

func RunLoop(
	client LLMClient,
	sessionID string,
	system string,
	messages []Message,
	tools *Registry,
	maxIter int,
	maxTokens int,
	obs Observer,
	stream bool,
	traceHome string,
	es *EmbeddingStore,
) *LoopResult {
	return RunLoopContext(context.Background(), client, sessionID, system, messages, tools, maxIter, maxTokens, obs, stream, traceHome, es)
}

type traceTagKey struct{}

type stageOutputsKey struct{}

// traceTagsFromCtx returns the playbook/stage tag set on the context when the
// loop is executing inside a playbook stage (see runWorkspacePlaybook).
func traceTagsFromCtx(ctx context.Context) map[string]string {
	tags, _ := ctx.Value(traceTagKey{}).(map[string]string)
	return tags
}

// missingStageOutputs returns the declared stage outputs that do not exist yet
// or are empty. The loop consults this before declaring a stage turn complete:
// a stage's contract is its artifacts, not the model's word.
func missingStageOutputs(ctx context.Context) []string {
	paths, _ := ctx.Value(stageOutputsKey{}).([]string)
	var missing []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || info.IsDir() || info.Size() == 0 {
			missing = append(missing, p)
		}
	}
	return missing
}

// --- Mutation-claim verification (chat turns) ---
// Issue #16 sibling: the model claimed "Consider it deleted" with zero tool
// calls. When the owner asks for a state change and the model declares
// success without executing anything, the loop pushes back once.

var mutationObjectWords = []string{
	"memory", "note", "notes", "file", "files", "post", "posts", "message",
	"playbook", "schedule", "reminder", "record", "entry", "task", "history",
	"data", "backup", "snapshot", "log", "chat",
}

// isMutationRequest reports whether the owner asked for a state change on a
// concrete object (verb + object), e.g. "delete the file mino.db".
func isMutationRequest(text string) bool {
	lower := strings.ToLower(text)
	hasVerb := false
	for _, v := range []string{"delete", "remove", "forget", "erase", "cancel", "clear", "rename", "move"} {
		if strings.Contains(lower, v) {
			hasVerb = true
			break
		}
	}
	if !hasVerb {
		return false
	}
	for _, o := range mutationObjectWords {
		if strings.Contains(lower, o) {
			return true
		}
	}
	for _, tok := range strings.Fields(lower) {
		if strings.Contains(tok, ".") && strings.ContainsAny(tok, "abcdefghijklmnopqrstuvwxyz0123456789") {
			return true // filename-ish token
		}
	}
	return false
}

// claimsMutationDone reports whether the model's reply declares the change
// complete. "done"/"sorted" alone are too loose; the strong claims are the
// completion forms of the mutation verbs.
func claimsMutationDone(text string) bool {
	lower := strings.ToLower(text)
	for _, c := range []string{
		"deleted", "removed", "forgotten", "erased", "gone", "cleared",
		"cancelled", "canceled", "consider it", "all set", "taken care",
		"handled", "sorted", "done",
	} {
		if strings.Contains(lower, c) {
			return true
		}
	}
	return false
}

// --- Outcome-claim verification (OSV-03) ---
// Sibling of the mutation guard: the model asserts an operation OUTCOME
// ("the edit was rejected", "I fixed it") that this turn's own tool results
// contradict (observed 2026-08-09: "the edit was rejected" while the audit
// log showed write_file succeeded). The harness's record of the turn is the
// tool results it already holds — bounded, no log scanning, checked only
// when a claim is made.

// operationNouns are the objects of outcome claims — the things Mino mutates.
// Paired with the claim words below; "failed to find" (a search) is a
// legitimate outcome, not an operation claim, and needs no noun match.
var operationNouns = append([]string{
	"edit", "write", "save", "update", "change", "fix", "reply", "comment",
	"deploy", "sync", "event", "fact", "skill", "calendar",
}, mutationObjectWords...)

// successOutcomeWords are strong success assertions; failureOutcomeWords are
// strong failure assertions. Both must pair with an operation noun.
var successOutcomeWords = []string{
	"fixed", "saved", "wrote", "edited", "updated", "changed", "replied",
	"created", "added", "deleted", "removed", "cancelled", "scheduled",
	"deployed", "applied", "landed", "completed", "went through",
}
var failureOutcomeWords = []string{
	"rejected", "failed", "couldn't", "could not", "wasn't able", "was not able",
	"refused", "denied", "not applied", "didn't work", "did not work",
	"wasn't saved", "was not saved", "didn't land", "did not land", "not saved",
}

// outcomeContradiction returns a corrective push when the reply claims an
// outcome this turn's tool results contradict, or "" when the claim is
// consistent or absent. Contradiction: a failure claim against all-successful
// tool results, or a success claim against all-errored results. Mixed tool
// results → no push (the claim may be true).
func outcomeContradiction(reply string, calls []ToolCall) string {
	lower := strings.ToLower(reply)
	failure := false
	claim := false
	for _, w := range successOutcomeWords {
		if strings.Contains(lower, w) {
			claim = true
			break
		}
	}
	for _, w := range failureOutcomeWords {
		if strings.Contains(lower, w) {
			failure = true
			claim = true
			break
		}
	}
	if !claim {
		return ""
	}
	noun := false
	for _, w := range operationNouns {
		if strings.Contains(lower, w) {
			noun = true
			break
		}
	}
	if !noun {
		return ""
	}
	failed, succeeded := 0, 0
	var successEv, failEv string
	for _, c := range calls {
		if strings.HasPrefix(c.Output, "Error") {
			failed++
			if failEv == "" {
				failEv = fmt.Sprintf("%s%v", c.Name, c.Args)
			}
		} else {
			succeeded++
			if successEv == "" {
				successEv = fmt.Sprintf("%s%v", c.Name, c.Args)
			}
		}
	}
	if failure && succeeded > 0 && failed == 0 {
		if len(successEv) > 120 {
			successEv = successEv[:120] + "…"
		}
		return fmt.Sprintf("[System: your reply claims an operation failed or was rejected, but this turn's tool results show success — e.g. %s. Reconcile your claim with the tool results; verify with system_check if the current state is unclear. Do not report failure when the harness's records show success.]", successEv)
	}
	if !failure && failed > 0 && succeeded == 0 {
		if len(failEv) > 120 {
			failEv = failEv[:120] + "…"
		}
		return fmt.Sprintf("[System: your reply claims an operation succeeded, but this turn's tool results show errors — e.g. %s. Do not claim success; retry the operation or report the actual error.]", failEv)
	}
	return ""
}

func lastUserText(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

func RunLoopContext(
	ctx context.Context,
	client LLMClient,
	sessionID string,
	system string,
	messages []Message,
	tools *Registry,
	maxIter int,
	maxTokens int,
	obs Observer,
	stream bool,
	traceHome string,
	es *EmbeddingStore,
) *LoopResult {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, sessionIDKey{}, sessionID)
	ctx = context.WithValue(ctx, userMessageKey{}, lastUserContent(messages))

	// Turn-boundary marker for the audit log: capture_playbook scopes its
	// evidence to the task turn preceding the capture request.
	if tools != nil {
		tools.LogTurnStart(sessionID)
	}

	// Stage context (playbook/stage) for trace attribution: every event written
	// while inside a playbook stage carries its stage identity so the dashboard
	// can group them instead of showing a flat stream.
	traceTags := traceTagsFromCtx(ctx)
	trace := func(eventType string, data map[string]any) {
		for k, v := range traceTags {
			data[k] = v
		}
		logTrace(traceHome, eventType, data)
	}

	result := &LoopResult{}

	defer func() {
		decision, reason := "skip", "memory tool not invoked"
		for _, call := range result.ToolCalls {
			if call.Name == "remember" {
				decision, reason = "retrieve", "memory tool invoked"
				break
			}
		}
		notify(obs, "gate", map[string]any{"decision": decision, "reason": reason})
		trace("gate", map[string]any{"decision": decision, "reason": reason})
		trace("turn_end", map[string]any{"reply": result.Reply, "status": result.Status, "iterations": result.Iterations})
	}()

	var lastLoopDetected string
	loopDetections := 0
	lastRewritePush := ""

	// Tool schemas are computed ONCE per turn, not per iteration. The selected
	// schema set feeds the `tools` array in the request payload; recomputing it
	// against the growing message history drifts the set mid-turn and breaks the
	// provider's prompt-prefix cache on every iteration (observed: iteration 2
	// cached 64/10671 tokens). Selection still happens against the full context
	// available at turn start, so specialist tools for the task are included.
	oneTurnText := lastTurnContext(messages)
	schemas := tools.SchemasForContext(sessionID, toolSelectionContext(system, messages), oneTurnText, es)
	schemaChars := 0
	schemaNames := make([]string, 0, len(schemas))
	for _, s := range schemas {
		schemaChars += len(s.Name) + len(s.Description) + 200 // ~params JSON
		schemaNames = append(schemaNames, s.Name)
	}
	trace("context_diag", map[string]any{"system_chars": len(system), "msg_count": len(messages), "schema_count": len(schemas), "schema_names": schemaNames, "schema_est_chars": schemaChars, "one_turn_chars": len(oneTurnText)})

	mutationChecked := false // push the unverified-mutation-claim correction at most once per turn
	claimChecked := false   // push the contradicted-outcome-claim correction at most once per turn
	parseFailures := 0       // consecutive unparseable text-marker calls (issue #24)

	for i := 1; i <= maxIter; i++ {
		if ctx.Err() != nil {
			result.Status = "cancelled"
			result.Reply = "Stopped."
			return result
		}
		result.Iterations = i

		// Update nervous system snapshot
		if update, ok := ctx.Value(snapshotKey{}).(func(LoopSnapshot)); ok {
			update(LoopSnapshot{Iteration: i, Status: "thinking"})
		}

		_, llmCancel := context.WithTimeout(ctx, 90*time.Second)
		resp, err := client.Create(sessionID, MainModel, messages, maxTokens, system, schemas)
		llmCancel()
		if err != nil {
			if ctx.Err() != nil {
				result.Status = "cancelled"
				result.Reply = "Stopped."
				return result
			}
			if audit, ok := ctx.Value(auditKey{}).(func(string, string, int)); ok {
				audit("error", fmt.Sprintf("LLM call failed: %v", err), i)
			}
			result.Status = "error"
			result.Reply = fmt.Sprintf("(error: %v)", err)
			return result
		}

		result.TokensIn += resp.Usage.InputTokens
		result.TokensOut += resp.Usage.OutputTokens

		notify(obs, "llm", map[string]any{
			"iteration":  i,
			"stopReason": resp.StopReason,
			"usage":      map[string]int{"in": resp.Usage.InputTokens, "out": resp.Usage.OutputTokens},
		})
		trace("llm", map[string]any{"iteration": i, "in": resp.Usage.InputTokens, "out": resp.Usage.OutputTokens})

		messages = append(messages, Message{Role: "assistant", Content: assembleAssistantContent(resp.Content)})

		toolUses := extractToolUses(resp.Content)
		markerFound := false
		if len(toolUses) == 0 {
			toolUses, markerFound = extractTextToolUses(extractText(resp.Content))
		}

		// No tool calls = LLM is done — unless a text tool-call marker was
		// detected but failed to parse (the call was dropped, not absent), or
		// the stage's declared outputs are still missing. Both get a corrective
		// push and another iteration instead of a silent "complete".
		if len(toolUses) == 0 {
			if markerFound {
				parseFailures++
				// Circuit breaker (issue #24): a model stuck in a broken marker
				// shape repeats the same failure — the identical push does not
				// help, so escalate, then abort with a diagnosis instead of
				// burning to the iteration cap (observed 2026-08-08: 16
				// consecutive failures on the facebook run, iters 35-50).
				if parseFailures >= 6 {
					slog.Error("repeated unparseable tool markers", "session_id", sessionID, "iteration", i, "failures", parseFailures)
					trace("tool_call_parse_aborted", map[string]any{"iteration": i, "failures": parseFailures})
					result.Status = "error"
					result.Reply = fmt.Sprintf("(error: repeatedly emitted unparseable tool calls after %d attempts)", parseFailures)
					return result
				}
				slog.Warn("unparseable tool_call marker", "session_id", sessionID, "iteration", i, "failures", parseFailures)
				if audit, ok := ctx.Value(auditKey{}).(func(string, string, int)); ok {
					audit("tool_call_parse_failed", fmt.Sprintf("text marker found but args did not parse (failure %d)", parseFailures), i)
				}
				trace("tool_call_parse_failed", map[string]any{"iteration": i, "failures": parseFailures})
				if parseFailures >= 3 {
					messages = append(messages, Message{
						Role:    "user",
						Content: "[System: your last " + fmt.Sprint(parseFailures) + " tool calls failed to parse. STOP re-emitting the same shape — use native function calling, or call the _FLAT variant of the tool with arguments as a JSON string. Inspect the tool schema before retrying.]",
					})
				} else {
					messages = append(messages, Message{
						Role:    "user",
						Content: "[System: your previous tool call could not be parsed — re-emit it in the exact format [tool_call: name({...})] with valid JSON, or use native function calling.]",
					})
				}
				continue
			}
			if missing := missingStageOutputs(ctx); len(missing) > 0 {
				slog.Warn("stage outputs missing at turn end", "session_id", sessionID, "iteration", i, "outputs", missing)
				if audit, ok := ctx.Value(auditKey{}).(func(string, string, int)); ok {
					audit("stage_output_missing", strings.Join(missing, ", "), i)
				}
				trace("stage_output_missing", map[string]any{"missing": missing, "iteration": i})
				messages = append(messages, Message{
					Role:    "user",
					Content: fmt.Sprintf("[System: stage incomplete — required output(s) not written: %s. Use write_file to write them, then finish.]", strings.Join(missing, ", ")),
				})
				continue
			}
			// A claimed state change with zero tool calls is a lie, not a reply:
			// the owner asked to delete/remove something and the model declared
			// success without executing anything (observed 2026-08-08:
			// "Consider it deleted from my notes" — no tool was called). Push
			// back once. Chat turns only; stages are governed by their output
			// contract above.
			if !mutationChecked && len(result.ToolCalls) == 0 {
				if _, inStage := ctx.Value(stageOutputsKey{}).([]string); !inStage {
					replyText := extractText(resp.Content)
					if isMutationRequest(lastUserText(messages)) && claimsMutationDone(replyText) {
						mutationChecked = true
						trace("mutation_claim_unverified", map[string]any{"iteration": i})
						messages = append(messages, Message{
							Role:    "user",
							Content: "[System: you were asked to change or delete something, but no tool was executed this turn. Either perform the change with the appropriate tool now, or tell the owner explicitly that you cannot. Do not claim it is done.]",
						})
						continue
					}
				}
			}
			// OSV-03: an outcome claim contradicted by this turn's own tool
			// results ("the edit was rejected" after write_file succeeded) is
			// corrected before it reaches the user. Bounded: only when a claim
			// is made, only in-memory tool results — no log scanning.
			if !claimChecked && len(result.ToolCalls) > 0 {
				if _, inStage := ctx.Value(stageOutputsKey{}).([]string); !inStage {
					replyText := extractText(resp.Content)
					if push := outcomeContradiction(replyText, result.ToolCalls); push != "" {
						claimChecked = true
						trace("outcome_claim_contradicted", map[string]any{"iteration": i})
						messages = append(messages, Message{Role: "user", Content: push})
						continue
					}
				}
			}
			result.Status = "complete"
			result.Reply = extractText(resp.Content)
			return result
		}

		// Execute tools and feed results back
		parseFailures = 0 // a successfully parsed + executed call breaks the streak (issue #24)
		toolResults := make([]map[string]any, 0)
		var turnImages []string
		for _, tc := range toolUses {
			args, _ := tc.Input.(map[string]any)

			// Update snapshot before running tool
			if update, ok := ctx.Value(snapshotKey{}).(func(LoopSnapshot)); ok {
				update(LoopSnapshot{Iteration: i, Status: "running_tool", CurrentTool: fmt.Sprintf("%s(%v)", tc.Name, args)})
			}

			// Malformed native tool-call args (provider.go injected
			// __raw_arguments__ and logged the raw string). Never execute a tool
			// with garbage input: return the raw string as the result so the
			// model sees exactly what it emitted and can re-emit valid JSON.
			var raw string
			if rawArgs, bad := args["__raw_arguments__"]; bad {
				raw = fmt.Sprintf("Error: tool call arguments did not parse as JSON. Raw arguments: %v. Re-emit this call with valid JSON arguments.", rawArgs)
			} else {
				raw = tools.ExecuteContext(ctx, tc.Name, args)
			}
			if ctx.Err() != nil {
				result.Status = "cancelled"
				result.Reply = "Stopped."
				return result
			}
			if tc.Name == "view_image" && strings.HasPrefix(raw, "data:image/") {
				turnImages = append(turnImages, raw)
				raw = "[image loaded into visual context]"
			}
			output := prepareToolOutput(traceHome, sessionID, i, tc.Name, raw)
			result.ToolCalls = append(result.ToolCalls, ToolCall{Name: tc.Name, Args: args, Output: output})

			// Update snapshot with tool result
			if update, ok := ctx.Value(snapshotKey{}).(func(LoopSnapshot)); ok {
				history := make([]string, 0)
				for _, tc := range result.ToolCalls {
					history = append(history, fmt.Sprintf("%s(%v) -> %s", tc.Name, tc.Args, toolOutputStatus(tc.Output)))
				}
				update(LoopSnapshot{Iteration: i, Status: "thinking", LastOutput: output, ToolHistory: history})
			}

			notify(obs, "tool", map[string]any{"tool": tc.Name, "args": args, "status": toolOutputStatus(raw)})
			trace("tool", map[string]any{"tool": tc.Name, "args": args, "status": toolOutputStatus(raw)})

			toolResults = append(toolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": tc.ID,
				"tool":        tc.Name,
				"content":     output,
			})
		}
		messages = append(messages, Message{Role: "user", Content: formatToolResults(toolResults), Images: turnImages})

		// Loop detection: check for repeated identical tool calls. Skipped inside
		// playbook stages: a stage whose whitelist is only search_web + write_file
		// legitimately calls search_web many times — that is the stage's job, and
		// the stage has its own iteration cap. The main loop remains guarded.
		if len(traceTags) == 0 {
			history := make([]string, 0, len(result.ToolCalls))
			for _, tc := range result.ToolCalls {
				history = append(history, fmt.Sprintf("%s(%v)", tc.Name, tc.Args))
			}
			if loop, msg := detectLoop(history); loop && msg != lastLoopDetected {
				lastLoopDetected = msg
				loopDetections++
				// Push to dashboard event stream
				pushDashEvent(map[string]any{
					"type": "loop_detected", "session_id": sessionID,
					"message": msg, "iteration": i,
				})
				// Audit trail
				if audit, ok := ctx.Value(auditKey{}).(func(string, string, int)); ok {
					audit("loop_detected", msg, i)
				}
				trace("loop_detected", map[string]any{"message": msg, "iteration": i})
				notify(obs, "loop", map[string]any{"message": msg})
				// Advisory prompts alone don't stop a stuck agent (observed: 8+ prompts
				// ignored, ~200k tokens burned on a dead-end investigation). Hard-stop
				// after three consecutive detections and hand back to the user.
				if loopDetections >= 3 {
					result.Status = "loop"
					result.Reply = "Stopped: repeated loop detected (" + msg + "). Ask the user for guidance."
					return result
				}
				messages = append(messages, Message{
					Role:    "user",
					Content: fmt.Sprintf("[System: loop detected — %s. Try a different approach or ask the user for guidance.]", msg),
				})
				notify(obs, "loop", map[string]any{"message": msg})
			}
		} else if path := stageRewriteStreak(result.ToolCalls); path != "" && path != lastRewritePush {
			// Stage-side tripwire: the loop detector is skipped inside stages
			// (repeated search_web is legit there), but a same-tool streak writing
			// the SAME output path over and over is a rewrite drift, not progress.
			// 2026-08-07: reddit-karma-builder stage 1 rewrote candidates.md 26x
			// because the whitelist lacked read_file (model couldn't verify its own
			// output) — the run burned all 50 iterations and failed.
			lastRewritePush = path
			slog.Warn("stage rewrite streak detected", "session_id", sessionID, "iteration", i, "path", path)
			if audit, ok := ctx.Value(auditKey{}).(func(string, string, int)); ok {
				audit("stage_rewrite_streak", path, i)
			}
			trace("stage_rewrite_streak", map[string]any{"path": path, "iteration": i})
			messages = append(messages, Message{
				Role:    "user",
				Content: fmt.Sprintf("[System: you have rewritten %s repeatedly. Read it back with read_file to verify its content, then either finish the stage or take a genuinely different action. Do not rewrite the same file again without reading it first.]", path),
			})
		}
	}

	result.Status = "iteration_limit"
	result.Reply = "(stopped after " + fmt.Sprint(maxIter) + " iterations)"
	return result
}

// stageRewriteStreak reports the output path being rewritten by a trailing run
// of same-tool calls with the same path argument (e.g. write_file to the same
// target 6+ times). Empty string means no rewrite drift.
func stageRewriteStreak(calls []ToolCall) string {
	if len(calls) < 6 {
		return ""
	}
	last := calls[len(calls)-1]
	path, _ := last.Args["path"].(string)
	if path == "" {
		return ""
	}
	sameTool := last.Name
	count := 0
	for i := len(calls) - 1; i >= 0; i-- {
		c := calls[i]
		if c.Name != sameTool {
			break
		}
		p, _ := c.Args["path"].(string)
		if p != path {
			break
		}
		count++
	}
	if count >= 6 {
		return path
	}
	return ""
}

func toolSelectionContext(system string, messages []Message) string {
	var b strings.Builder
	b.WriteString(system)
	for _, message := range messages {
		b.WriteString("\n")
		b.WriteString(message.Content)
	}
	text := b.String()
	if len(text) > 24000 {
		text = text[len(text)-24000:]
	}
	return text
}

// lastTurnContext returns the last user message + last assistant reply for
// targeted tool matching (semantic embedding and MCP keyword gating).
func lastTurnContext(messages []Message) string {
	var user, assistant string
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role == "user" && user == "" {
			user = m.Content
		} else if m.Role == "assistant" && assistant == "" {
			assistant = m.Content
		}
		if user != "" && assistant != "" {
			break
		}
	}
	return user + "\n" + assistant
}

func toolOutputStatus(output string) string {
	text := strings.ToLower(strings.TrimSpace(output))
	if strings.HasPrefix(text, "error:") || strings.HasPrefix(text, "error ") ||
		strings.HasPrefix(text, "extension error:") || strings.HasPrefix(text, "failed ") ||
		strings.HasPrefix(text, "search failed:") ||
		(strings.HasPrefix(text, "mcp ") && (strings.Contains(text, " failed:") || strings.Contains(text, "not connected"))) {
		return "error"
	}
	return "ok"
}

var scpCommandPattern = regexp.MustCompile(`(?i)(?:^|[\s;&|($'"])\\?(?:[^\s;&|()]+/)?scp(?:[\s;&|()'"]|$)`)
var copyCommandPattern = regexp.MustCompile(`(?i)(?:^|[\s;&|($'"])\\?(?:[^\s;&|()]+/)?(?:cp|scp|rsync)(?:[\s;&|()'"]|$)`)

func containsSCPCommand(args map[string]any) bool {
	command, _ := args["command"].(string)
	return scpCommandPattern.MatchString(command)
}

func containsCopyCommand(args map[string]any) bool {
	command, _ := args["command"].(string)
	return isShellCopyCommand(command)
}

func isShellCopyCommand(command string) bool {
	return copyCommandPattern.MatchString(command)
}

func includeToolSchema(schemas []ToolDef, registry *Registry, name string) []ToolDef {
	for _, schema := range schemas {
		if schema.Name == name {
			return schemas
		}
	}
	if schema, ok := registry.Schema(name); ok {
		return append(schemas, schema)
	}
	return schemas
}

func extractText(blocks []ContentBlock) string {
	var text string
	for _, b := range blocks {
		if b.Type == "text" {
			text += b.Text
		}
	}
	return text
}

func extractToolUses(blocks []ContentBlock) []ContentBlock {
	var uses []ContentBlock
	for _, b := range blocks {
		if b.Type == "tool_use" {
			uses = append(uses, b)
		}
	}
	return uses
}

// extractTextToolUses parses text-embedded [tool_call: name({...})] markers.
// Fallback for models that don't support native function calling.
// Returns the parsed uses and whether any marker was found — a marker that
// failed to parse must be surfaced to the caller, not silently dropped
// (2026-08-07: a bash marker with shell-style \' escapes ended a playbook
// stage "complete" without its required output).
func extractTextToolUses(text string) ([]ContentBlock, bool) {
	var uses []ContentBlock
	markerFound := false
	marker := "[tool_call:"
	for {
		idx := strings.Index(text, marker)
		if idx == -1 {
			break
		}
		markerFound = true
		text = text[idx+len(marker):]
		paren := strings.IndexByte(text, '(')
		if paren == -1 {
			break
		}
		name := strings.TrimSpace(text[:paren])
		text = text[paren+1:]
		if len(text) == 0 || text[0] != '{' {
			break
		}
		argsJSON, rest := extractBalancedJSON(text)
		if argsJSON == "" {
			break
		}
		text = rest
		var args map[string]any
		// Repair common model sloppiness in hand-written JSON: shell-style
		// escapes (devil\'s), trailing commas, and stray newlines in strings.
		// Strict-parse first; only repair-then-parse when strict fails, so
		// valid JSON is never touched.
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			argsJSON = repairToolArgsJSON(argsJSON)
			if err2 := json.Unmarshal([]byte(argsJSON), &args); err2 != nil {
				continue // keep scanning for later markers; caller sees markerFound
			}
		}
		uses = append(uses, ContentBlock{
			Type:  "tool_use",
			ID:    fmt.Sprintf("txt_%d", len(uses)),
			Name:  name,
			Input: args,
		})
	}
	return uses, markerFound
}

// repairToolArgsJSON tolerates the JSON sloppiness models emit when writing
// tool calls by hand instead of using native function calling.
func repairToolArgsJSON(s string) string {
	s = strings.ReplaceAll(s, `\'`, `'`)
	s = strings.ReplaceAll(s, ",}", "}")
	s = strings.ReplaceAll(s, ",]", "]")
	return s
}

// extractBalancedJSON extracts a brace-balanced JSON string, handling
// nested objects, strings, and escapes. Returns the JSON and remaining text.
func extractBalancedJSON(s string) (jsonStr string, remaining string) {
	if len(s) == 0 || s[0] != '{' {
		return "", s
	}
	depth := 0
	inString := false
	escaped := false
	for i, c := range s {
		if escaped {
			escaped = false
			continue
		}
		if inString {
			if c == '\\' {
				escaped = true
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[:i+1], s[i+1:]
			}
		}
	}
	return "", s
}

func hasInvalidToolInput(uses []ContentBlock) bool {
	for _, use := range uses {
		args, ok := use.Input.(map[string]any)
		if !ok || args == nil {
			return true
		}
	}
	return false
}

func lastUserContent(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

func assembleAssistantContent(blocks []ContentBlock) string {
	var out strings.Builder
	for _, b := range blocks {
		if b.Type == "text" {
			out.WriteString(b.Text)
		}
		if b.Type == "tool_use" {
			args, _ := json.Marshal(b.Input)
			if len(args) > 600 {
				args = append(args[:600], []byte("...")...)
			}
			fmt.Fprintf(&out, "\n[tool_call: %s(%s)]", b.Name, args)
		}
	}
	return strings.TrimSpace(out.String())
}

func formatToolResults(results []map[string]any) string {
	var out strings.Builder
	for _, r := range results {
		fmt.Fprintf(&out, "[tool_result tool=%v: %v]\n", r["tool"], r["content"])
	}
	return out.String()
}

type traceFile struct {
	date string
	file *os.File
}

var traceFiles = struct {
	sync.Mutex
	byHome map[string]traceFile
}{byHome: make(map[string]traceFile)}

// logTrace reuses one append handle per home and day.
func logTrace(home, eventType string, data map[string]any) {
	if home == "" {
		return
	}
	now := time.Now()
	date := now.Format("2006-01-02")
	entry := map[string]any{
		"type": eventType,
		"ts":   now.UTC().Format(time.RFC3339),
	}
	for k, v := range data {
		entry[k] = v
	}
	b, _ := json.Marshal(entry)
	traceFiles.Lock()
	defer traceFiles.Unlock()
	current := traceFiles.byHome[home]
	if current.file == nil || current.date != date {
		if current.file != nil {
			current.file.Close()
		}
		dir := filepath.Join(home, "traces")
		os.MkdirAll(dir, 0700)
		file, err := os.OpenFile(filepath.Join(dir, date+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
		current = traceFile{date: date, file: file}
		traceFiles.byHome[home] = current
	}
	current.file.Write(append(b, '\n'))
}

func closeTrace(home string) {
	traceFiles.Lock()
	defer traceFiles.Unlock()
	if current := traceFiles.byHome[home]; current.file != nil {
		current.file.Close()
		delete(traceFiles.byHome, home)
	}
}

// --- Artifact compaction (moved from artifacts.go) ---

const artifactInlineLimit = 8000

func prepareToolOutput(home, sessionID string, turn int, tool, output string) string {
	if tool == "read_file" {
		return output
	}
	return compactToolOutput(home, sessionID, turn, tool, output)
}

func compactToolOutput(home, sessionID string, turn int, tool, output string) string {
	if len(output) <= artifactInlineLimit {
		return output
	}
	dir := filepath.Join("/tmp/mino/results", safePath(sessionID), fmt.Sprintf("%d", turn))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return output[:artifactInlineLimit] + "\n[artifact write failed]"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.txt", safePath(tool), time.Now().UnixNano())) // unique name: reused turn dirs across days must never serve stale files as fresh results
	if err := os.WriteFile(path, []byte(output), 0600); err != nil {
		return output[:artifactInlineLimit] + "\n[artifact write failed]"
	}
	return fmt.Sprintf("[artifact: %s → %d chars at %s; use read_file with offset and limit]", tool, len(output), path)
}

func safePath(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, s)
	return s
}
