package main

// Mino — loop/agent.py — Core's exact loop.
// The loop remains observe → plan → act once → record proof → observe → repeat.

import (
	"context"
	"encoding/json"
	"fmt"
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
	rbDir := filepath.Join(traceHome, "rollback", sessionID)
	ctx = context.WithValue(ctx, rollbackDirKey{}, rbDir)
	result := &LoopResult{}

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

	schemas := tools.Schemas()

	for i := 1; i <= maxIter; i++ {
		if ctx.Err() != nil {
			result.Status = "cancelled"
			result.Reply = "Stopped."
			return result
		}
		result.Iterations = i

		_, llmCancel := context.WithTimeout(ctx, 90*time.Second)
		resp, err := client.Create(sessionID, MainModel, messages, maxTokens, system, schemas)
		llmCancel()
		if err != nil {
			if ctx.Err() != nil {
				result.Status = "cancelled"
				result.Reply = "Stopped."
				return result
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

		messages = append(messages, Message{Role: "assistant", Content: assembleAssistantContent(resp.Content)})

		toolUses := extractToolUses(resp.Content)
		if len(toolUses) == 0 {
			toolUses = extractTextToolUses(extractText(resp.Content))
		}

		// No tool calls = LLM is done
		if len(toolUses) == 0 {
			result.Status = "complete"
			result.Reply = extractText(resp.Content)
			return result
		}

		// Execute tools and feed results back
		toolResults := make([]map[string]any, 0)
		var turnImages []string
		for _, tc := range toolUses {
			args, _ := tc.Input.(map[string]any)
			raw := tools.ExecuteContext(ctx, tc.Name, args)
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

			notify(obs, "tool", map[string]any{"tool": tc.Name, "args": args, "status": toolOutputStatus(raw)})
			logTrace(traceHome, "tool", map[string]any{"tool": tc.Name, "args": args, "status": toolOutputStatus(raw)})

			toolResults = append(toolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": tc.ID,
				"tool":        tc.Name,
				"content":     output,
			})
		}
		messages = append(messages, Message{Role: "user", Content: formatToolResults(toolResults), Images: turnImages})
	}

	result.Status = "iteration_limit"
	result.Reply = "(stopped after " + fmt.Sprint(maxIter) + " iterations)"
	return result
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
func extractTextToolUses(text string) []ContentBlock {
	var uses []ContentBlock
	marker := "[tool_call:"
	for {
		idx := strings.Index(text, marker)
		if idx == -1 {
			break
		}
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
		if err := json.Unmarshal([]byte(argsJSON), &args); err == nil {
			uses = append(uses, ContentBlock{
				Type:  "tool_use",
				ID:    fmt.Sprintf("txt_%d", len(uses)),
				Name:  name,
				Input: args,
			})
		}
	}
	return uses
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
	path := filepath.Join(dir, safePath(tool)+".txt")
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
