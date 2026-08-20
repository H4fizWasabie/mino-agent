package main

// Mino — loop/agent.py — Core's exact loop.
// The loop remains observe → plan → act once → record proof → observe → repeat.

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
	CreateContextNoReasoning(context.Context, string, ModelRole, []Message, int, string) (*LLMResponse, error)
	Stream(session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef, onText func(string)) (*LLMResponse, error)
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
) *LoopResult {
	return RunLoopContext(context.Background(), client, sessionID, system, messages, tools, maxIter, maxTokens, obs, stream, traceHome)
}

type traceTagKey struct{}

type stageOutputsKey struct{}

type reviewGateKey struct{}

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
		if scriptCallFailed(c) {
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

// scriptCallFailed reports whether a ToolCall (native or code-mode "script")
// ended in failure. Script outputs carry the "[script exit N]" wrapper; a
// native call's output is the raw tool result (Error: prefix convention).
func scriptCallFailed(c ToolCall) bool {
	if strings.HasPrefix(c.Output, "[script exit") {
		if i := strings.Index(c.Output, "]"); i > 0 {
			codeStr := strings.TrimSpace(strings.TrimPrefix(c.Output[:i], "[script exit"))
			if code, err := strconv.Atoi(codeStr); err == nil {
				return code != 0 || strings.Contains(c.Output[i:], "\nError:")
			}
		}
		return true
	}
	return strings.HasPrefix(c.Output, "Error")
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
) *LoopResult {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, sessionIDKey{}, sessionID)
	ctx = context.WithValue(ctx, userMessageKey{}, lastUserContent(messages))

	// Config-change push signal (#204): if providers.json changed since the
	// last load, the FIRST turn after the change carries a re-verify notice so
	// the brain refreshes model-stack memory facts instead of answering stale.
	if pm, ok := client.(interface{ ConsumeConfigChange() time.Time }); ok {
		if changed := pm.ConsumeConfigChange(); !changed.IsZero() {
			system += fmt.Sprintf(
				"\n[config change notice: providers.json changed at %s — re-verify model-stack memory facts (remember/model_stack) against the live file before answering model questions]",
				changed.UTC().Format(time.RFC3339))
		}
	}

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

	// CDE-001 (#271): code mode — no JSON tools array. The stub module (the
	// registry rendered as compact text) joins the system prompt ONCE per
	// turn; it is byte-stable, so the provider prefix cache stays warm.
	// Stage runs pass a filtered registry (Tools.Only), so the stub is
	// stage-scoped automatically; chat runs get the full registry. The
	// sliding schema-selection machinery is retired from the loop path.
	oneTurnText := lastTurnContext(messages)
	schemas := []ToolDef(nil)
	system = system + "\n\n" + tools.StubModule()
	trace("context_diag", map[string]any{"system_chars": len(system), "msg_count": len(messages), "schema_count": 0, "stub_chars": len(tools.StubModule()), "one_turn_chars": len(oneTurnText)})

	mutationChecked := false // push the unverified-mutation-claim correction at most once per turn
	claimChecked := false    // push the contradicted-outcome-claim correction at most once per turn
	gatePlanChecked := false // review-gate: push the plan-only correction at most once per turn
	parseFailures := 0       // per-turn unparseable text-marker calls (issue #24; CTX-006)

	// #171 — per-turn repetition tracking for the awareness/containment
	// observation injected into the message stream (never the byte-stable system
	// prompt, so prefix-cache warmth is preserved).
	repStreak := 0
	var lastToolSig string

	// CTX-025 fix-B: divergence detector state. The baseline is the input
	// token count of iteration 1 (the stage prompt is byte-stable, so growth
	// past 3x baseline means context bloat from tool results, not the prompt).
	// The reset fires once per turn; baseMsgCount is the snapshot of the
	// message stack at loop start (system + stage prompt) that a reset
	// truncates back to — clearing the accumulated exploration noise.
	divergenceBaseline := 0
	divergenceReset := false
	baseMsgCount := len(messages)

	for i := 1; i <= maxIter; i++ {
		if ctx.Err() != nil {
			result.Status = "cancelled"
			result.Reply = "Stopped."
			return result
		}
		result.Iterations = i
		// Carry the iteration into tool execution so tool_calls rows record
		// which loop step produced them (#272 warm-up: the column existed but
		// the INSERT never passed it).
		ctx = context.WithValue(ctx, iterationKey{}, i)

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

		// CTX-025 fix-B: stabilize the divergence baseline from the first
		// iteration's input tokens (byte-stable stage prompt + stub module).
		if divergenceBaseline == 0 && resp.Usage.InputTokens > 0 {
			divergenceBaseline = resp.Usage.InputTokens
		}

		notify(obs, "llm", map[string]any{
			"iteration":  i,
			"stopReason": resp.StopReason,
			"usage":      map[string]int{"in": resp.Usage.InputTokens, "out": resp.Usage.OutputTokens},
		})
		trace("llm", map[string]any{"iteration": i, "in": resp.Usage.InputTokens, "out": resp.Usage.OutputTokens})

		messages = append(messages, Message{Role: "assistant", Content: assembleAssistantContent(resp.Content)})

		// CDE-001 (#271): code mode — the model's only action is a [script]
		// marker. Native tool calls cannot occur (no tools array is sent);
		// the old text-marker [tool_call: ...] protocol is retired.
		var scripts []string
		markerFound := false
		markerMalformed := false
		legacyMarker := false
		scripts, markerFound, markerMalformed, legacyMarker = extractScriptMarkers(extractText(resp.Content))

		// No script = LLM is done — unless a [script] marker was detected but
		// malformed (the call was dropped, not absent), or the stage's declared
		// outputs are still missing. Both get a corrective push and another
		// iteration instead of a silent "complete".
		if len(scripts) == 0 {
			if legacyMarker {
				// Retired protocol (CDE-001): the model reached for
				// [tool_call: ...] — push the code-mode correction once per
				// turn instead of silently treating it as the final reply.
				messages = append(messages, Message{
					Role:    "user",
					Content: "[System: [tool_call: ...] / DSML / XML function-call syntax is retired. Code mode: emit ONE script between [script] and [/script] markers, or reply in plain text when done. See the stub module.]",
				})
				continue
			}
			if markerFound {
				parseFailures++
				// Circuit breaker (issue #24): a model stuck in a broken marker
				// shape repeats the same failure — the identical push does not
				// help, so escalate, then abort with a diagnosis instead of
				// burning to the iteration cap. CTX-006: the count is per-turn
				// TOTAL, not a streak.
				if parseFailures >= 6 {
					slog.Error("repeated malformed script markers", "session_id", sessionID, "iteration", i, "failures", parseFailures)
					trace("tool_call_parse_aborted", map[string]any{"iteration": i, "failures": parseFailures})
					result.Status = "error"
					result.Reply = fmt.Sprintf("(error: repeatedly emitted malformed script markers after %d attempts)", parseFailures)
					return result
				}
				slog.Warn("malformed script marker", "session_id", sessionID, "iteration", i, "failures", parseFailures)
				if audit, ok := ctx.Value(auditKey{}).(func(string, string, int)); ok {
					audit("tool_call_parse_failed", fmt.Sprintf("script marker found but empty (failure %d)", parseFailures), i)
				}
				trace("tool_call_parse_failed", map[string]any{"iteration": i, "failures": parseFailures, "malformed": markerMalformed})
				if parseFailures >= 3 {
					messages = append(messages, Message{
						Role:    "user",
						Content: "[System: your last " + fmt.Sprint(parseFailures) + " script markers were empty or malformed. Emit ONE script between [script] and [/script] — non-empty bash. Inspect the stub module before retrying.]",
					})
				} else {
					messages = append(messages, Message{
						Role:    "user",
						Content: "[System: your previous [script] marker contained no script. Re-emit it as [script]#!/bin/bash\n...\n[/script] with real commands.]",
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
			// Review-gate guard (#283): a gate whose turn produced NO tool
			// calls (plan-only reply) must not complete — the gate's contract
			// is observe/act/verdict, and a plan is neither. Push the model to
			// act (view_image / read_file / write_file via mino exec) instead
			// of letting a planning reply count as done. Live: instagram
			// 02-compose gate replied "Let me start by reading..." with no
			// script and the loop completed turn 1, then failed on the
			// missing VERDICT.
			if _, isGate := ctx.Value(reviewGateKey{}).(bool); isGate && len(result.ToolCalls) == 0 && !gatePlanChecked {
				gatePlanChecked = true
				trace("midflight_signal", map[string]any{"signal": "gate_plan_only", "iteration": i})
				messages = append(messages, Message{
					Role:    "user",
					Content: "[System: you are the review gate — you must ACT, not plan. Emit a script that observes the artifacts (read_file / view_image via `mino exec`) and performs the corrective work. A planning reply does not count. Then end with VERDICT: PASS or VERDICT: FAIL.]",
				})
				continue
			}
			result.Status = "complete"
			result.Reply = extractText(resp.Content)
			return result
		}

		// Execute scripts and feed results back (CDE-001, #271).
		// CTX-006: no parseFailures reset here — the counter is per-turn total.
		// Each script runs through the denylist gate BEFORE execution; a
		// flagged script never runs and the model gets the reason. Script
		// runs are recorded as synthetic ToolCalls (name "script") so the
		// repetition guard, loop detection, mutation check and outcome
		// contradiction machinery all keep working unchanged.
		toolResults := make([]map[string]any, 0)
		for si, script := range scripts {
			head := firstScriptLine(script)

			// Update snapshot before running
			if update, ok := ctx.Value(snapshotKey{}).(func(LoopSnapshot)); ok {
				update(LoopSnapshot{Iteration: i, Status: "running_tool", CurrentTool: "script: " + head})
			}

			var raw string
			if reason := gateScript(script); reason != "" {
				raw = "Error: script blocked by the harness gate (" + reason + "). Rewrite without the blocked construct — the gate is absolute."
			} else {
				output, code := runLoopScript(ctx, script, sessionID)
				raw = fmt.Sprintf("[script exit %d]\n%s", code, output)
			}
			if ctx.Err() != nil {
				result.Status = "cancelled"
				result.Reply = "Stopped."
				return result
			}
			output := prepareToolOutput(traceHome, sessionID, i, "script", raw)
			args := map[string]any{"head": head}
			result.ToolCalls = append(result.ToolCalls, ToolCall{Name: "script", Args: args, Output: output})
			tools.LogScriptRun(ctx, sessionID, script, raw, toolOutputStatus(raw))

			// Update snapshot with result
			if update, ok := ctx.Value(snapshotKey{}).(func(LoopSnapshot)); ok {
				history := make([]string, 0)
				for _, tc := range result.ToolCalls {
					history = append(history, fmt.Sprintf("%s(%v) -> %s", tc.Name, tc.Args, toolOutputStatus(tc.Output)))
				}
				update(LoopSnapshot{Iteration: i, Status: "thinking", LastOutput: output, ToolHistory: history})
			}

			notify(obs, "tool", map[string]any{"tool": "script", "args": args, "status": toolOutputStatus(raw)})
			trace("script", map[string]any{"iteration": i, "head": head, "status": toolOutputStatus(raw)})

			toolResults = append(toolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": fmt.Sprintf("script_%d", si),
				"tool":        "script",
				"content":     output,
			})
		}
		messages = append(messages, Message{Role: "user", Content: formatToolResults(toolResults)})

		// CTX-025 fix-B: divergence detector — in-tokens > 3× the iteration-1
		// baseline within the first 10 iterations while the stage's required
		// outputs are still missing = exploration without production (the
		// daily-ai-concept 982k-token burn; the chat-driven instagram test's
		// 29-iteration explore loop). Fire once per turn: state-reset the
		// context back to the stage prompt, re-inject the required outputs,
		// and push the model to converge instead of continuing to explore.
		if !divergenceReset && len(traceTags) > 0 && i <= 10 && divergenceBaseline > 0 &&
			resp.Usage.InputTokens > 3*divergenceBaseline {
			if missing := missingStageOutputs(ctx); len(missing) > 0 {
				divergenceReset = true
				slog.Error("stage divergence detected — resetting turn context", "session_id", sessionID, "iteration", i, "in_tokens", resp.Usage.InputTokens, "baseline", divergenceBaseline, "missing", missing)
				trace("midflight_signal", map[string]any{"signal": "divergence", "iteration": i, "in_tokens": resp.Usage.InputTokens, "baseline": divergenceBaseline, "missing": missing})
				if audit, ok := ctx.Value(auditKey{}).(func(string, string, int)); ok {
					audit("divergence_reset", strings.Join(missing, ", "), i)
				}
				messages = messages[:baseMsgCount]
				messages = append(messages, Message{
					Role:    "user",
					Content: fmt.Sprintf("[System: divergence reset — your context grew to %d tokens (%dx baseline) without producing the required output(s): %s. Prior exploration results are cleared. Re-read the stage contract above and WRITE the required outputs now — do not explore further.]", resp.Usage.InputTokens, resp.Usage.InputTokens/divergenceBaseline, strings.Join(missing, ", ")),
				})
				continue
			}
		}

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

var copyCommandPattern = regexp.MustCompile(`(?i)(?:^|[\s;&|($'"])\\?(?:[^\s;&|()]+/)?(?:cp|scp|rsync)(?:[\s;&|()'"]|$)`)

func isShellCopyCommand(command string) bool {
	return copyCommandPattern.MatchString(command)
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

// extractPrefixedToolUses scans for [tool_call: name({...})] markers.

// bareToolNameRe matches a bare tool-call start: an identifier immediately
// followed by '(' and a JSON object — e.g. "bash({"command":...})" or
// "MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL_FLAT({"arguments_json":...})".
// Used as the tolerant fallback (see extractTextToolUses). The paren/JSON
// shape is required so ordinary prose ("seen({"..."})") is not misread as
// a tool call.

// extractBareToolUses scans for bare name({...}) tool calls when no prefixed
// [tool_call:] markers exist. Only emits a use when the args parse as JSON
// and the name is identifier-shaped; anything else is left alone so prose is
// not spuriously dispatched as a tool call.

// markerSnippet bounds the diagnostic text so a pathological marker cannot
// flood the trace or logs.

// parseToolArgsJSON parses tool-call args, trying progressively lenient
// repairs of common model sloppiness in hand-written JSON: shell-style
// escapes (devil\'s), trailing commas, stray newlines, markdown fences,
// single-quoted strings, and unquoted keys. Valid JSON parses on the first
// attempt and is never transformed.

// repairToolArgsVariants returns deterministic lenient repairs, cheapest
// first. Each is applied to the original text (fence stripping runs first,
// then the standard repair).

// stripJSONFences removes a ```json / ``` code fence wrapped around the args.

// singleQuotesToDouble converts single-quoted strings to double-quoted, the
// shape models emit when they write JSON by hand ({'path': '/x'}). Only ever
// applied as a repair variant — the result must still parse to be used.

// quoteUnquotedKeys adds quotes around unquoted object keys ({path: /x} ->
// {"path": /x}). Only ever applied as a repair variant.

// repairToolArgsJSON tolerates the JSON sloppiness models emit when writing
// tool calls by hand instead of using native function calling.

// extractBalancedJSON extracts a brace-balanced JSON string, handling
// nested objects, strings, and escapes. Returns the JSON and remaining text.

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

// provenanceGateWarning (CTX-022 C, proactive): if recall of the query
// returned a user-provenanced top fact, return a mid-flight warning naming it
// as the higher-priority truth candidate. Empty when no conflict signal.
func provenanceGateWarning(recall, query string) string {
	if !strings.Contains(recall, "user-provenanced") {
		return ""
	}
	words := memoryTokenize(query)
	if len(matchedWords(words, recall)) == 0 {
		return ""
	}
	subject := firstFactSubjectLine(recall)
	if subject == "" {
		return ""
	}
	return fmt.Sprintf("[System: provenance gate — your memory returned a user-authored fact on this topic (%s). It outranks web data unless flagged stale or superseded. Verify gaps; do not re-litigate the owner's own fact.]", subject)
}

// firstFactSubjectLine returns the first non-edge line carrying a fact id.
func firstFactSubjectLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if strings.Contains(t, "# ") && !strings.Contains(t, "→") && !strings.Contains(t, "←") {
			return t
		}
	}
	return ""
}

// provenanceSearchWarning (CTX-022 C, reactive) inspects the turn's earlier
// tool calls: if a `remember` returned user-provenanced facts whose text
// overlaps this web-search query, it returns the same warning. Empty when no
// conflict signal exists. (The proactive gate on search_web itself covers the
// model-skipped-remember case; this covers remember-then-search.)

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
	// OBS-001 loop liveness: the stall heartbeat watches per-active-turn
	// staleness. A frozen loop (2026-08-14 wedge) keeps background tickers
	// tracing, so global trace freshness is the WRONG signal — in-flight turn
	// with no loop activity is the right one.
	switch eventType {
	case "turn_start":
		markTurnStart()
		markLoopActivity()
	case "turn_end":
		markTurnEnd()
	case "llm", "tool", "vision", "gate", "context_diag", "midflight_signal", "tool_call_parse_failed":
		markLoopActivity()
	}
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

// --- Spill store (RUN-007: durable spill artifacts) ---

// spillDir is the durable spill root under the Mino home — the old
// /tmp/mino/results was RAM-backed and died on reboot, so oversized tool
// outputs (issue #99) never survived to the eval workflow that needs them
// (ex-#214). Every spill writer routes through this helper, which also runs
// the throttled max-age prune: durable means bounded. (dsh's spillStore was
// checked for a retention pattern first — it has none: files persist until
// external cleanup, with per-session dirs only grouping them for that future
// cleanup. Mino's bound is therefore our own, 30 days like audit events.)
func spillDir(home string) string {
	pruneSpillsIfDue(home)
	return filepath.Join(home, "results")
}

// spillRetention bounds the spill store: artifacts older than this are pruned.
// 30 days matches the audit-event horizon and comfortably covers the artifact
// catalog's own 1-day lookback plus the eval workflow's fetch window.
const spillRetention = 30 * 24 * time.Hour

var (
	spillPruneMu   sync.Mutex
	lastSpillPrune time.Time
)

// spillPruneEvery throttles the on-write prune sweep — each sweep is a full
// directory walk, so once an hour is plenty to keep the store bounded without
// taxing write-heavy turns.
const spillPruneEvery = time.Hour

// pruneSpillsIfDue sweeps at most once per spillPruneEvery; called from
// spillDir on every spill write and once at boot.
func pruneSpillsIfDue(home string) {
	spillPruneMu.Lock()
	defer spillPruneMu.Unlock()
	if time.Since(lastSpillPrune) < spillPruneEvery {
		return
	}
	lastSpillPrune = time.Now()
	pruneSpills(home)
}

// pruneSpills deletes spill files older than spillRetention and the empty
// directories left behind. Removal races with concurrent spill writes are
// benign (os.Remove fails silently on a fresh file/dir and the next sweep
// retries).
func pruneSpills(home string) {
	root := filepath.Join(home, "results")
	cutoff := time.Now().Add(-spillRetention)
	var dirs []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // root missing or unreadable: nothing to prune
		}
		if d.IsDir() {
			if path != root {
				dirs = append(dirs, path)
			}
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.Remove(path)
		}
		return nil
	})
	// WalkDir lists parents before children; removing in reverse drops the
	// deepest empty dirs first (os.Remove fails on non-empty, harmlessly).
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i])
	}
}

func compactToolOutput(home, sessionID string, turn int, tool, output string) string {
	if len(output) <= artifactInlineLimit {
		return output
	}
	dir := filepath.Join(spillDir(home), safePath(sessionID), fmt.Sprintf("%d", turn))
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
