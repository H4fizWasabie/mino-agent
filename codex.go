package main

// ChatGPT subscription transport for Codex. Authentication is OAuth, while
// inference uses the Codex Responses endpoint rather than chat/completions.

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (c *Client) isCodex() bool {
	return strings.Contains(c.baseURL, "chatgpt.com/backend-api") || strings.HasSuffix(strings.TrimRight(c.baseURL, "/"), "/codex")
}

func codexAccountID(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid codex access token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode codex access token: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parse codex access token: %w", err)
	}
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	accountID, _ := auth["chatgpt_account_id"].(string)
	if accountID == "" {
		return "", fmt.Errorf("codex access token has no ChatGPT account ID")
	}
	return accountID, nil
}

func (c *Client) createCodex(model string, messages []Message, maxTokens int, system string, tools []ToolDef, onText func(string)) (*LLMResponse, error) {
	accountID, err := codexAccountID(c.apiKey)
	if err != nil {
		return nil, err
	}
	input := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		contentType := "input_text"
		if message.Role == "assistant" {
			contentType = "output_text"
		}
		content := []map[string]any{{"type": contentType, "text": message.Content}}
		for _, image := range message.Images {
			content = append(content, map[string]any{"type": "input_image", "image_url": image})
		}
		input = append(input, map[string]any{"role": message.Role, "content": content})
	}
	payload := map[string]any{
		"model": model, "store": false, "stream": true, "instructions": system,
		"input": input, "max_output_tokens": maxTokens, "tool_choice": "auto", "parallel_tool_calls": true,
	}
	if len(tools) > 0 {
		definitions := make([]map[string]any, 0, len(tools))
		for _, tool := range tools {
			definitions = append(definitions, map[string]any{
				"type": "function", "name": tool.Name, "description": tool.Description,
				"parameters": tool.Parameters, "strict": false,
			})
		}
		payload["tools"] = definitions
	}
	body, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(c.baseURL, "/")
	if !strings.HasSuffix(endpoint, "/responses") {
		endpoint += "/responses"
	}
	req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("chatgpt-account-id", accountID)
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", "mino")
	req.Header.Set("User-Agent", "mino")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("codex response failed (%d): %.500s", resp.StatusCode, data)
	}
	return parseCodexSSE(resp.Body, onText)
}

func parseCodexSSE(r io.Reader, onText func(string)) (*LLMResponse, error) {
	var text strings.Builder
	var blocks []ContentBlock
	var usage UsageInfo
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Item struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"item"`
			Response struct {
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return nil, fmt.Errorf("parse codex event: %w", err)
		}
		switch event.Type {
		case "response.output_text.delta":
			text.WriteString(event.Delta)
			if onText != nil {
				onText(event.Delta)
			}
		case "response.output_item.done":
			if event.Item.Type == "function_call" {
				var input map[string]any
				if err := json.Unmarshal([]byte(event.Item.Arguments), &input); err != nil {
					return nil, fmt.Errorf("parse codex tool arguments: %w", err)
				}
				id := event.Item.CallID
				if id == "" {
					id = event.Item.ID
				}
				blocks = append(blocks, ContentBlock{Type: "tool_use", ID: id, Name: event.Item.Name, Input: input})
			}
		case "response.completed", "response.incomplete":
			usage = UsageInfo{InputTokens: event.Response.Usage.InputTokens, OutputTokens: event.Response.Usage.OutputTokens}
		case "error":
			return nil, fmt.Errorf("codex error: %s", event.Error.Message)
		case "response.failed":
			message := "request failed"
			if event.Response.Error != nil && event.Response.Error.Message != "" {
				message = event.Response.Error.Message
			}
			return nil, fmt.Errorf("codex error: %s", message)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	finalText := text.String()
	if finalText != "" {
		blocks = append([]ContentBlock{{Type: "text", Text: finalText}}, blocks...)
	}
	stopReason := "end_turn"
	if len(extractToolUses(blocks)) > 0 {
		stopReason = "tool_use"
	}
	return &LLMResponse{StopReason: stopReason, Usage: usage, Content: blocks, FinalText: finalText}, nil
}
