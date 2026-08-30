package main

// Mino — loop/models.py — OpenAI-compatible client.
// Speaks Anthropic Messages shape the loop expects, backed by
// OpenAI-style chat.completions API.

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
)

// --- Response types (match Core's SimpleNamespace pattern) ---

type ContentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
}

type UsageInfo struct {
	InputTokens         int     `json:"input_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	CacheReadTokens     int     `json:"cache_read_input_tokens,omitempty"`
	CacheCreationTokens int     `json:"cache_creation_input_tokens,omitempty"`
	CostUSD             float64 `json:"cost_usd,omitempty"` // provider-reported USD, 0 when omitted
}

type LLMResponse struct {
	StopReason string
	Usage      UsageInfo
	Content    []ContentBlock
	FinalText  string
}

// Message is (role, content) — matches Core's dict format.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Images holds data URLs attached to this message only (omni-modal MiMo).
	// Never persisted to history: re-sending base64 every turn would blow the
	// context budget, so images live one turn and history keeps a placeholder.
	Images []string `json:"-"`
}

// --- Client ---

type Client struct {
	apiKey          string
	baseURL         string
	transport       string // wire family: "" / "openai" | "anthropic" | "codex" (declared in providers.json, PRV-001)
	client          *http.Client
	usageDB         *sql.DB  // DATA-001 (#344): usage records land in state.db, not usage.jsonl
	providerRouting []string // openrouter provider routing, e.g. ["DigitalOcean"]
	allowFallbacks  bool     // may fall back outside the routing list (default false = privacy-safe)
}

// NewClient builds the default LLM client. The HTTP timeout is configurable
// via MINO_LLM_TIMEOUT, default 5 minutes (#311, ported from the v2.20-era
// #289 fix): the old 120s default kills heavy non-streaming reasoning calls
// (persona + stub + reasoning_effort=high can exceed 120s before the first
// byte — observed 2026-08-20 blocking the ai-news-daily pilot run).
func NewClient(apiKey, baseURL string) *Client {
	return NewClientTimeout(apiKey, baseURL, envDuration("MINO_LLM_TIMEOUT", 5*time.Minute))
}

// NewClientTimeout builds a client with an explicit HTTP timeout (#289/#311).
func NewClientTimeout(apiKey, baseURL string, timeout time.Duration) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: timeout},
	}
}

func (c *Client) Create(model string, messages []Message, maxTokens int, system string, tools []ToolDef) (*LLMResponse, error) {
	return c.create(context.Background(), model, "", messages, maxTokens, system, tools, false, false, nil)
}

func (c *Client) CreateJSON(model string, messages []Message, maxTokens int, system string) (*LLMResponse, error) {
	return c.create(context.Background(), model, "", messages, maxTokens, system, nil, false, true, nil)
}

func (c *Client) Stream(model string, messages []Message, maxTokens int, system string, tools []ToolDef, onText func(string)) (*LLMResponse, error) {
	return c.create(context.Background(), model, "", messages, maxTokens, system, tools, true, false, onText)
}

// Wedge guard (2026-08-14): the loop's buffered LLM reads must not sit behind
// the transport's gzip decompressor — a gzip-stalled body can block io.ReadAll
// forever, immune to both ctx cancellation and the client timeout (the
// h2+gzip close deadlock wedged a live session until restart). Requests
// declare identity encoding so the decompressor never enters the read path
// and the existing timeout/cancel mechanisms work as designed.

func (c *Client) create(ctx context.Context, model, reasoning string, messages []Message, maxTokens int, system string, tools []ToolDef, stream, jsonOutput bool, onText func(string)) (*LLMResponse, error) {
	return c.createWithRouting(ctx, model, reasoning, messages, maxTokens, system, tools, stream, jsonOutput, onText, c.providerRouting, "")
}

func (c *Client) createWithRouting(ctx context.Context, model, reasoning string, messages []Message, maxTokens int, system string, tools []ToolDef, stream, jsonOutput bool, onText func(string), routing []string, sessionID string) (*LLMResponse, error) {
	if c.isCodex() {
		return c.createCodex(ctx, model, reasoning, messages, system, tools, onText)
	}
	if c.isAnthropic() {
		return c.createAnthropic(ctx, model, messages, maxTokens, system, tools, stream, onText)
	}

	oaiMsgs := make([]map[string]any, 0)
	if system != "" {
		oaiMsgs = append(oaiMsgs, map[string]any{"role": "system", "content": system})
	}
	for _, m := range messages {
		if len(m.Images) == 0 {
			oaiMsgs = append(oaiMsgs, map[string]any{"role": m.Role, "content": m.Content})
			continue
		}
		parts := []map[string]any{{"type": "text", "text": m.Content}}
		for _, img := range m.Images {
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": img}})
		}
		oaiMsgs = append(oaiMsgs, map[string]any{"role": m.Role, "content": parts})
	}

	startTime := time.Now()

	payload := map[string]any{
		"model":                 model,
		"messages":              oaiMsgs,
		"max_completion_tokens": maxTokens,
		"stream":                stream,
	}
	if len(routing) > 0 {
		payload["provider"] = map[string]any{
			"order":           routing,
			"allow_fallbacks": c.allowFallbacks,
		}
	}
	if sessionID != "" {
		payload["session_id"] = sessionID
	}
	if reasoning != "" {
		payload["reasoning_effort"] = reasoning
	}
	if tools != nil {
		funcs := make([]map[string]any, 0)
		for _, t := range tools {
			funcs = append(funcs, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
				},
			})
		}
		payload["tools"] = funcs
	}

	url := c.baseURL + "/chat/completions"
	send := func(withJSON, lastResort bool) (*LLMResponse, error) {
		p := payload
		if withJSON {
			p["response_format"] = map[string]string{"type": "json_object"}
		} else {
			delete(p, "response_format")
		}
		body, _ := json.Marshal(p)
		req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		// Wedge guard (2026-08-14): identity encoding — never let the transport's
		// gzip layer sit between the cancel/timeout machinery and the body read
		// (the h2+gzip close deadlock). Responses are a few KB; bandwidth is
		// irrelevant, a hang is not.
		req.Header.Set("Accept-Encoding", "identity")

		resp, err := c.client.Do(req)
		if err != nil {
			slog.Error("llm request failed", "error", err)
			return nil, err
		}
		defer resp.Body.Close()

		if stream {
			resp, err := parseSSEStream(resp.Body, onText)
			c.logUsage(ctx, model, resp, startTime)
			return resp, err
		}
		resp2, err := parseResponse(resp.Body, jsonOutput && !lastResort)
		c.logUsage(ctx, model, resp2, startTime)
		return resp2, err
	}

	if jsonOutput {
		// Reasoning models (DeepSeek v4 flash) spiral into endless reasoning on
		// large prompts — content stays null, finish:length at any token budget,
		// in json or plain mode. JSON-mode background tasks disable reasoning by
		// default; the tolerant parsers extract facts from a normal reply.
		payload["reasoning"] = map[string]bool{"enabled": false}
		// Retry once without response_format too: some models null content when
		// forced into json_object mode.
		if r, err := send(true, false); err == nil {
			return r, nil
		} else if r2, err2 := send(false, false); err2 == nil {
			return r2, nil
		} else {
			// Some endpoints require the opposite (GLM 5.3 flash, 2026-08-30):
			// they reject a disabled reasoning field outright ("Reasoning is
			// mandatory for this endpoint and cannot be disabled") instead of
			// just ignoring it. Last resort: drop the override and let the
			// model use its own default, same two response_format attempts.
			delete(payload, "reasoning")
			if r3, err3 := send(true, false); err3 == nil {
				return r3, nil
			} else if r4, err4 := send(false, true); err4 == nil {
				// GLM 5.3 flash puts its JSON answer under reasoning even with
				// reasoning enabled and response_format dropped, so content stays
				// empty on every prior attempt — the true last resort promotes
				// reasoning into content like the non-JSON path already does
				// (tolerant JSON-mode parsers extract the object from surrounding
				// prose, so a reasoning-trace-wrapped answer still parses).
				return r4, nil
			} else {
				return nil, err4
			}
		}
	}
	return send(false, false)
}

func parseResponse(r io.Reader, jsonMode bool) (*LLMResponse, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning_content"`
				// Some OpenAI-compatible providers (e.g. qwen via OpenRouter)
				// surface the thinking trace under "reasoning" instead of
				// DeepSeek's "reasoning_content". Capture both (see #163).
				ReasoningAlt string `json:"reasoning"`
				ToolCalls    []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens       int     `json:"prompt_tokens"`
			CompletionTokens   int     `json:"completion_tokens"`
			CostUSD            float64 `json:"cost"`
			PromptTokenDetails struct {
				CachedTokens     int `json:"cached_tokens"`
				CacheWriteTokens int `json:"cache_write_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse json: %w, body: %.200s", err, string(data))
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response, body: %.500s", string(data))
	}

	choice := result.Choices[0]
	content := choice.Message.Content
	reasoning := choice.Message.Reasoning
	if reasoning == "" {
		reasoning = choice.Message.ReasoningAlt
	}
	// MiMo: answers go to reasoning, content is empty. Some providers send
	// thinking under "reasoning" and leave content null — without capturing
	// both names the response was dropped wholesale as "empty model response"
	// (2026-08-12, #163: qwen fallback streamed reasoning-only, empty content).
	// JSON mode excludes this during createWithRouting's retry ladder: a
	// reasoning-only reply must stay "empty" so each response_format/reasoning
	// variation gets a real chance to work. The ladder's true last resort
	// passes jsonMode=false here on purpose (GLM 5.3 flash, #436 live eval:
	// every JSON-mode variation still lands its answer in reasoning) so the
	// promotion below finally applies instead of failing outright.
	if !jsonMode && content == "" && reasoning != "" {
		content = reasoning
	}
	blocks := make([]ContentBlock, 0)
	if content != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: content})
	}
	for _, tc := range choice.Message.ToolCalls {
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			// Model emitted malformed native tool_calls JSON (same failure class
			// as the text-marker \' bug). Don't execute with nil args: hand the
			// raw string back as a tool result so the model can self-correct.
			slog.Warn("unparseable native tool_call arguments", "tool", tc.Function.Name, "arguments", tc.Function.Arguments)
			args = map[string]any{"__raw_arguments__": tc.Function.Arguments}
		}
		blocks = append(blocks, ContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: args,
		})
	}
	if len(blocks) == 0 {
		slog.Warn("llm response empty content", "body", string(data[:min(400, len(data))]))
		return nil, fmt.Errorf("empty model response")
	}

	stopReason := choice.FinishReason
	if len(choice.Message.ToolCalls) > 0 {
		stopReason = "tool_use"
	}

	return &LLMResponse{
		StopReason: stopReason,
		Usage: UsageInfo{
			InputTokens:         result.Usage.PromptTokens,
			OutputTokens:        result.Usage.CompletionTokens,
			CacheReadTokens:     result.Usage.PromptTokenDetails.CachedTokens,
			CacheCreationTokens: result.Usage.PromptTokenDetails.CacheWriteTokens,
			CostUSD:             result.Usage.CostUSD,
		},
		Content:   blocks,
		FinalText: content,
	}, nil
}

// --- SSE stream parser (matches Core's _OpenAIStream) ---

func parseSSEStream(r io.Reader, onText func(string)) (*LLMResponse, error) {
	var fullText strings.Builder
	tools := make(map[int]*streamTool)
	var usage UsageInfo

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					// Some providers send thinking under "reasoning" (see #163).
					ReasoningAlt string `json:"reasoning"`
					ToolCalls    []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens       int `json:"prompt_tokens"`
				CompletionTokens   int `json:"completion_tokens"`
				PromptTokenDetails struct {
					CachedTokens     int `json:"cached_tokens"`
					CacheWriteTokens int `json:"cache_write_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, fmt.Errorf("parse stream chunk: %w", err)
		}

		if chunk.Usage != nil {
			usage.InputTokens = chunk.Usage.PromptTokens
			usage.OutputTokens = chunk.Usage.CompletionTokens
			usage.CacheReadTokens = chunk.Usage.PromptTokenDetails.CachedTokens
			usage.CacheCreationTokens = chunk.Usage.PromptTokenDetails.CacheWriteTokens
		}

		for _, choice := range chunk.Choices {
			text := choice.Delta.Content
			if text == "" {
				text = choice.Delta.ReasoningContent
			}
			if text == "" {
				text = choice.Delta.ReasoningAlt
			}
			if text != "" {
				fullText.WriteString(text)
				if onText != nil {
					onText(text)
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				st, ok := tools[tc.Index]
				if !ok {
					st = &streamTool{}
					tools[tc.Index] = st
				}
				if tc.ID != "" {
					st.ID = tc.ID
				}
				if tc.Function.Name != "" {
					st.Name = tc.Function.Name
				}
				st.Args += tc.Function.Arguments
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	blocks := make([]ContentBlock, 0)
	if text := fullText.String(); text != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: text})
	}
	for _, index := range sortedIndexes(tools) {
		st := tools[index]
		var args map[string]any
		json.Unmarshal([]byte(st.Args), &args)
		blocks = append(blocks, ContentBlock{
			Type:  "tool_use",
			ID:    st.ID,
			Name:  st.Name,
			Input: args,
		})
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("empty streamed model response")
	}

	stopReason := "end_turn"
	if len(tools) > 0 {
		stopReason = "tool_use"
	}

	return &LLMResponse{
		StopReason: stopReason,
		Usage:      usage,
		Content:    blocks,
	}, nil
}

type streamTool struct {
	ID   string
	Name string
	Args string
}

func sortedIndexes[T any](items map[int]T) []int {
	indexes := make([]int, 0, len(items))
	for index := range items {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	return indexes
}

// --- Tool definition (matches Core's input_schema dict) ---

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}

// --- Anthropic Messages API adapter ---

func (c *Client) isAnthropic() bool {
	return c.transport == "anthropic"
}

func (c *Client) createAnthropic(ctx context.Context, model string, messages []Message, maxTokens int, system string, tools []ToolDef, stream bool, onText func(string)) (*LLMResponse, error) {
	// build Anthropic Messages API payload
	var anthropicTools []map[string]any
	for _, t := range tools {
		anthropicTools = append(anthropicTools, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.Parameters,
		})
	}

	// Prompt caching: cache system + all but the last 2 messages.
	// The last user+assistant pair changes each turn; everything before is stable.
	cacheBreak := len(messages) - 2
	if cacheBreak < 0 {
		cacheBreak = 0
	}

	anthropicMsgs := make([]map[string]any, 0)
	for i, m := range messages {
		content := []map[string]any{{"type": "text", "text": m.Content}}
		if len(m.Images) > 0 {
			for _, img := range m.Images {
				if strings.HasPrefix(img, "data:image/") {
					parts := strings.SplitN(img, ";base64,", 2)
					mediaType := strings.TrimPrefix(parts[0], "data:")
					content = append(content, map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": mediaType,
							"data":       parts[1],
						},
					})
				}
			}
		}
		// Mark last content block as cache breakpoint for historical messages
		if i < cacheBreak {
			content[len(content)-1]["cache_control"] = map[string]string{"type": "ephemeral"}
		}
		role := m.Role
		if role == "assistant" {
			role = "assistant"
		}
		anthropicMsgs = append(anthropicMsgs, map[string]any{"role": role, "content": content})
	}

	payload := map[string]any{
		"model":      model,
		"messages":   anthropicMsgs,
		"max_tokens": maxTokens,
	}
	if system != "" {
		// Wrap system in content block with cache_control for prompt caching
		payload["system"] = []map[string]any{
			{"type": "text", "text": system, "cache_control": map[string]string{"type": "ephemeral"}},
		}
	}
	if len(anthropicTools) > 0 {
		payload["tools"] = anthropicTools
	}
	if stream {
		payload["stream"] = true
	}

	startTime := time.Now()
	body, _ := json.Marshal(payload)
	url := c.baseURL + "/v1/messages"
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.client.Do(req)
	if err != nil {
		slog.Error("anthropic request failed", "error", err)
		return nil, err
	}
	defer resp.Body.Close()

	if stream {
		resp, err := parseAnthropicStream(resp.Body, onText)
		c.logUsage(ctx, model, resp, startTime)
		return resp, err
	}
	resp2, err := parseAnthropicResponse(resp.Body)
	c.logUsage(ctx, model, resp2, startTime)
	return resp2, err
}

func parseAnthropicResponse(r io.Reader) (*LLMResponse, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var result struct {
		Content []struct {
			Type  string `json:"type"`
			Text  string `json:"text,omitempty"`
			ID    string `json:"id,omitempty"`
			Name  string `json:"name,omitempty"`
			Input any    `json:"input,omitempty"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens         int `json:"input_tokens"`
			OutputTokens        int `json:"output_tokens"`
			CacheReadTokens     int `json:"cache_read_input_tokens"`
			CacheCreationTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse anthropic json: %w, body: %.200s", err, string(data))
	}

	blocks := make([]ContentBlock, 0)
	var finalText string
	for _, c := range result.Content {
		switch c.Type {
		case "text":
			blocks = append(blocks, ContentBlock{Type: "text", Text: c.Text})
			finalText += c.Text
		case "tool_use":
			blocks = append(blocks, ContentBlock{Type: "tool_use", ID: c.ID, Name: c.Name, Input: c.Input})
		}
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("empty anthropic response")
	}

	stopReason := result.StopReason

	return &LLMResponse{
		StopReason: stopReason,
		Usage: UsageInfo{
			InputTokens:         result.Usage.InputTokens,
			OutputTokens:        result.Usage.OutputTokens,
			CacheReadTokens:     result.Usage.CacheReadTokens,
			CacheCreationTokens: result.Usage.CacheCreationTokens,
		},
		Content:   blocks,
		FinalText: finalText,
	}, nil
}

func parseAnthropicStream(r io.Reader, onText func(string)) (*LLMResponse, error) {
	var fullText strings.Builder
	var usage UsageInfo
	type streamTool struct {
		ID    string
		Name  string
		Input string
	}
	tools := make(map[int]*streamTool)
	currentBlockIndex := -1

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var event struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
			ContentBlock struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				ID    string `json:"id"`
				Name  string `json:"name"`
				Input any    `json:"input"`
			} `json:"content_block"`
			Usage *struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Message struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return nil, fmt.Errorf("parse anthropic stream event: %w", err)
		}

		switch event.Type {
		case "content_block_start":
			if event.ContentBlock.Type == "text" {
				currentBlockIndex = event.Index
			} else if event.ContentBlock.Type == "tool_use" {
				currentBlockIndex = event.Index
				tools[event.Index] = &streamTool{
					ID:   event.ContentBlock.ID,
					Name: event.ContentBlock.Name,
				}
			}
		case "content_block_delta":
			if event.Delta.Type == "text_delta" {
				fullText.WriteString(event.Delta.Text)
				if onText != nil {
					onText(event.Delta.Text)
				}
			} else if event.Delta.Type == "input_json_delta" {
				if st, ok := tools[currentBlockIndex]; ok {
					st.Input += event.Delta.Text
				}
			}
		case "content_block_stop":
			currentBlockIndex = -1
		case "message_delta":
			if event.Usage != nil {
				usage.InputTokens = event.Usage.InputTokens
				usage.OutputTokens = event.Usage.OutputTokens
			}
		case "message_stop":
			// stream complete
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// collapse text blocks into one
	var textBlocks []ContentBlock
	if finalText := fullText.String(); finalText != "" {
		textBlocks = append(textBlocks, ContentBlock{Type: "text", Text: finalText})
	}
	for _, index := range sortedIndexes(tools) {
		st := tools[index]
		var input map[string]any
		json.Unmarshal([]byte(st.Input), &input)
		textBlocks = append(textBlocks, ContentBlock{Type: "tool_use", ID: st.ID, Name: st.Name, Input: input})
	}
	if len(textBlocks) == 0 {
		return nil, fmt.Errorf("empty anthropic streamed response")
	}

	stopReason := "end_turn"
	if len(tools) > 0 {
		stopReason = "tool_use"
	}

	return &LLMResponse{
		StopReason: stopReason,
		Usage:      usage,
		Content:    textBlocks,
		FinalText:  fullText.String(),
	}, nil
}

// logUsage inserts a usage record into state.db (Core format fields kept as
// columns — DATA-001, #344). Failure warns loudly, never drops silently
// (#200: a silent drop blinded cost accounting for ~15 min on 2026-08-15).
func (c *Client) logUsage(ctx context.Context, model string, resp *LLMResponse, startTime time.Time) {
	if resp == nil || c.usageDB == nil {
		return
	}
	sid := ""
	if v := ctx.Value(sessionIDKey{}); v != nil {
		sid, _ = v.(string)
	}
	var cost any
	if resp.Usage.CostUSD > 0 {
		cost = resp.Usage.CostUSD
	}
	_, err := c.usageDB.Exec(`INSERT INTO usage_log
		(ts, provider, model, session_id, in_tokens, out_tokens, cache_read, cache_write, latency_ms, cost_usd)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339), "openai", model, sid,
		resp.Usage.InputTokens, resp.Usage.OutputTokens,
		resp.Usage.CacheReadTokens, resp.Usage.CacheCreationTokens,
		time.Since(startTime).Milliseconds(), cost)
	if err != nil {
		slog.Warn("usage log insert failed, dropping record", "error", err)
	}
}
