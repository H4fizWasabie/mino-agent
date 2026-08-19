package main

import (
	"context"
)

// fakeClient plays back scripted LLMResponses — the "model" for offline tests.
type fakeClient struct {
	script   []*LLMResponse
	pos      int
	tools    []ToolDef
	messages [][]Message
	toolSets [][]ToolDef
	roles    []ModelRole
}

func (f *fakeClient) Create(session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef) (*LLMResponse, error) {
	f.tools = tools
	f.roles = append(f.roles, role)
	f.messages = append(f.messages, append([]Message(nil), messages...))
	f.toolSets = append(f.toolSets, append([]ToolDef(nil), tools...))
	if f.pos >= len(f.script) {
		return scriptedResp([]ContentBlock{textBlock("out of script")}, "stop"), nil
	}
	r := f.script[f.pos]
	f.pos++
	return r, nil
}

func (f *fakeClient) Stream(session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef, onText func(string)) (*LLMResponse, error) {
	return f.Create(session, role, messages, maxTokens, system, tools)
}

func (f *fakeClient) CreateContext(ctx context.Context, session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef) (*LLMResponse, error) {
	return f.Create(session, role, messages, maxTokens, system, tools)
}

func (f *fakeClient) CreateContextNoReasoning(ctx context.Context, session string, role ModelRole, messages []Message, maxTokens int, system string) (*LLMResponse, error) {
	return f.Create(session, role, messages, maxTokens, system, nil)
}

// helpers to build scripted responses
func textBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

func toolBlock(name string, args map[string]any) ContentBlock {
	return ContentBlock{Type: "tool_use", ID: "tu_1", Name: name, Input: args}
}

func scriptedResp(blocks []ContentBlock, stopReason string) *LLMResponse {
	return &LLMResponse{
		StopReason: stopReason,
		Usage:      UsageInfo{InputTokens: 10, OutputTokens: 10},
		Content:    blocks,
	}
}

// --- Tests ---
