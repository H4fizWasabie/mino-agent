package main

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeClient plays back scripted LLMResponses — the "model" for offline tests.
type fakeClient struct {
	script   []*LLMResponse
	pos      int
	tools    []ToolDef
	messages [][]Message
	toolSets [][]ToolDef
}

type streamingFake struct{ *fakeClient }

func (f *streamingFake) Stream(session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef, onText func(string)) (*LLMResponse, error) {
	onText("Let me read it...")
	return f.Create(session, role, messages, maxTokens, system, tools)
}

func (f *fakeClient) Create(session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef) (*LLMResponse, error) {
	f.tools = tools
	f.messages = append(f.messages, append([]Message(nil), messages...))
	f.toolSets = append(f.toolSets, append([]ToolDef(nil), tools...))
	if f.pos >= len(f.script) {
		return scriptedResp([]ContentBlock{finishBlock("complete", "out of script")}, "tool_use"), nil
	}
	r := f.script[f.pos]
	f.pos++
	return r, nil
}

func (f *fakeClient) Stream(session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef, onText func(string)) (*LLMResponse, error) {
	return f.Create(session, role, messages, maxTokens, system, tools)
}

// helpers to build scripted responses
func textBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

func toolBlock(name string, args map[string]any) ContentBlock {
	return ContentBlock{Type: "tool_use", ID: "tu_1", Name: name, Input: args}
}

func finishBlock(status, reply string) ContentBlock {
	return toolBlock(completionToolName, map[string]any{"status": status, "reply": reply})
}

func scriptedResp(blocks []ContentBlock, stopReason string) *LLMResponse {
	return &LLMResponse{
		StopReason: stopReason,
		Usage:      UsageInfo{InputTokens: 10, OutputTokens: 10},
		Content:    blocks,
	}
}

// makeTestHome creates an isolated temp dir for each test.
func makeTestHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "traces"), 0700)
	return dir
}

// makeEvalTools creates the same tools BuildRegistry would, but isolated.
func makeEvalTools(home string) *Registry {
	db := Connect(home)
	mem := NewMemory(db, nil, &Settings{Home: home, TopK: 4, ConsolidateEvery: 0})
	return BuildRegistry(db, home, "/", mem)
}

// --- Tests ---
