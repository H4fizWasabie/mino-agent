package main

import (
	"errors"
	"context"
	"strings"
	"testing"
)

// compose_message (SCR-002, #277): one bounded single-turn provider call;
// the digest is the only input; errors are loud Error: strings (the exec
// exit contract turns them into non-zero exits for scripts).

// TestComposeSystemPromptSynthesisDiscipline covers the named seam
// (REL-04): the fixed synthesis contract pins the verification discipline
// into the message layer — no fabrication, digest-only facts.
func TestComposeSystemPromptSynthesisDiscipline(t *testing.T) {
	for _, must := range []string{"digest", "never fabricate", "ONE"} {
		if !strings.Contains(strings.ToLower(composeSystemPrompt), strings.ToLower(must)) {
			t.Fatalf("composeSystemPrompt missing %q", must)
		}
	}
}

func TestComposeMessageToolSingleTurnSynthesis(t *testing.T) {
	fake := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock("💰 Spend this week: $6.32.")}, "stop"),
	}}
	tool := makeComposeMessageTool(fake)

	out := tool.ContextFn(context.Background(), map[string]any{"digest": "spend 6.32"})
	if out != "💰 Spend this week: $6.32." {
		t.Fatalf("unexpected output: %q", out)
	}
	if len(fake.roles) != 1 || fake.roles[0] != MainModel {
		t.Fatalf("expected exactly one MainModel call, got roles=%v", fake.roles)
	}
	if len(fake.messages) != 1 || len(fake.messages[0]) != 1 || fake.messages[0][0].Content != "spend 6.32" {
		t.Fatalf("expected one user message with the digest, got %v", fake.messages)
	}
	if len(fake.toolSets) != 1 || len(fake.toolSets[0]) != 0 {
		t.Fatalf("expected NO tools on the synthesis call, got %v", fake.toolSets)
	}
	if len(fake.tools) != 0 {
		t.Fatalf("expected no tools array")
	}
}

// errClient always fails — the provider-error path for compose_message.
type errClient struct {
	fakeClient
}

func (e *errClient) Create(session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef) (*LLMResponse, error) {
	return nil, errors.New("provider down")
}

func (e *errClient) CreateContext(ctx context.Context, session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef) (*LLMResponse, error) {
	return e.Create(session, role, messages, maxTokens, system, tools)
}

func TestComposeMessageToolErrorPaths(t *testing.T) {
	t.Run("empty digest", func(t *testing.T) {
		tool := makeComposeMessageTool(&fakeClient{})
		out := tool.ContextFn(context.Background(), map[string]any{"digest": "  "})
		if !strings.HasPrefix(out, "Error:") {
			t.Fatalf("expected Error:, got %q", out)
		}
	})
	t.Run("nil client", func(t *testing.T) {
		tool := makeComposeMessageTool(nil)
		out := tool.ContextFn(context.Background(), map[string]any{"digest": "x"})
		if !strings.HasPrefix(out, "Error:") {
			t.Fatalf("expected Error:, got %q", out)
		}
	})
	t.Run("provider error", func(t *testing.T) {
		tool := makeComposeMessageTool(&errClient{})
		out := tool.ContextFn(context.Background(), map[string]any{"digest": "x"})
		if !strings.HasPrefix(out, "Error:") {
			t.Fatalf("expected Error:, got %q", out)
		}
	})
	t.Run("empty response", func(t *testing.T) {
		fake := &fakeClient{script: []*LLMResponse{
			scriptedResp([]ContentBlock{textBlock("   ")}, "stop"),
		}}
		tool := makeComposeMessageTool(fake)
		out := tool.ContextFn(context.Background(), map[string]any{"digest": "x"})
		if !strings.HasPrefix(out, "Error:") {
			t.Fatalf("expected Error:, got %q", out)
		}
	})
}
