package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression 2026-08-07: the threads-ai-learning publish stage failed because
// mimo-v2.5 emitted a text marker whose JSON args used shell-style \' escapes:
//
//	[tool_call: bash({"command":"echo -n '...devil\'s advocate...' | wc -c"})]
//
// json.Unmarshal rejects \' as an invalid escape, the call was silently
// dropped, and the loop declared the stage complete without writing
// output/result.md. The parser must repair the args and keep the call.
func TestExtractTextToolUsesRepairsShellEscapes(t *testing.T) {
	text := `[tool_call: bash({"command":"echo -n 'Most people use AI to confirm what they already think.\n\nAsk AI to play devil\'s advocate. Ask it to tell you what you\'re missing.' | wc -c"})]`
	uses, found := extractTextToolUses(text)
	if !found {
		t.Fatal("marker not found")
	}
	if len(uses) != 1 {
		t.Fatalf("got %d uses, want 1 (the malformed marker must not be dropped)", len(uses))
	}
	if uses[0].Name != "bash" {
		t.Fatalf("tool name = %q, want bash", uses[0].Name)
	}
	args := uses[0].Input.(map[string]any)
	cmd, _ := args["command"].(string)
	if !strings.Contains(cmd, "devil's advocate") {
		t.Fatalf("command not repaired: %q", cmd)
	}
}

// A marker that still fails after repair must be reported as found-but-broken
// so the loop pushes the model to re-emit instead of treating it as "done".
func TestExtractTextToolUsesReportsUnparseableMarker(t *testing.T) {
	uses, found := extractTextToolUses(`[tool_call: bash({not valid json at all!!})]`)
	if !found {
		t.Fatal("marker should be reported as found")
	}
	if len(uses) != 0 {
		t.Fatalf("got %d uses, want 0 for unparseable marker", len(uses))
	}
}

// Loop-level: an unparseable marker must trigger a corrective push and another
// iteration, not a silent "complete" on the first turn.
func TestLoopPushesOnUnparseableMarker(t *testing.T) {
	tools := NewRegistry()
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock(`[tool_call: bash({broken})]`)}, "stop"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}
	result := RunLoopContext(context.Background(), client, "marker-loop", "", []Message{{Role: "user", Content: "go"}}, tools, 5, 100, nil, false, "", nil)
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete (loop must continue after the push)", result.Status)
	}
	if len(client.messages) < 2 {
		t.Fatalf("model called %d times, want 2 (corrective push then done)", len(client.messages))
	}
	pushed := false
	for _, m := range client.messages[1] {
		if strings.Contains(m.Content, "could not be parsed") {
			pushed = true
		}
	}
	if !pushed {
		t.Fatal("corrective push missing from second call's messages")
	}
}

// Stage-level contract: the loop must NOT declare a stage complete while a
// declared output is missing; it pushes the model to write it and continues.
func TestStageLoopRequiresOutputBeforeComplete(t *testing.T) {
	out := filepath.Join(t.TempDir(), "result.md")
	tools := NewRegistry()
	tools.Register(&Tool{
		Name:        "write_file",
		Description: "write a file",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"path", "content"},
		},
		Fn: func(args map[string]any) string {
			p, _ := args["path"].(string)
			c, _ := args["content"].(string)
			if err := os.WriteFile(p, []byte(c), 0644); err != nil {
				return "error: " + err.Error()
			}
			return "wrote " + p
		},
	})
	// The stage declares output/result.md; the first model turn claims done
	// without writing it. The loop must push and continue, then the second
	// turn writes the file, and the third completes.
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock("done, no output written")}, "stop"),
		scriptedResp([]ContentBlock{toolBlock("write_file", map[string]any{"path": out, "content": "ok"})}, "tool_use"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}
	ctx := context.WithValue(context.Background(), stageOutputsKey{}, []string{out})
	result := RunLoopContext(ctx, client, "stage-contract", "", []Message{{Role: "user", Content: "run stage"}}, tools, 10, 100, nil, false, "", nil)
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	if len(client.messages) < 3 {
		t.Fatalf("model called %d times, want 3 (done → push+write → done)", len(client.messages))
	}
	pushed := false
	for _, m := range client.messages[1] {
		if strings.Contains(m.Content, "required output") {
			pushed = true
		}
	}
	if !pushed {
		t.Fatal("stage-output push missing from second call's messages")
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output file was never written: %v", err)
	}
}

// Control: without a stage-output contract, a plain done still completes on
// the first turn (the push must not leak into normal chat loops).
func TestLoopNoPushOutsideStage(t *testing.T) {
	tools := NewRegistry()
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}
	result := RunLoopContext(context.Background(), client, "plain-loop", "", []Message{{Role: "user", Content: "hi"}}, tools, 5, 100, nil, false, "", nil)
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete on first turn outside a stage", result.Status)
	}
	if len(client.messages) != 1 {
		t.Fatalf("model called %d times, want 1 (no push outside stage)", len(client.messages))
	}
}
