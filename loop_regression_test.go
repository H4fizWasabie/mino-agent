package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// CTX-007: a client disconnect cancels the turn's ctx; the provider call must
// observe it so the loop returns promptly instead of wedging the session mutex
// (2026-08-11: a dead dashboard connection left the turn inside a provider call
// that ignored cancellation — every later turn blocked on conversation.mu).
func TestLoopReturnsWhenClientDisconnectsMidProviderCall(t *testing.T) {
	tools := NewRegistry()
	started := make(chan struct{})
	blocking := &blockingClient{started: started}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *LoopResult, 1)
	go func() {
		done <- RunLoopContext(ctx, blocking, "wedge", "", []Message{{Role: "user", Content: "go"}}, tools, 10, 100, nil, false, "", nil)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider call never started")
	}
	cancel() // the client connection died
	select {
	case res := <-done:
		if res.Status != "cancelled" {
			t.Fatalf("status = %q, want cancelled", res.Status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("loop wedged after cancel — the session mutex would block forever")
	}
}

// blockingClient's CreateContext blocks until the ctx is cancelled, simulating
// a slow provider call that must be interruptible.
type blockingClient struct{ started chan struct{} }

func (b *blockingClient) Create(session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef) (*LLMResponse, error) {
	return scriptedResp([]ContentBlock{textBlock("done")}, "stop"), nil
}

func (b *blockingClient) CreateContext(ctx context.Context, session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef) (*LLMResponse, error) {
	close(b.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *blockingClient) Stream(session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef, onText func(string)) (*LLMResponse, error) {
	return b.Create(session, role, messages, maxTokens, system, tools)
}

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
	uses, found, _ := extractTextToolUses(text)
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
	uses, found, failed := extractTextToolUses(`[tool_call: bash({not valid json at all!!})]`)
	if !found {
		t.Fatal("marker should be reported as found")
	}
	if len(uses) != 0 {
		t.Fatalf("got %d uses, want 0 for unparseable marker", len(uses))
	}
	if !strings.Contains(failed, "not valid json") {
		t.Fatalf("failed marker snippet not surfaced: %q", failed)
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

// Regression: malformed NATIVE tool_calls (the sibling of the text-marker \'
// bug). provider.go injects __raw_arguments__ when the model's native
// tool_calls JSON doesn't parse; the loop must NOT execute the tool with
// garbage, and must return the raw string so the model can self-correct.
func TestLoopSurfacesMalformedNativeArgs(t *testing.T) {
	executions := 0
	tools := NewRegistry()
	tools.Register(&Tool{
		Name: "probe", Schema: map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(map[string]any) string {
			executions++
			return "observed"
		},
	})
	client := &fakeClient{script: []*LLMResponse{
		// native tool_use whose args were unparseable at the provider layer
		scriptedResp([]ContentBlock{toolBlock("probe", map[string]any{"__raw_arguments__": "{broken json"})}, "tool_use"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}
	result := RunLoopContext(context.Background(), client, "native-args", "", []Message{{Role: "user", Content: "go"}}, tools, 5, 100, nil, false, "", nil)
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	if executions != 0 {
		t.Fatalf("tool executed %d times, want 0 (malformed args must not run the tool)", executions)
	}
	if len(result.ToolCalls) != 1 || !strings.Contains(result.ToolCalls[0].Output, "{broken json") {
		t.Fatalf("raw args not surfaced to the model: %#v", result.ToolCalls)
	}
}

// Regression: reddit-karma-builder stage 1 rewrote candidates.md 26x and
// burned all 50 iterations. The stage-side tripwire must push a corrective
// message when the same output path is rewritten 6+ times consecutively.
func TestStageRewriteStreakTripwire(t *testing.T) {
	tools := NewRegistry()
	tools.Register(&Tool{
		Name: "write_file", Schema: map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(map[string]any) string { return "wrote" },
	})
	client := &fakeClient{}
	out := "/tmp/stage-out/result.md"
	for i := 0; i < 8; i++ {
		client.script = append(client.script, scriptedResp([]ContentBlock{toolBlock("write_file", map[string]any{"path": out})}, "tool_use"))
	}
	client.script = append(client.script, scriptedResp([]ContentBlock{textBlock("done")}, "stop"))
	ctx := context.WithValue(context.Background(), traceTagKey{}, map[string]string{"playbook": "p", "stage": "01-x"})
	result := RunLoopContext(ctx, client, "rewrite-loop", "", []Message{{Role: "user", Content: "go"}}, tools, 20, 100, nil, false, "", nil)
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	// The corrective push must have been sent after the 6th consecutive rewrite.
	pushed := false
	for _, msgs := range client.messages {
		for _, m := range msgs {
			if strings.Contains(m.Content, "rewritten "+out+" repeatedly") {
				pushed = true
			}
		}
	}
	if !pushed {
		t.Fatal("rewrite-streak push missing from model messages")
	}
}

// A mixed sequence (write, read, write) is NOT drift — no push.
func TestStageRewriteStreakAllowsInterleavedReads(t *testing.T) {
	tools := NewRegistry()
	tools.Register(&Tool{
		Name: "write_file", Schema: map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(map[string]any) string { return "wrote" },
	})
	tools.Register(&Tool{
		Name: "read_file", Schema: map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(map[string]any) string { return "content" },
	})
	client := &fakeClient{}
	out := "/tmp/stage-out/result.md"
	for i := 0; i < 6; i++ {
		client.script = append(client.script,
			scriptedResp([]ContentBlock{toolBlock("write_file", map[string]any{"path": out})}, "tool_use"),
			scriptedResp([]ContentBlock{toolBlock("read_file", map[string]any{"path": out})}, "tool_use"),
		)
	}
	client.script = append(client.script, scriptedResp([]ContentBlock{textBlock("done")}, "stop"))
	ctx := context.WithValue(context.Background(), traceTagKey{}, map[string]string{"playbook": "p", "stage": "01-x"})
	result := RunLoopContext(ctx, client, "interleaved", "", []Message{{Role: "user", Content: "go"}}, tools, 20, 100, nil, false, "", nil)
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	for _, msgs := range client.messages {
		for _, m := range msgs {
			if strings.Contains(m.Content, "rewritten ") {
				t.Fatalf("false-positive push for interleaved write/read: %q", m.Content)
			}
		}
	}
}

// Regression: the distill prompt's template placeholder ("snake_case_id_prefixed_ep_")
// must never become a fact ID — parseDistillResponse rejects it.
func TestParseDistillResponseRejectsTemplateID(t *testing.T) {
	good := `{"run": {"id": "ep_ai_news_daily_20260805", "subject": "posted report"}, "facts": []}`
	if _, err := parseDistillResponse(good); err != nil {
		t.Fatalf("valid distill response rejected: %v", err)
	}
	leaked := `{"run": {"id": "snake_case_id_prefixed_ep_ai_news_daily_20260805", "subject": "posted report"}, "facts": []}`
	if _, err := parseDistillResponse(leaked); err == nil {
		t.Fatal("template-leaked ID accepted; must be rejected")
	}
	leakedSubject := `{"run": {"id": "ep_x", "subject": "snake_case_id_prefixed_ep_ copied placeholder"}, "facts": []}`
	if _, err := parseDistillResponse(leakedSubject); err == nil {
		t.Fatal("template text in subject accepted; must be rejected")
	}
}

// The model claims a deletion it never executed ("Consider it deleted") with
// zero tool calls. The loop must push back once instead of completing.
func TestLoopPushesOnUnverifiedMutationClaim(t *testing.T) {
	tools := NewRegistry()
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock("Consider it deleted from my notes.")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}
	msgs := []Message{{Role: "user", Content: "remove that from your memory"}}
	result := RunLoopContext(context.Background(), client, "mutation-loop", "", msgs, tools, 5, 100, nil, false, "", nil)
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	if len(client.messages) < 2 {
		t.Fatalf("model called %d times, want 2 (push then done)", len(client.messages))
	}
	pushed := false
	for _, m := range client.messages[1] {
		if strings.Contains(m.Content, "no tool was executed this turn") {
			pushed = true
		}
	}
	if !pushed {
		t.Fatal("mutation-claim push missing from second call's messages")
	}
}

// OSV-03: the model claims an operation FAILED ("the edit was rejected") while
// this turn's tool results show it succeeded. The loop must correct the claim
// before it reaches the user — the exact 2026-08-09 threads case.
func TestLoopCorrectsContradictedFailureClaim(t *testing.T) {
	tools := NewRegistry()
	tools.Register(&Tool{Name: "write_file", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string { return "Wrote 123 bytes to /tmp/x.md" }})
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("write_file", map[string]any{"path": "/tmp/x.md"})}, "tool_use"),
		scriptedResp([]ContentBlock{textBlock("the edit was rejected")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("Checking the result again — it did land. Done.")}, "stop"),
	}}
	msgs := []Message{{Role: "user", Content: "fix the file"}}
	result := RunLoopContext(context.Background(), client, "osv03-loop", "", msgs, tools, 5, 100, nil, false, "", nil)
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	if len(client.messages) < 3 {
		t.Fatalf("model called %d times, want 3 (tool, claim, corrected)", len(client.messages))
	}
	pushed := false
	for _, m := range client.messages[2] {
		if strings.Contains(m.Content, "tool results show success") && strings.Contains(m.Content, "write_file") {
			pushed = true
		}
	}
	if !pushed {
		t.Fatal("contradiction push missing from third call's messages")
	}
}

// OSV-03: the reverse contradiction — a success claim ("I fixed the file")
// while the tool result shows an error. Same bounded check, other direction.
func TestLoopCorrectsContradictedSuccessClaim(t *testing.T) {
	tools := NewRegistry()
	tools.Register(&Tool{Name: "write_file", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string { return "Error writing /tmp/x.md: permission denied" }})
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("write_file", map[string]any{"path": "/tmp/x.md"})}, "tool_use"),
		scriptedResp([]ContentBlock{textBlock("I fixed the file")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("Retrying with the right permissions.")}, "stop"),
	}}
	msgs := []Message{{Role: "user", Content: "fix the file"}}
	result := RunLoopContext(context.Background(), client, "osv03-loop2", "", msgs, tools, 5, 100, nil, false, "", nil)
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	pushed := false
	for _, m := range client.messages[2] {
		if strings.Contains(m.Content, "tool results show errors") {
			pushed = true
		}
	}
	if !pushed {
		t.Fatal("contradiction push missing from third call's messages")
	}
}

// OSV-03 controls: legitimate outcomes must not trip the check — a search-style
// "failed" without an operation noun, and a failure claim that matches a real
// tool error, both complete without a push.
func TestLoopNoPushOnConsistentOutcomes(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		out   string // tool output; "" = no tool call
	}{
		{"no operation noun: search-style failure", "the search failed to find anything", "found 3 results"},
		{"failure claim matches errored tool", "the edit was rejected", "Error writing /tmp/x.md: disk full"},
		{"success claim matches successful tool", "I fixed the file", "Wrote 123 bytes to /tmp/x.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tools := NewRegistry()
			var script []*LLMResponse
			if tc.out != "" {
				tools.Register(&Tool{Name: "write_file", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string { return tc.out }})
				script = append(script, scriptedResp([]ContentBlock{toolBlock("write_file", map[string]any{"path": "/tmp/x.md"})}, "tool_use"))
			}
			script = append(script, scriptedResp([]ContentBlock{textBlock(tc.reply)}, "stop"))
			client := &fakeClient{script: script}
			result := RunLoopContext(context.Background(), client, "osv03-ctrl", "", []Message{{Role: "user", Content: "fix the file"}}, tools, 5, 100, nil, false, "", nil)
			if result.Status != "complete" {
				t.Fatalf("status = %q, want complete", result.Status)
			}
			if len(client.messages) != len(script) {
				t.Fatalf("model called %d times, want %d (no push for a consistent claim)", len(client.messages), len(script))
			}
		})
	}
}

// Control: a plain conversational completion without a mutation request must
// not trigger the push.
func TestLoopNoPushOnPlainCompletion(t *testing.T) {
	tools := NewRegistry()
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock("Good morning! How can I help?")}, "stop"),
	}}
	msgs := []Message{{Role: "user", Content: "hello mino"}}
	result := RunLoopContext(context.Background(), client, "plain-loop", "", msgs, tools, 5, 100, nil, false, "", nil)
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	if len(client.messages) != 1 {
		t.Fatalf("model called %d times, want 1 (no push)", len(client.messages))
	}
}

// Control: in a stage context the output contract governs; a final summary
// containing "removed" must not trigger the chat push.
func TestLoopNoMutationPushInsideStage(t *testing.T) {
	tools := NewRegistry()
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock("all removed, stage complete")}, "stop"),
	}}
	msgs := []Message{{Role: "user", Content: "run stage"}}
	ctx := context.WithValue(context.Background(), stageOutputsKey{}, []string{})
	result := RunLoopContext(ctx, client, "stage-mutation", "", msgs, tools, 5, 100, nil, false, "", nil)
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	if len(client.messages) != 1 {
		t.Fatalf("model called %d times, want 1 (stage: no chat push)", len(client.messages))
	}
}

// Issue #24: repeated unparseable markers escalate, then abort with a
// diagnosis instead of burning to the iteration cap.
func TestLoopAbortsAfterSixConsecutiveParseFailures(t *testing.T) {
	tools := NewRegistry()
	script := make([]*LLMResponse, 0, 8)
	for i := 0; i < 8; i++ {
		script = append(script, scriptedResp([]ContentBlock{textBlock("[tool_call: bash({broken)")}, "stop"))
	}
	client := &fakeClient{script: script}
	result := RunLoopContext(context.Background(), client, "parse-loop", "", []Message{{Role: "user", Content: "go"}}, tools, 10, 100, nil, false, "", nil)
	if result.Status != "error" {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Reply, "repeatedly emitted unparseable tool calls") {
		t.Fatalf("reply = %q, want the parse-abort diagnosis", result.Reply)
	}
	if result.Iterations != 6 {
		t.Fatalf("iterations = %d, want 6 (abort before the cap)", result.Iterations)
	}
}

// CTX-006: a model that alternates malformed markers and successful calls
// (2026-08-10 CHEM 15: failures at iters 4, 11-14, 16, 24-26 — never 6
// consecutive) must still abort at 6 total failures, not burn to the cap.
func TestLoopAbortsAfterSixTotalParseFailuresWithInterleavedSuccesses(t *testing.T) {
	tools := NewRegistry()
	tools.Register(&Tool{
		Name:        "echo",
		Description: "echo back",
		Schema:      map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}},
		Fn:          func(args map[string]any) string { return "ok" },
	})
	// Each broken marker is followed by a successful native tool call, so the
	// old streak counter reset every time — 7 broken markers never aborted.
	// Args vary so the loop detector does not fire before the parse guard.
	script := make([]*LLMResponse, 0, 14)
	for i := 0; i < 7; i++ {
		script = append(script, scriptedResp([]ContentBlock{textBlock("[tool_call: echo({broken)")}, "stop"))
		script = append(script, scriptedResp([]ContentBlock{toolBlock("echo", map[string]any{"n": float64(i)})}, "tool_use"))
	}
	client := &fakeClient{script: script}
	result := RunLoopContext(context.Background(), client, "parse-alternating", "", []Message{{Role: "user", Content: "go"}}, tools, 30, 100, nil, false, "", nil)
	if result.Status != "error" {
		t.Fatalf("status = %q, want error (7 broken markers in 14 iterations)", result.Status)
	}
	if result.Iterations != 11 {
		t.Fatalf("iterations = %d, want 11 (6th total failure aborts, not the 30-cap)", result.Iterations)
	}
}

// Escalation: the third consecutive failure gets a different push message.
func TestLoopEscalatesParsePushAfterThreeFailures(t *testing.T) {
	tools := NewRegistry()
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock("[tool_call: bash({broken)")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("[tool_call: bash({broken)")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("[tool_call: bash({broken)")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}
	result := RunLoopContext(context.Background(), client, "parse-escalate", "", []Message{{Role: "user", Content: "go"}}, tools, 10, 100, nil, false, "", nil)
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	escalated := false
	for _, m := range client.messages[3] { // the 4th call sees the escalated push
		if strings.Contains(m.Content, "STOP re-emitting the same shape") {
			escalated = true
		}
	}
	if !escalated {
		t.Fatal("escalated push missing after 3 consecutive failures")
	}
}

// A successfully executed tool call between failures resets the counter.
func TestLoopParseCounterResetsOnSuccess(t *testing.T) {
	tools := NewRegistry()
	tools.Register(&Tool{Name: "ping", Description: "p", Schema: map[string]any{"type": "object", "properties": map[string]any{}}, Fn: func(map[string]any) string { return "pong" }})
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock("[tool_call: bash({broken)")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("[tool_call: bash({broken)")}, "stop"),
		scriptedResp([]ContentBlock{toolBlock("ping", map[string]any{})}, "tool_use"),
		scriptedResp([]ContentBlock{textBlock("[tool_call: bash({broken)")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("[tool_call: bash({broken)")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("[tool_call: bash({broken)")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}
	result := RunLoopContext(context.Background(), client, "parse-reset", "", []Message{{Role: "user", Content: "go"}}, tools, 10, 100, nil, false, "", nil)
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete (counter reset by the executed tool)", result.Status)
	}
	// The 3rd call (before the tool executed) must NOT carry the escalated push
	// — only 2 consecutive failures at that point.
	escalatedEarly := false
	for _, m := range client.messages[2] {
		if strings.Contains(m.Content, "STOP re-emitting") {
			escalatedEarly = true
		}
	}
	if escalatedEarly {
		t.Fatal("counter did not reset — escalation appeared before 3 consecutive failures")
	}
	// The 7th call (3 failures after the reset) must carry it.
	escalatedLate := false
	for _, m := range client.messages[6] {
		if strings.Contains(m.Content, "STOP re-emitting") {
			escalatedLate = true
		}
	}
	if !escalatedLate {
		t.Fatal("escalated push missing after 3 consecutive failures post-reset")
	}
}

// #110: hand-written JSON sloppiness that must survive the lenient repair —
// fences, single-quoted strings, unquoted keys, and combinations. Valid JSON
// must parse on the strict path untouched.
func TestParseToolArgsJSONLenient(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string // key whose value must be present
	}{
		{"valid untouched", `{"path":"/x","limit":5}`, "path"},
		{"shell escapes", `{"command":"echo 'devil\'s advocate'"}` + "", "command"},
		{"trailing comma", `{"path":"/x",}`, "path"},
		{"fenced", "```json\n{\"path\": \"/x\"}\n```", "path"},
		{"single quotes", `{'path': '/x', 'limit': 5}`, "path"},
		{"unquoted keys", `{path: "/x", limit: 5}`, "path"},
		{"single quotes + unquoted keys", `{path: '/x', limit: 5}`, "path"},
	}
	for _, c := range cases {
		args, ok := parseToolArgsJSON(c.json)
		if !ok {
			t.Errorf("%s: parse failed for %s", c.name, c.json)
			continue
		}
		if _, exists := args[c.want]; !exists {
			t.Errorf("%s: key %q missing in %v", c.name, c.want, args)
		}
	}
	// Genuinely broken JSON must still fail.
	if _, ok := parseToolArgsJSON(`{not valid json at all!!}`); ok {
		t.Fatal("broken JSON parsed")
	}
}

// #110: the full marker path repairs each shape end to end.
func TestExtractTextToolUsesLenientShapes(t *testing.T) {
	cases := []string{
		`[tool_call: read_file({'path': '/tmp/x'})]`,
		"[tool_call: read_file(```json\n{\"path\": \"/tmp/x\"}\n```)]",
		`[tool_call: read_file({path: '/tmp/x'})]`,
	}
	for _, text := range cases {
		uses, found, failed := extractTextToolUses(text)
		if !found || len(uses) != 1 {
			t.Fatalf("marker %q: found=%v uses=%d failed=%q", text, found, len(uses), failed)
		}
		args := uses[0].Input.(map[string]any)
		if args["path"] != "/tmp/x" {
			t.Fatalf("marker %q: path = %v", text, args["path"])
		}
	}
}

// T8 (#103 follow-up): the view_image task argument maps to curated prompts —
// critique/OCR/describe get dedicated variants, empty keeps the original
// describe-for-critic prompt, unknown tasks fall through to the free-form
// wrapper.
func TestVisionPromptVariants(t *testing.T) {
	cases := []struct {
		task string
		want string
	}{
		{"", "brief for a critic"},
		{"critique the composition", "PASS or REJECT"},
		{"critique", "PASS or REJECT"},
		{"review this photo", "PASS or REJECT"},
		{"assess the quality", "PASS or REJECT"},
		{"judge it", "PASS or REJECT"},
		{"OCR this screenshot", "No text found"},
		{"extract the text", "No text found"},
		{"transcribe it", "No text found"},
		{"describe the subject", "Be neutral and concrete"},
		{"a description", "Be neutral and concrete"},
		{"check if it has people", "You are looking at an image. Task:"},
	}
	for _, c := range cases {
		if got := visionPrompt(c.task); !strings.Contains(got, c.want) {
			t.Errorf("visionPrompt(%q) missing %q: %.60q", c.task, c.want, got)
		}
	}
}
