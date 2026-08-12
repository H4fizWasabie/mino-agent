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
	"sort"
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
	CreateContext(context.Context, string, ModelRole, []Message, int, string, []ToolDef) (*LLMResponse, error)
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
	schemaBytes, schemaHeavy := schemaDiag(schemas)
	trace("context_diag", map[string]any{"system_chars": len(system), "msg_count": len(messages), "schema_count": len(schemas), "schema_names": schemaNames, "schema_est_chars": schemaChars, "schema_bytes": schemaBytes, "schema_heavy": schemaHeavy, "one_turn_chars": len(oneTurnText)})

	mutationChecked := false // push the unverified-mutation-claim correction at most once per turn
	claimChecked := false    // push the contradicted-outcome-claim correction at most once per turn
	parseFailures := 0       // per-turn unparseable text-marker calls (issue #24; CTX-006)

	// #171 — per-turn repetition tracking for the awareness/containment
	// observation injected into the message stream (never the byte-stable system
	// prompt, so prefix-cache warmth is preserved).
	repStreak := 0
	var lastToolSig string

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

		// #171 — iteration/retry awareness. Give the model sight of its own
		// repetition and budget so it can diverge or stop BEFORE the cap. Both
		// injections go into the message stream (cache-safe) — never the static
		// system prompt — and fire only when notable to avoid token spam.
		if len(result.ToolCalls) > 0 {
			sig := loopToolSignature(result.ToolCalls[len(result.ToolCalls)-1])
			if sig == lastToolSig {
				repStreak++
			} else {
				lastToolSig = sig
				repStreak = 1
			}
			if repStreak >= 3 && repStreak%3 == 0 {
				messages = append(messages, Message{
					Role: "user",
					Content: fmt.Sprintf(
						"[System: you have repeated the identical tool call (%s) %d times in a row without progress, and have used %d of %d iterations. CHANGE APPROACH or state explicitly why you are abandoning this one — do not keep retrying the same thing to the iteration cap.]",
						sig, repStreak, i-1, maxIter),
				})
				// CTX-019: make the redirect observable — the post-mortem reads
				// midflight_signal events + the run outcome to verify whether a
				// redirect was followed and whether it helped.
				trace("midflight_signal", map[string]any{"signal": "repetition", "iteration": i - 1, "tool": sig, "streak": repStreak})
			}
		}
		if i == maxIter-3 {
			messages = append(messages, Message{
				Role: "user",
				Content: fmt.Sprintf(
					"[System: %d of %d iterations used. Finish the task now with what you have, or state what remains — do not start new exploration.]",
					i-1, maxIter),
			})
			trace("midflight_signal", map[string]any{"signal": "near_cap", "iteration": i - 1})
		}

		_, llmCancel := context.WithTimeout(ctx, 90*time.Second)
		resp, err := client.CreateContext(ctx, sessionID, MainModel, messages, maxTokens, system, schemas)
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
		failedMarker := ""
		if len(toolUses) == 0 {
			toolUses, markerFound, failedMarker = extractTextToolUses(extractText(resp.Content))
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
				// CTX-006: the count is per-turn TOTAL, not a streak — on
				// 2026-08-10 the CHEM 15 turn failed at iters 4, 11-14, 16,
				// 24-26 (9 total) yet never hit 6 consecutive, so the old
				// streak guard never fired and the run burned to the cap.
				if parseFailures >= 6 {
					slog.Error("repeated unparseable tool markers", "session_id", sessionID, "iteration", i, "failures", parseFailures)
					trace("tool_call_parse_aborted", map[string]any{"iteration": i, "failures": parseFailures})
					result.Status = "error"
					result.Reply = fmt.Sprintf("(error: repeatedly emitted unparseable tool calls after %d attempts)", parseFailures)
					return result
				}
				slog.Warn("unparseable tool_call marker", "session_id", sessionID, "iteration", i, "failures", parseFailures, "marker", failedMarker)
				if audit, ok := ctx.Value(auditKey{}).(func(string, string, int)); ok {
					audit("tool_call_parse_failed", fmt.Sprintf("text marker found but args did not parse (failure %d)", parseFailures), i)
				}
				trace("tool_call_parse_failed", map[string]any{"iteration": i, "failures": parseFailures, "marker": failedMarker})
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
		// CTX-006: no parseFailures reset here. The counter is per-turn total —
		// a model that alternates success and malformed markers (2026-08-10
		// CHEM 15) degrades just as surely as one stuck in a streak, and both
		// must abort at the same bound.
		toolResults := make([]map[string]any, 0)
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
				// T8 (map #88): the data URL is converted to vision-model text
				// here instead of being attached to the main messages — the
				// main brain never carries image bytes, so it stays on the main
				// provider for the rest of the turn and the provider prompt
				// cache is not broken by per-iteration image blobs.
				task, _ := args["task"].(string)
				desc, err := describeImage(ctx, client, sessionID, raw, task, maxTokens)
				if err != nil {
					// A failed vision call degrades to an error tool result the
					// model can react to; never fall back to attaching the image.
					slog.Warn("view_image vision call failed", "session_id", sessionID, "error", err)
					trace("vision", map[string]any{"ok": false, "error": err.Error()})
					raw = "Error: vision analysis failed: " + err.Error()
				} else {
					trace("vision", map[string]any{"ok": true, "chars": len(desc)})
					raw = "[view_image: " + desc + "]"
				}
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
		messages = append(messages, Message{Role: "user", Content: formatToolResults(toolResults)})

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
	result.Reply = iterationCapReply(maxIter, result.ToolCalls)
	return result
}

// iterationCapReply builds the message shown when a turn or playbook hits the
// iteration cap. Instead of a bare "(stopped after N iterations)" it reports
// what was attempted so the user (or a "continue") knows where work stops.
// #161: a silent cap reply made every Continue blindly re-run the same dead
// task; surfacing the completed tools + a continue/abandon decision point lets
// the user (and the model on a later turn) resume meaningfully.
func iterationCapReply(maxIter int, toolCalls []ToolCall) string {
	done := make([]string, 0)
	seen := map[string]bool{}
	for _, tc := range toolCalls {
		if tc.Name != "" && !seen[tc.Name] {
			seen[tc.Name] = true
			done = append(done, tc.Name)
		}
	}
	header := fmt.Sprintf("(stopped after %d iterations)", maxIter)
	if len(done) == 0 {
		return header + " No tools were completed yet."
	}
	return fmt.Sprintf("%s Completed steps: %s. Continue, or abandon the task? Reply \"continue\" to resume or describe what to change.",
		header, strings.Join(done, ", "))
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

// schemaDiag measures the real serialized schema payload: the JSON bytes sent
// to the provider plus the five heaviest schemas. schema_est_chars (+200 per
// schema) undercounts MCP executor schemas by ~4×, so compaction and capping
// decisions are sized from the real number.
func schemaDiag(schemas []ToolDef) (bytes int, heavy []map[string]any) {
	if len(schemas) == 0 {
		return 0, nil
	}
	raw, err := json.Marshal(schemas)
	if err != nil {
		return 0, nil
	}
	bytes = len(raw)
	type sized struct {
		name string
		size int
	}
	all := make([]sized, 0, len(schemas))
	for _, s := range schemas {
		if b, err := json.Marshal(s); err == nil {
			all = append(all, sized{s.Name, len(b)})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].size > all[j].size })
	for i := 0; i < len(all) && i < 5; i++ {
		heavy = append(heavy, map[string]any{"name": all[i].name, "chars": all[i].size})
	}
	return bytes, heavy
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
// stage "complete" without its required output). The third return is a
// bounded snippet of the last marker whose args could not be parsed, for
// diagnostics (the loop logs it — without it the failure shape is invisible).
//
// Tolerance (2026-08-12, #158): some models under OpenRouter/provider
// rotation emit the tool call WITHOUT the [tool_call:] prefix — bare
// "name({...})" — which strict parsing dropped, spitting the model into a
// retry loop until the iteration cap (observed: a FB publish call burned
// ~20 iterations re-emitting MCP_composio_..._FLAT({...})). When no prefixed
// marker is found, fall back to scanning bare name({json}) calls.
func extractTextToolUses(text string) ([]ContentBlock, bool, string) {
	uses, markerFound, failed := extractPrefixedToolUses(text)
	if len(uses) > 0 || markerFound {
		return uses, markerFound, failed
	}
	return extractBareToolUses(text)
}

// extractPrefixedToolUses scans for [tool_call: name({...})] markers.
func extractPrefixedToolUses(text string) ([]ContentBlock, bool, string) {
	var uses []ContentBlock
	markerFound := false
	failed := ""
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
			failed = markerSnippet(text)
			break
		}
		name := strings.TrimSpace(text[:paren])
		text = text[paren+1:]
		if len(text) == 0 || text[0] != '{' {
			// The model may wrap the args in a markdown code fence inside the
			// marker: [tool_call: name(```json\n{...}\n```)]. Strip it before
			// giving up.
			if fenced := stripJSONFences(text); len(fenced) > 0 && fenced[0] == '{' {
				text = fenced
			} else {
				failed = markerSnippet(text)
				break
			}
		}
		argsJSON, rest := extractBalancedJSON(text)
		if argsJSON == "" {
			failed = markerSnippet(text)
			break
		}
		text = rest
		args, ok := parseToolArgsJSON(argsJSON)
		if !ok {
			failed = markerSnippet(name + "(" + argsJSON + ")")
			continue // keep scanning for later markers; caller sees markerFound
		}
		uses = append(uses, ContentBlock{
			Type:  "tool_use",
			ID:    fmt.Sprintf("txt_%d", len(uses)),
			Name:  name,
			Input: args,
		})
	}
	return uses, markerFound, failed
}

// bareToolNameRe matches a bare tool-call start: an identifier immediately
// followed by '(' and a JSON object — e.g. "bash({"command":...})" or
// "MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL_FLAT({"arguments_json":...})".
// Used as the tolerant fallback (see extractTextToolUses). The paren/JSON
// shape is required so ordinary prose ("seen({"..."})") is not misread as
// a tool call.
var bareToolNameRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_:]*)\s*\(\s*(\{)`)

// extractBareToolUses scans for bare name({...}) tool calls when no prefixed
// [tool_call:] markers exist. Only emits a use when the args parse as JSON
// and the name is identifier-shaped; anything else is left alone so prose is
// not spuriously dispatched as a tool call.
func extractBareToolUses(text string) ([]ContentBlock, bool, string) {
	var uses []ContentBlock
	for {
		loc := bareToolNameRe.FindStringSubmatchIndex(text)
		if loc == nil {
			break
		}
		name := text[loc[2]:loc[3]]
		braceAt := loc[4]
		argsJSON, rest := extractBalancedJSON(text[braceAt:])
		// Advance past this candidate regardless so the scan always moves.
		if argsJSON != "" {
			args, ok := parseToolArgsJSON(argsJSON)
			if ok {
				uses = append(uses, ContentBlock{
					Type:  "tool_use",
					ID:    fmt.Sprintf("txt_%d", len(uses)),
					Name:  name,
					Input: args,
				})
			}
			text = rest
		} else {
			// Not a JSON object after the paren — skip the opening brace char
			// and continue scanning onward.
			text = text[braceAt+1:]
		}
	}
	return uses, len(uses) > 0, ""
}

// markerSnippet bounds the diagnostic text so a pathological marker cannot
// flood the trace or logs.
func markerSnippet(s string) string {
	const max = 200
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// parseToolArgsJSON parses tool-call args, trying progressively lenient
// repairs of common model sloppiness in hand-written JSON: shell-style
// escapes (devil\'s), trailing commas, stray newlines, markdown fences,
// single-quoted strings, and unquoted keys. Valid JSON parses on the first
// attempt and is never transformed.
func parseToolArgsJSON(s string) (map[string]any, bool) {
	var args map[string]any
	if json.Unmarshal([]byte(s), &args) == nil {
		return args, true
	}
	for _, variant := range repairToolArgsVariants(s) {
		if json.Unmarshal([]byte(variant), &args) == nil {
			return args, true
		}
	}
	return nil, false
}

// repairToolArgsVariants returns deterministic lenient repairs, cheapest
// first. Each is applied to the original text (fence stripping runs first,
// then the standard repair).
func repairToolArgsVariants(s string) []string {
	base := repairToolArgsJSON(s)
	sq := singleQuotesToDouble(base)
	return []string{
		base,
		repairToolArgsJSON(stripJSONFences(s)),
		sq,
		quoteUnquotedKeys(base),
		quoteUnquotedKeys(sq),
	}
}

// stripJSONFences removes a ```json / ``` code fence wrapped around the args.
func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i != -1 {
		s = s[i+1:]
	} else {
		s = s[3:]
	}
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSpace(s[:len(s)-3])
	}
	return s
}

// singleQuotesToDouble converts single-quoted strings to double-quoted, the
// shape models emit when they write JSON by hand ({'path': '/x'}). Only ever
// applied as a repair variant — the result must still parse to be used.
func singleQuotesToDouble(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inDQ := false
	inSQ := false
	escaped := false
	for _, c := range s {
		if escaped {
			b.WriteRune(c)
			escaped = false
			continue
		}
		switch {
		case c == '\\' && (inDQ || inSQ):
			b.WriteRune(c)
			escaped = true
		case inSQ:
			if c == '\'' {
				b.WriteRune('"')
				inSQ = false
			} else {
				b.WriteRune(c)
			}
		case inDQ:
			if c == '"' {
				inDQ = false
			}
			b.WriteRune(c)
		case c == '\'':
			b.WriteRune('"')
			inSQ = true
		case c == '"':
			b.WriteRune(c)
			inDQ = true
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// quoteUnquotedKeys adds quotes around unquoted object keys ({path: /x} ->
// {"path": /x}). Only ever applied as a repair variant.
func quoteUnquotedKeys(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	inDQ := false
	escaped := false
	// Classic loop: the body skips ahead (i = j-1) after quoting a key,
	// which range iteration would ignore.
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}
		if inDQ {
			b.WriteByte(c)
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inDQ = false
			}
			continue
		}
		if c == '"' {
			inDQ = true
			b.WriteByte(c)
			continue
		}
		// Outside strings: a word right after { or , that is followed by :
		// is an unquoted key.
		if c == '{' || c == ',' {
			b.WriteByte(c)
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				b.WriteByte(s[j])
				j++
			}
			start := j
			for j < len(s) && isKeyChar(s[j]) {
				j++
			}
			if j > start && j < len(s) && s[j] == ':' && s[start] != '"' {
				b.WriteByte('"')
				b.WriteString(s[start:j])
				b.WriteByte('"')
				i = j - 1
				continue
			}
			if j > start {
				b.WriteString(s[start:j])
				i = j - 1
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func isKeyChar(c byte) bool {
	return c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
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

// artifactInlineLimit bounds how much of a tool result is appended inline to
// the running turn. Larger results are written to an artifact file and the
// inline copy carries the marker plus a head/tail preview, so the model sees
// the beginning (status, first matches) and end (final lines, errors) of
// every result without re-sending tens of thousands of chars on each
// iteration (issue #99 / wayfinder map #88). read_file is NOT exempt: live
// measurement (facebook run, 2026-08-10) showed eleven read_file results —
// up to 8k chars each, re-sent on every iteration — dominating a 2.48M-token
// run. The system prompt already coaches targeted slice reads.
const artifactInlineLimit = 4000

// toolPreviewHead/Tail: how much of a compacted result stays inline after the
// marker (the compactUserInput pattern).
const (
	toolPreviewHead = 2000
	toolPreviewTail = 500
)

func prepareToolOutput(home, sessionID string, turn int, tool, output string) string {
	return compactToolOutput(home, sessionID, turn, tool, output)
}

// visionPrompt maps the view_image task argument to a curated prompt
// (T8 follow-up from #103). critique/OCR/describe get their own variants;
// an empty task keeps the original describe-for-critic prompt; anything
// else falls through to the free-form wrapper so custom tasks still work.
func visionPrompt(task string) string {
	lower := strings.ToLower(task)
	switch {
	case strings.Contains(lower, "critique") || strings.Contains(lower, "review") ||
		strings.Contains(lower, "assess") || strings.Contains(lower, "judge") || strings.Contains(lower, "approve"):
		return "You are a rigorous image critic. Evaluate this image for quality and fitness for publication: composition, focus, exposure, resolution, artifacts, cropping, text legibility, and anything that would look unprofessional. End with a clear verdict: PASS or REJECT — and if REJECT, name the single most important fix."
	case strings.Contains(lower, "ocr") || strings.Contains(lower, "extract") || strings.Contains(lower, "transcrib"):
		return "Extract all visible text from this image verbatim, preserving line breaks. Output only the extracted text; if there is none, say exactly 'No text found'. Do not summarize, correct, or comment."
	case strings.Contains(lower, "describe") || strings.Contains(lower, "description"):
		return "Describe this image factually: subject, setting, visible text, colors, layout, and mood. Be neutral and concrete; do not speculate about intent beyond what is visible."
	case task == "":
		return "Describe this image precisely and neutrally as a brief for a critic: subject, layout, visible text, colors, mood, and any flaws (blur, artifacts, cropping, cut-off elements). Be concrete; do not speculate about intent beyond what is visible."
	default:
		return "You are looking at an image. Task: " + task + " Answer directly and precisely, based only on what the image shows."
	}
}

// describeImage sends one image to the vision-capable provider (VisionModel
// role, which skips text-only providers) and returns the model's text
// response. T8 (wayfinder map #88): view_image data URLs are converted here
// into a text tool result instead of being attached to the main messages — the
// main brain stays on the main provider for the rest of the turn, per-iteration
// image re-sends disappear, and the provider prompt cache is no longer broken
// by unique image blobs. One LLM call, tool-result transformation: no second
// agent loop. The per-call task text (from the tool args, when provided) steers
// the analysis via visionPrompt.
func describeImage(ctx context.Context, client LLMClient, sessionID, dataURL, task string, maxTokens int) (string, error) {
	prompt := visionPrompt(task)
	resp, err := client.CreateContext(ctx, sessionID, VisionModel, []Message{{Role: "user", Content: prompt, Images: []string{dataURL}}}, maxTokens, "", nil)
	if err != nil {
		return "", err
	}
	desc := strings.TrimSpace(extractText(resp.Content))
	if desc == "" {
		return "", fmt.Errorf("empty vision response")
	}
	return desc, nil
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
	head := toolPreviewHead
	if head > len(output) {
		head = len(output)
	}
	tail := toolPreviewTail
	if tail > len(output)-head {
		tail = len(output) - head
	}
	return fmt.Sprintf("[artifact: %s → %d chars at %s; use read_file with offset and limit]\nHEAD:\n%s\n...\nTAIL:\n%s", tool, len(output), path, output[:head], output[len(output)-tail:])
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

// loopToolSignature is a deterministic signature of a tool call (name + sorted
// args) used to detect an identical retry loop (issues #171). Args are a map,
// so keys are sorted to keep the signature stable regardless of iteration order.
func loopToolSignature(tc ToolCall) string {
	args := tc.Args
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(tc.Name)
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%v", k, args[k])
	}
	b.WriteByte('}')
	return b.String()
}
