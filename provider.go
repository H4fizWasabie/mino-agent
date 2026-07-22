package main

// Mino — loop/models.py — OpenAI-compatible client.
// Speaks Anthropic Messages shape the loop expects, backed by
// OpenAI-style chat.completions API.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
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
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
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
	apiKey       string
	baseURL      string
	client       *http.Client
	usageLogPath string
}

func NewClient(apiKey, baseURL string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (c *Client) Create(model string, messages []Message, maxTokens int, system string, tools []ToolDef) (*LLMResponse, error) {
	return c.create(model, "", messages, maxTokens, system, tools, false, nil)
}

func (c *Client) Stream(model string, messages []Message, maxTokens int, system string, tools []ToolDef, onText func(string)) (*LLMResponse, error) {
	return c.create(model, "", messages, maxTokens, system, tools, true, onText)
}

func (c *Client) create(model, reasoning string, messages []Message, maxTokens int, system string, tools []ToolDef, stream bool, onText func(string)) (*LLMResponse, error) {
	if c.isCodex() {
		return c.createCodex(model, reasoning, messages, system, tools, onText)
	}
	if c.isAnthropic() {
		return c.createAnthropic(model, messages, maxTokens, system, tools, stream, onText)
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

	body, _ := json.Marshal(payload)
	url := c.baseURL + "/chat/completions"
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if c.isOpenRouter() {
		req.Header.Set("HTTP-Referer", "https://github.com/H4fizWasabie/mino-agent")
		req.Header.Set("X-Title", "Mino")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		slog.Error("llm request failed", "error", err)
		return nil, err
	}
	defer resp.Body.Close()

	if stream {
		resp, err := parseSSEStream(resp.Body, onText)
		c.logUsage(model, resp, startTime)
		return resp, err
	}
	resp2, err := parseResponse(resp.Body)
	c.logUsage(model, resp2, startTime)
	return resp2, err
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
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
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
		json.Unmarshal([]byte(tc.Function.Arguments), &args)
		blocks = append(blocks, ContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: args,
		})
	}

	stopReason := choice.FinishReason
	if len(choice.Message.ToolCalls) > 0 {
		stopReason = "tool_use"
	}

	return &LLMResponse{
		StopReason: stopReason,
		Usage: UsageInfo{
			InputTokens:  result.Usage.PromptTokens,
			OutputTokens: result.Usage.CompletionTokens,
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
					ToolCalls []struct {
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
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		json.Unmarshal([]byte(data), &chunk)

		if chunk.Usage != nil {
			usage.InputTokens = chunk.Usage.PromptTokens
			usage.OutputTokens = chunk.Usage.CompletionTokens
		}

		for _, choice := range chunk.Choices {
			deltaText := choice.Delta.Content
			if deltaText == "" && choice.Delta.ReasoningContent != "" {
				deltaText = choice.Delta.ReasoningContent
			}
			if deltaText != "" {
				fullText.WriteString(deltaText)
				if onText != nil {
					onText(deltaText)
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

	blocks := make([]ContentBlock, 0)
	if text := fullText.String(); text != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: text})
	}
	for _, st := range tools {
		var args map[string]any
		json.Unmarshal([]byte(st.Args), &args)
		blocks = append(blocks, ContentBlock{
			Type:  "tool_use",
			ID:    st.ID,
			Name:  st.Name,
			Input: args,
		})
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

func (c *Client) isOpenRouter() bool {
	return strings.Contains(c.baseURL, "openrouter.ai")
}

func (c *Client) createAnthropic(model string, messages []Message, maxTokens int, system string, tools []ToolDef, stream bool, onText func(string)) (*LLMResponse, error) {
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

	anthropicMsgs := make([]map[string]any, 0)
	for _, m := range messages {
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
		payload["system"] = system
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
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
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
		c.logUsage(model, resp, startTime)
		return resp, err
	}
	resp2, err := parseAnthropicResponse(resp.Body)
	c.logUsage(model, resp2, startTime)
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
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
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

	stopReason := result.StopReason

	return &LLMResponse{
		StopReason: stopReason,
		Usage: UsageInfo{
			InputTokens:  result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens,
		},
		Content:   blocks,
		FinalText: finalText,
	}, nil
}

func parseAnthropicStream(r io.Reader, onText func(string)) (*LLMResponse, error) {
	var fullText strings.Builder
	var blocks []ContentBlock
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
				Type       string `json:"type"`
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
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
				StopReason string `json:"stop_reason"`
				Usage      struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		json.Unmarshal([]byte(data), &event)

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
				blocks = append(blocks, ContentBlock{Type: "text", Text: event.Delta.Text})
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

	// collapse text blocks into one
	var textBlocks []ContentBlock
	var finalText string
	for _, b := range blocks {
		if b.Type == "text" {
			finalText += b.Text
		}
	}
	if finalText != "" {
		textBlocks = append(textBlocks, ContentBlock{Type: "text", Text: finalText})
	}
	for _, st := range tools {
		var input map[string]any
		json.Unmarshal([]byte(st.Input), &input)
		textBlocks = append(textBlocks, ContentBlock{Type: "tool_use", ID: st.ID, Name: st.Name, Input: input})
	}

	stopReason := "end_turn"
	if len(tools) > 0 {
		stopReason = "tool_use"
	}

	return &LLMResponse{
		StopReason: stopReason,
		Usage:      usage,
		Content:    textBlocks,
		FinalText:  finalText,
	}, nil
}

// logUsage appends a usage record to usage.jsonl (Core format)
func (c *Client) logUsage(model string, resp *LLMResponse, startTime time.Time) {
	if resp == nil || c.usageLogPath == "" {
		return
	}
	record := map[string]any{
		"ts":         time.Now().UTC().Format(time.RFC3339),
		"provider":   "openai",
		"model":      model,
		"in":         resp.Usage.InputTokens,
		"out":        resp.Usage.OutputTokens,
		"latency_ms": time.Since(startTime).Milliseconds(),
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
