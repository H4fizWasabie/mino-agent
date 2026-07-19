package main

// Mino — loop/agent.py — Core's exact loop.
// The loop is ~95 lines: observe → reason → act → repeat.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type LoopResult struct {
	Reply      string
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
		logTrace(traceHome, "turn_end", map[string]any{"reply": result.Reply, "iterations": result.Iterations})
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

		if stream {
			resp, err = client.Stream(sessionID, MainModel, messages, maxTokens, system, tools.SchemasFor(filterQuery, es), func(delta string) {
				notify(obs, "text", map[string]any{"delta": delta})
			})
		} else {
			resp, err = client.Create(sessionID, MainModel, messages, maxTokens, system, tools.SchemasFor(filterQuery, es))
		}
		if err != nil {
			result.Reply = fmt.Sprintf("(error: %v)", err)
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

		// guardrail 1: no tool calls → reply to human
		if len(toolUses) == 0 {
			result.Reply = extractText(resp.Content)
			return result
		}

		// act: execute each tool; observe: feed results back
		toolResults := make([]map[string]any, 0)
		var turnImages []string
		for _, tc := range toolUses {
			args, _ := tc.Input.(map[string]any)
			key := dedupKey(tc.Name, args)
			var output, status string
			if cached, ok := dedup[key]; ok {
				output = "[already executed] " + cached
				status = dedupStatus[key]
			} else {
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
			event := map[string]any{"tool": tc.Name, "args": args, "output": output, "status": status}
			result.ToolCalls = append(result.ToolCalls, ToolCall{Name: tc.Name, Args: args, Output: output})
			notify(obs, "tool", event)
			trace := map[string]any{"tool": tc.Name, "args": args, "status": status}
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
				chk.Save(system, i, toolNames, nil)
			}
			toolResults = append(toolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": tc.ID,
				"content":     output,
			})
		}
		messages = append(messages, Message{Role: "user", Content: formatToolResults(toolResults), Images: turnImages})
	}

	result.Reply = "(I hit my iteration limit — try breaking the request into smaller steps.)"
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

func assembleAssistantContent(blocks []ContentBlock) string {
	// simplified for OpenAI wire format — just the text
	return extractText(blocks)
}

// ponytail: global lock, per-account locks if throughput matters

func dedupKey(name string, args map[string]any) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(name)
	sb.WriteByte(':')
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(fmt.Sprint(args[k]))
		sb.WriteByte(',')
	}
	return sb.String()
}

func formatToolResults(results []map[string]any) string {
	var out string
	for _, r := range results {
		out += fmt.Sprintf("[tool_result: %v]\n", r["content"])
	}
	return out
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
