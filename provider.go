package main

// Mino — loop/models.py — OpenAI-compatible client.
// Speaks Anthropic Messages shape the loop expects, backed by
// OpenAI-style chat.completions API.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
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
	client          *http.Client
	usageLogPath    string
	providerRouting []string // openrouter provider routing, e.g. ["DigitalOcean"]
}

func NewClient(apiKey, baseURL string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 120 * time.Second},
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
			"allow_fallbacks": true,
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
	send := func(withJSON bool) (*LLMResponse, error) {
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
		resp2, err := parseResponse(resp.Body)
		c.logUsage(ctx, model, resp2, startTime)
		return resp2, err
	}

	if jsonOutput {
		// Reasoning models (DeepSeek v4 flash) spiral into endless reasoning on
		// large prompts — content stays null, finish:length at any token budget,
		// in json or plain mode. JSON-mode background tasks disable reasoning
		// entirely; the tolerant parsers extract facts from a normal reply.
		payload["reasoning"] = map[string]bool{"enabled": false}
		// Retry once without response_format too: some models null content when
		// forced into json_object mode.
		if r, err := send(true); err == nil {
			return r, nil
		} else if r2, err2 := send(false); err2 == nil {
			return r2, nil
		} else {
			return nil, err
		}
	}
	return send(false)
}

func parseResponse(r io.Reader) (*LLMResponse, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning_content"`
				ToolCalls []struct {
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
	// MiMo: answers go to reasoning, content is empty
	if content == "" && reasoning != "" {
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
					ToolCalls        []struct {
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
	return strings.Contains(c.baseURL, "anthropic.com")
}

func (c *Client) createAnthropic(ctx context.Context, model string, messages []Message, maxTokens int, system string, tools []ToolDef, stream bool, onText func(string)) (*LLMResponse, error) {
	// build Anthropic Messages API payload
	var anthropicTools []map[string]any
	if tools != nil {
		for _, t := range tools {
			anthropicTools = append(anthropicTools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.Parameters,
			})
		}
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

// logUsage appends a usage record to usage.jsonl (Core format)
func (c *Client) logUsage(ctx context.Context, model string, resp *LLMResponse, startTime time.Time) {
	if resp == nil || c.usageLogPath == "" {
		return
	}
	sid := ""
	if v := ctx.Value(sessionIDKey{}); v != nil {
		sid, _ = v.(string)
	}
	record := map[string]any{
		"ts":          time.Now().UTC().Format(time.RFC3339),
		"provider":    "openai",
		"model":       model,
		"session_id":  sid,
		"in":          resp.Usage.InputTokens,
		"out":         resp.Usage.OutputTokens,
		"cache_read":  resp.Usage.CacheReadTokens,
		"cache_write": resp.Usage.CacheCreationTokens,
		"latency_ms":  time.Since(startTime).Milliseconds(),
	}
	// Real provider-reported USD (issue #76): usage.jsonl is the source of
	// truth for spend; the price table is only a fallback for providers that
	// omit cost. Kept out of the record when 0 so legacy consumers see the
	// same shape and the fallback path stays exercisable.
	if resp.Usage.CostUSD > 0 {
		record["cost_usd"] = resp.Usage.CostUSD
	}
	data, _ := json.Marshal(record)
	f, err := os.OpenFile(c.usageLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(data)
	f.Write([]byte("\n"))
}
