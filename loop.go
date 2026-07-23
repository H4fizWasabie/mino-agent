package main

// Mino — loop/agent.py — Core's exact loop.
// The loop remains observe → plan → act once → record proof → observe → repeat.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

const (
	completionToolName = "complete_task"
	maxNoProgress      = 3
	completionPrompt   = `COMPLETION PROTOCOL (RUNTIME ENFORCED):
- Ordinary assistant text is progress and cannot end the turn.
- To answer the user, call complete_task ALONE with status and the final reply.
- Use status "complete" only when every requested step is verified complete.
- Use status "blocked" only when required user input, approval, or an unavailable external dependency prevents further safe progress.
- If work remains, call the next tool instead.
- Each side-effecting tool follows: plan one action, execute it once, use its action receipt as proof, then observe before deciding what comes next. Never repeat an action whose receipt is successful.
- TOOL HYGIENE: Prefer write_file over bash echo for file creation. Prefer read_file over bash cat. If a specialized tool exists for your task, use it — bash is the fallback, not the default.
- Before claiming "file created" in complete_task, verify the file exists. If it does not exist, fix it first. The harness may reject completion with unverified file claims.
- IMPORTANT: complete_task.reply must contain the FULL answer including any jokes, lists, code, or content the user requested. NEVER wrap it with meta-commentary like "Hope that helped!" — put the actual content in the reply field.`
)

var completionTool = ToolDef{
	Name:        completionToolName,
	Description: "Finish the current task. Call alone only after all work is complete or genuinely blocked.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{"type": "string", "enum": []string{"complete", "blocked"}},
			"reply":  map[string]any{"type": "string", "description": "The final user-facing answer or exact blocker."},
		},
		"required": []string{"status", "reply"},
	},
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
	chk *CheckpointManager,
	traceHome string,
	es *EmbeddingStore,
) *LoopResult {
	result := &LoopResult{}
	dedup := make(map[string]string) // tool dedup: key → cached output
	dedupStatus := make(map[string]string)
	lastActionFailed := false
	lastFileOutput := "" // track last file-writing output for completion verification
	noProgress := 0
	successfulObservation := false
	finalizeOnly := false
	checkpointGoal := lastUserContent(messages)

	defer func() {
		decision, reason := "skip", "recall tool not invoked"
		for _, call := range result.ToolCalls {
			if call.Name == "recall" {
				decision, reason = "retrieve", "recall tool invoked"
				break
			}
		}
		notify(obs, "gate", map[string]any{"decision": decision, "reason": reason})
		logTrace(traceHome, "gate", map[string]any{"decision": decision, "reason": reason})
		logTrace(traceHome, "turn_end", map[string]any{"reply": result.Reply, "status": result.Status, "iterations": result.Iterations})
	}()

	// build filter query once — tools needed don't change mid-loop
	filterQuery := system
	if len(messages) > 0 {
		filterQuery += "\n" + messages[len(messages)-1].Content
	}

	for i := 1; i <= maxIter; i++ {
		result.Iterations = i

		// reason: one LLM call
		var resp *LLMResponse
		var err error

		// build filter query from system + last user message
		filterQuery := system
		if len(messages) > 0 {
			filterQuery += "\n" + messages[len(messages)-1].Content
		}

		schemas := []ToolDef{completionTool}
		if !finalizeOnly {
			schemas = append(tools.SchemasFor(filterQuery, es), completionTool)
		}
		if stream {
			resp, err = client.Stream(sessionID, MainModel, messages, maxTokens, system, schemas, func(delta string) {
				notify(obs, "text", map[string]any{"delta": delta})
			})
		} else {
			resp, err = client.Create(sessionID, MainModel, messages, maxTokens, system, schemas)
		}
		if err != nil {
			result.Reply = fmt.Sprintf("(error: %v)", err)
			result.Status = "error"
			return result
		}

		result.TokensIn += resp.Usage.InputTokens
		result.TokensOut += resp.Usage.OutputTokens

		logTrace(traceHome, "llm", map[string]any{"iteration": i, "in": resp.Usage.InputTokens, "out": resp.Usage.OutputTokens})

		notify(obs, "llm", map[string]any{
			"iteration":  i,
			"stopReason": resp.StopReason,
			"usage":      map[string]int{"in": resp.Usage.InputTokens, "out": resp.Usage.OutputTokens},
		})

		// assistant turn joins working memory
		messages = append(messages, Message{Role: "assistant", Content: assembleAssistantContent(resp.Content)})

		// extract tool uses
		toolUses := extractToolUses(resp.Content)
		completionError := "Error: complete_task must be called alone with status complete or blocked and a non-empty reply."

		if resp.Usage.OutputTokens >= maxTokens && (len(toolUses) == 0 || hasInvalidToolInput(toolUses)) {
			noProgress++
			successfulObservation, finalizeOnly = false, false
			logTrace(traceHome, "no_progress", map[string]any{"iteration": i, "count": noProgress, "reason": "output ceiling truncated tool arguments"})
			if noProgress >= maxNoProgress {
				result.Status = "stalled"
				result.Reply = "(I stopped after repeated output truncation. The task remains incomplete.)"
				return result
			}
			messages = append(messages, Message{Role: "user", Content: "Your response hit the output ceiling and the tool arguments were incomplete. Do not retry the same large payload. Use smaller targeted edits, or split a large write into write_file chunks using mode=overwrite once and mode=append afterward."})
			continue
		}

		// Plain text is provisional. Only complete_task can end the turn.
		if len(toolUses) == 0 {
			noProgress++
			notify(obs, "progress", map[string]any{"text": "Still working..."})
			logTrace(traceHome, "no_progress", map[string]any{"iteration": i, "count": noProgress, "reason": "no tool call"})
			if noProgress >= maxNoProgress {
				result.Status = "stalled"
				result.Reply = "(I stopped after repeated no-progress responses before completing the task.)"
				return result
			}
			prompt := "Your previous response did not complete the protocol. Call the next tool, or call complete_task alone with the final reply."
			if successfulObservation {
				prompt = "The previous tool observation was successful. Do not repeat or re-verify it. Call the next distinct tool only if work remains; otherwise call complete_task alone now."
				finalizeOnly = noProgress >= maxNoProgress-1
			}
			messages = append(messages, Message{Role: "user", Content: prompt})
			continue
		}

		if len(toolUses) == 1 && toolUses[0].Name == completionToolName {
			args, _ := toolUses[0].Input.(map[string]any)
			status, _ := args["status"].(string)
			reply, _ := args["reply"].(string)
			status, reply = strings.ToLower(strings.TrimSpace(status)), strings.TrimSpace(reply)
			if (status == "complete" || status == "blocked") && reply != "" && (status == "blocked" || !lastActionFailed) {
				// Verify claimed files exist before accepting completion
				if status == "complete" {
					if correction := verifyFileClaims(reply, lastFileOutput); correction != "" {
						messages = append(messages, Message{Role: "user", Content: correction})
						continue
					}
				}
				result.Status, result.Reply = status, reply
				notify(obs, "completion", map[string]any{"status": status})
				logTrace(traceHome, "task_completion", map[string]any{"status": status})
				return result
			}
			if status == "complete" && lastActionFailed {
				completionError = "Error: the last tool batch failed. Recover with another tool call or finish with status blocked and the exact blocker."
			}
		}

		// act: execute each tool; observe: feed results back
		toolResults := make([]map[string]any, 0)
		var turnImages []string
		batchTools, batchFailed, newActions, cachedActions := 0, false, 0, 0
		for _, tc := range toolUses {
			args, _ := tc.Input.(map[string]any)
			if tc.Name == completionToolName {
				toolResults = append(toolResults, map[string]any{
					"type": "tool_result", "tool_use_id": tc.ID,
					"tool": tc.Name, "status": "error", "cached": false, "content": completionError,
				})
				continue
			}
			batchTools++
			key := dedupKey(tc.Name, args)
			var output, status string
			cached := false
			if cachedOutput, ok := dedup[key]; ok {
				output = "[already executed] " + cachedOutput
				status = dedupStatus[key]
				cachedActions++
				cached = true
			} else {
				newActions++
				raw := tools.Execute(tc.Name, args)
				// view_image returns a data URL; attach it as vision content so
				// the model sees the image instead of megabytes of base64 text.
				if tc.Name == "view_image" && strings.HasPrefix(raw, "data:image/") {
					turnImages = append(turnImages, raw)
					raw = "[image loaded into visual context]"
				}
				status = toolOutputStatus(raw)
				output = prepareToolOutput(traceHome, sessionID, i, tc.Name, raw)
				dedup[key] = output
				dedupStatus[key] = status
			}
			batchFailed = batchFailed || status == "error"
			if tc.Name == "write_file" || (tc.Name == "bash" && strings.Contains(output, "/")) {
				lastFileOutput = output
			}
			output = appendActionReceipt(output, tc.Name, key, status, cached)
			event := map[string]any{"tool": tc.Name, "args": args, "output": output, "status": status, "cached": cached, "action": key, "proof": status == "ok"}
			result.ToolCalls = append(result.ToolCalls, ToolCall{Name: tc.Name, Args: args, Output: output})
			notify(obs, "tool", event)
			trace := map[string]any{"tool": tc.Name, "args": args, "status": status, "cached": cached, "action": key, "proof": status == "ok"}
			if status == "error" {
				trace["output"] = output
			}
			logTrace(traceHome, "tool", trace)
			// checkpoint: save after each tool execution
			if chk != nil {
				toolNames := make([]string, len(result.ToolCalls))
				for j, tc2 := range result.ToolCalls {
					toolNames[j] = tc2.Name
				}
				chk.Save(checkpointGoal, i, toolNames, nil)
			}
			toolResults = append(toolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": tc.ID,
				"tool":        tc.Name,
				"status":      status,
				"cached":      cached,
				"content":     output,
			})
		}
		if batchTools > 0 {
			lastActionFailed = batchFailed
			successfulObservation = !batchFailed
		}
		if newActions > 0 {
			noProgress = 0
			finalizeOnly = false
		} else {
			noProgress++
			reason := "invalid completion"
			if cachedActions > 0 {
				reason = "cached duplicate action"
			}
			logTrace(traceHome, "no_progress", map[string]any{"iteration": i, "count": noProgress, "reason": reason})
		}
		messages = append(messages, Message{Role: "user", Content: formatToolResults(toolResults), Images: turnImages})
		if noProgress >= maxNoProgress {
			result.Status = "stalled"
			result.Reply = "(I stopped after repeatedly attempting an action that had already run.)"
			return result
		}
		if successfulObservation && noProgress >= maxNoProgress-1 {
			finalizeOnly = true
			messages = append(messages, Message{Role: "user", Content: "The requested action already succeeded and produced no new evidence when repeated. Finalize now by calling complete_task alone."})
		}
	}

	result.Status = "iteration_limit"
	result.Reply = "(I hit my iteration limit before completing the task. The task remains incomplete.)"
	// turn_end trace handled by defer
	return result
}

func toolOutputStatus(output string) string {
	text := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(output, "[already executed] ")))
	if strings.HasPrefix(text, "error:") || strings.HasPrefix(text, "error ") ||
		strings.HasPrefix(text, "extension error:") || strings.HasPrefix(text, "failed ") ||
		strings.HasPrefix(text, "search failed:") ||
		(strings.HasPrefix(text, "mcp ") && (strings.Contains(text, " failed:") || strings.Contains(text, "not connected"))) {
		return "error"
	}
	return "ok"
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

// ponytail: global lock, per-account locks if throughput matters

func dedupKey(name string, args map[string]any) string {
	data, _ := json.Marshal(args)
	return name + ":" + string(data)
}

func formatToolResults(results []map[string]any) string {
	var out strings.Builder
	duplicate := false
	for _, r := range results {
		fmt.Fprintf(&out, "[tool_result tool=%v status=%v cached=%v: %v]\n", r["tool"], r["status"], r["cached"], r["content"])
		duplicate = duplicate || r["cached"] == true
	}
	if duplicate {
		out.WriteString("[The exact action already ran. Its cached result is authoritative; do not execute or verify it again.]\n")
	}
	out.WriteString("[Continue only if a requested step remains. After status=ok, call a distinct next tool or call complete_task alone now. Never repeat a successful action.]\n")
	return out.String()
}

// appendActionReceipt makes every tool result auditable and reusable by the
// next observe cycle. The loop owns this protocol; tools only return evidence.
func appendActionReceipt(output, tool, action, status string, cached bool) string {
	receipt, _ := json.Marshal(map[string]any{
		"tool": tool, "action": action, "status": status,
		"proof": status == "ok", "cached": cached,
	})
	return output + "\n[action_receipt " + string(receipt) + "]"
}

// logTrace appends a trace event to traces/YYYY-MM-DD.jsonl
func logTrace(home, eventType string, data map[string]any) {
	if home == "" {
		return
	}
	dir := filepath.Join(home, "traces")
	os.MkdirAll(dir, 0700)
	fname := time.Now().Format("2006-01-02") + ".jsonl"
	entry := map[string]any{
		"type": eventType,
		"ts":   time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range data {
		entry[k] = v
	}
	b, _ := json.Marshal(entry)
	f, err := os.OpenFile(filepath.Join(dir, fname), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(b)
	f.Write([]byte("\n"))
}

// verifyFileClaims checks if the last tool created a file that doesn't exist.
// Only fires when the model claims completion but the file is missing.
func verifyFileClaims(reply string, lastToolOutput string) string {
	// ponytail: check the actual tool output, not the model's natural-language reply
	if lastToolOutput == "" {
		return ""
	}
	// Extract path from tool outputs like "Wrote N bytes to /path" or "/path/to/file"
	re := regexp.MustCompile(`(?:Wrote \d+ bytes to |to )?(/\S+)`)
	matches := re.FindStringSubmatch(lastToolOutput)
	if len(matches) < 2 {
		return ""
	}
	path := matches[1]
	// Clean trailing punctuation
	path = strings.TrimRight(path, ".,;:!?")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Sprintf("Error: you claimed to have written %s but the file does not exist. Use the appropriate tool to actually create the file, then verify before calling complete_task again.", path)
	}
	return ""
}
