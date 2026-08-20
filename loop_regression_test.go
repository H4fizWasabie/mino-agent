package main

import (
	"context"
	"fmt"
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
		done <- RunLoopContext(ctx, blocking, "wedge", "", []Message{{Role: "user", Content: "go"}}, tools, 10, 100, nil, false, "")
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

func (b *blockingClient) CreateContextNoReasoning(ctx context.Context, session string, role ModelRole, messages []Message, maxTokens int, system string) (*LLMResponse, error) {
	return b.Create(session, role, messages, maxTokens, system, nil)
}

// Regression 2026-08-07: the threads-ai-learning publish stage failed because
// mimo-v2.5 emitted a text marker whose JSON args used shell-style \' escapes:
//
//	[tool_call: bash({"command":"echo -n '...devil\'s advocate...' | wc -c"})]
//
// json.Unmarshal rejects \' as an invalid escape, the call was silently
// dropped, and the loop declared the stage complete without writing
// output/result.md. The parser must repair the args and keep the call.

// A marker that still fails after repair must be reported as found-but-broken
// so the loop pushes the model to re-emit instead of treating it as "done".

// Loop-level: an unparseable marker must trigger a corrective push and another
// iteration, not a silent "complete" on the first turn.
func TestLoopPushesOnUnparseableMarker(t *testing.T) {
	tools := NewRegistry()
	client := &fakeClient{script: []*LLMResponse{
		// retired protocol: a [tool_call: ...] marker must get the
		// code-mode corrective push, not a silent no-op completion
		scriptedResp([]ContentBlock{textBlock(`[tool_call: bash({command: "echo hi"})]`)}, "stop"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}
	result := RunLoopContext(context.Background(), client, "marker-loop", "", []Message{{Role: "user", Content: "go"}}, tools, 5, 100, nil, false, "")
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete (loop must continue after the push)", result.Status)
	}
	if len(client.messages) < 2 {
		t.Fatalf("model called %d times, want 2 (corrective push then done)", len(client.messages))
	}
	pushed := false
	for _, m := range client.messages[1] {
		if strings.Contains(m.Content, "retired") {
			pushed = true
		}
	}
	if !pushed {
		t.Fatal("code-mode corrective push missing from second call's messages")
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
		scriptedResp([]ContentBlock{scriptBlock("printf ok > " + out)}, "stop"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}
	ctx := context.WithValue(context.Background(), stageOutputsKey{}, []string{out})
	result := RunLoopContext(ctx, client, "stage-contract", "", []Message{{Role: "user", Content: "run stage"}}, tools, 10, 100, nil, false, "")
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
	result := RunLoopContext(context.Background(), client, "plain-loop", "", []Message{{Role: "user", Content: "hi"}}, tools, 5, 100, nil, false, "")
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
	// CDE-001: native tool calls cannot occur (no tools array is sent). The
	// sibling failure — an empty [script] marker — must push, not execute.
	tools := NewRegistry()
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock("[script]\n[/script]")}, "stop"),
		scriptedResp([]ContentBlock{scriptBlock("echo recovered")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}
	result := RunLoopContext(context.Background(), client, "native-args", "", []Message{{Role: "user", Content: "go"}}, tools, 5, 100, nil, false, "")
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "script" {
		t.Fatalf("want exactly one script call after the malformed-marker push, got %#v", result.ToolCalls)
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
		client.script = append(client.script, scriptedResp([]ContentBlock{scriptBlock("echo rewrite > " + out)}, "stop"))
	}
	client.script = append(client.script, scriptedResp([]ContentBlock{textBlock("done")}, "stop"))
	ctx := context.WithValue(context.Background(), traceTagKey{}, map[string]string{"playbook": "p", "stage": "01-x"})
	result := RunLoopContext(ctx, client, "rewrite-loop", "", []Message{{Role: "user", Content: "go"}}, tools, 20, 100, nil, false, "")
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	// CDE-001: with code mode the write_file-specific rewrite tripwire is
	// dormant (scripts produce "script" calls, not write_file calls) — the
	// #171 repetition guard covers the same failure: identical script heads
	// must trigger the CHANGE APPROACH injection.
	pushed := false
	for _, msgs := range client.messages {
		for _, m := range msgs {
			if strings.Contains(m.Content, "repeated the identical tool call") && strings.Contains(m.Content, "CHANGE APPROACH") {
				pushed = true
			}
		}
	}
	if !pushed {
		t.Fatal("repetition-guard push missing from model messages")
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
	result := RunLoopContext(ctx, client, "interleaved", "", []Message{{Role: "user", Content: "go"}}, tools, 20, 100, nil, false, "")
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
	result := RunLoopContext(context.Background(), client, "mutation-loop", "", msgs, tools, 5, 100, nil, false, "")
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
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{scriptBlock("echo Wrote 123 bytes to /tmp/x.md")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("the edit was rejected")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("Checking the result again — it did land. Done.")}, "stop"),
	}}
	msgs := []Message{{Role: "user", Content: "fix the file"}}
	result := RunLoopContext(context.Background(), client, "osv03-loop", "", msgs, tools, 5, 100, nil, false, "")
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	if len(client.messages) < 3 {
		t.Fatalf("model called %d times, want 3 (script, claim, corrected)", len(client.messages))
	}
	pushed := false
	for _, m := range client.messages[2] {
		if strings.Contains(m.Content, "tool results show success") && strings.Contains(m.Content, "script") {
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
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{scriptBlock("echo Error writing /tmp/x.md: permission denied; exit 1")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("I fixed the file")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("Retrying with the right permissions.")}, "stop"),
	}}
	msgs := []Message{{Role: "user", Content: "fix the file"}}
	result := RunLoopContext(context.Background(), client, "osv03-loop2", "", msgs, tools, 5, 100, nil, false, "")
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
				cmd := "echo " + tc.out
				if strings.HasPrefix(tc.out, "Error") {
					cmd += "; exit 1"
				}
				script = append(script, scriptedResp([]ContentBlock{scriptBlock(cmd)}, "stop"))
			}
			script = append(script, scriptedResp([]ContentBlock{textBlock(tc.reply)}, "stop"))
			client := &fakeClient{script: script}
			result := RunLoopContext(context.Background(), client, "osv03-ctrl", "", []Message{{Role: "user", Content: "fix the file"}}, tools, 5, 100, nil, false, "")
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
	result := RunLoopContext(context.Background(), client, "plain-loop", "", msgs, tools, 5, 100, nil, false, "")
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
	result := RunLoopContext(ctx, client, "stage-mutation", "", msgs, tools, 5, 100, nil, false, "")
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
		script = append(script, scriptedResp([]ContentBlock{textBlock("[script]\n[/script]")}, "stop"))
	}
	client := &fakeClient{script: script}
	result := RunLoopContext(context.Background(), client, "parse-loop", "", []Message{{Role: "user", Content: "go"}}, tools, 10, 100, nil, false, "")
	if result.Status != "error" {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Reply, "repeatedly emitted malformed script markers") {
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
		script = append(script, scriptedResp([]ContentBlock{textBlock("[script]\n[/script]")}, "stop"))
		script = append(script, scriptedResp([]ContentBlock{scriptBlock("echo n=" + fmt.Sprint(i))}, "stop"))
	}
	client := &fakeClient{script: script}
	result := RunLoopContext(context.Background(), client, "parse-alternating", "", []Message{{Role: "user", Content: "go"}}, tools, 30, 100, nil, false, "")
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
		scriptedResp([]ContentBlock{textBlock("[script]\n[/script]")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("[script]\n[/script]")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("[script]\n[/script]")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}
	result := RunLoopContext(context.Background(), client, "parse-escalate", "", []Message{{Role: "user", Content: "go"}}, tools, 10, 100, nil, false, "")
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	escalated := false
	for _, m := range client.messages[3] { // the 4th call sees the escalated push
		if strings.Contains(m.Content, "were empty or malformed") {
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
		scriptedResp([]ContentBlock{textBlock("[script]\n[/script]")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("[script]\n[/script]")}, "stop"),
		scriptedResp([]ContentBlock{scriptBlock("echo pong")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("[script]\n[/script]")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("[script]\n[/script]")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("[script]\n[/script]")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}
	result := RunLoopContext(context.Background(), client, "parse-reset", "", []Message{{Role: "user", Content: "go"}}, tools, 10, 100, nil, false, "")
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete (counter reset by the executed tool)", result.Status)
	}
	// The 3rd call (before the tool executed) must NOT carry the escalated push
	// — only 2 consecutive failures at that point.
	escalatedEarly := false
	for _, m := range client.messages[2] {
		if strings.Contains(m.Content, "were empty or malformed") {
			escalatedEarly = true
		}
	}
	if escalatedEarly {
		t.Fatal("counter did not reset — escalation appeared before 3 consecutive failures")
	}
	// The 7th call (3 failures after the reset) must carry it.
	escalatedLate := false
	for _, m := range client.messages[6] {
		if strings.Contains(m.Content, "were empty or malformed") {
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

// #110: the full marker path repairs each shape end to end.

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

// #158: a model under provider rotation can emit a bare NAME({...}) tool call
// WITHOUT the [tool_call:] prefix. Strict parsing dropped it, so the loop
// pushed the model to re-emit the same shape until the iteration cap — a FB
// publish call burned ~20 iterations this way. The tolerant fallback must
// recover these while leaving ordinary prose alone.

// The bare fallback must not shadow the strict prefixed format, and a broken
// bare call (args not JSON) must not be dispatched as a tool call.

// #161: the iteration-cap reply must report progress and offer a decision point
// instead of a bare "(stopped after N iterations)", so the user knows what was
// attempted and a "continue" can resume meaningfully.
func TestIterationCapReplyReportsProgress(t *testing.T) {
	calls := []ToolCall{
		{Name: "bash", Output: "ok"},
		{Name: "generate_image", Output: "path"},
		{Name: "bash", Output: "ok"},
	}
	reply := iterationCapReply(50, calls)
	if !strings.Contains(reply, "stopped after 50 iterations") {
		t.Fatalf("reply missing cap note: %q", reply)
	}
	if !strings.Contains(reply, "generate_image") || !strings.Contains(reply, "bash") {
		t.Fatalf("reply missing completed tools: %q", reply)
	}
	if !strings.Contains(reply, "Continue") || !strings.Contains(reply, "abandon") {
		t.Fatalf("reply missing continue/abandon decision point: %q", reply)
	}
	if strings.Count(reply, "bash") != 1 {
		t.Fatalf("reply should dedupe repeated tools: %q", reply)
	}
}

func TestIterationCapReplyNoTools(t *testing.T) {
	reply := iterationCapReply(30, nil)
	if !strings.Contains(reply, "No tools were completed") {
		t.Fatalf("reply should note no completion: %q", reply)
	}
}

// TestLoopIterationAwarenessRepeatedTool (issues #171): when the model repeats
// the identical tool call 3x, the loop injects a repetition-awareness
// observation into the message stream so the model can diverge or stop BEFORE
// burning to the cap.
func TestLoopIterationAwarenessRepeatedTool(t *testing.T) {
	tools := NewRegistry()
	tools.Register(&Tool{
		Name:        "bounce",
		Description: "returns fixed output",
		Fn:          func(args map[string]any) string { return "bounced" },
	})
	var script []*LLMResponse
	for i := 0; i < 4; i++ {
		script = append(script, scriptedResp([]ContentBlock{scriptBlock("echo bounced")}, "stop"))
	}
	script = append(script, scriptedResp([]ContentBlock{textBlock("done")}, "stop"))
	client := &fakeClient{script: script}
	RunLoopContext(context.Background(), client, "awareness", "", []Message{{Role: "user", Content: "go"}}, tools, 20, 100, nil, false, "")

	found := false
	for _, msgs := range client.messages {
		for _, m := range msgs {
			if strings.Contains(m.Content, "repeated the identical tool call") && strings.Contains(m.Content, "CHANGE APPROACH") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("repetition-awareness observation was not injected")
	}
}

// CTX-019: a mid-flight redirect signal (repetition) must be observable in the
// trace so the post-mortem can verify whether the redirect was followed and
// whether it helped — redirects are checked against outcomes, not assumed.
func TestLoopLogsMidflightRedirectSignal(t *testing.T) {
	tools := NewRegistry()
	tools.Register(&Tool{Name: "bounce", Description: "returns fixed output", Fn: func(args map[string]any) string { return "bounced" }})
	var script []*LLMResponse
	for i := 0; i < 4; i++ {
		script = append(script, scriptedResp([]ContentBlock{scriptBlock("echo bounced")}, "stop"))
	}
	script = append(script, scriptedResp([]ContentBlock{textBlock("done")}, "stop"))
	client := &fakeClient{script: script}
	home := t.TempDir()
	RunLoopContext(context.Background(), client, "awareness", "", []Message{{Role: "user", Content: "go"}}, tools, 20, 100, nil, false, home)

	found := false
	files, _ := filepath.Glob(filepath.Join(home, "traces", "*.jsonl"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "midflight_signal") && strings.Contains(line, "repetition") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("midflight_signal repetition event was not logged to the trace (redirect must be observable for CTX-019 verification)")
	}
}

// CTX-022 C: provenance gate — a web search whose query overlaps a turn's
// earlier user-provenanced recall must produce a warning; non-overlapping
// queries and recalls without user provenance must stay silent.

// CTX-025 fix-B: divergence detector — a stage whose input tokens balloon
// past 3x the iteration-1 baseline within the first 10 iterations while its
// required outputs are still missing must be state-reset once: context
// truncated back to the stage prompt, required outputs re-injected. Recovery,
// not another advisory — exploring is shut down so the stage converges.
func TestLoopDivergenceResetClearsExploration(t *testing.T) {
	tools := NewRegistry()
	out := filepath.Join(t.TempDir(), "output", "payload.json")
	ctx := context.WithValue(context.Background(), stageOutputsKey{}, []string{out})
	ctx = context.WithValue(ctx, traceTagKey{}, map[string]string{"playbook": "p", "stage": "01-x", "run": "r"})
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{scriptBlock("echo explore")}, "stop"),
		scriptedResp([]ContentBlock{scriptBlock("echo explore2")}, "stop"),
		scriptedResp([]ContentBlock{scriptBlock("echo explore3")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}
	// scriptedResp hardcodes 10/10 tokens — grow inputs to simulate bloat.
	client.script[1].Usage.InputTokens = 10
	client.script[2].Usage.InputTokens = 35
	client.script[3].Usage.InputTokens = 40
	home := t.TempDir()
	_ = RunLoopContext(ctx, client, "s", "", []Message{{Role: "user", Content: "run"}}, tools, 10, 100, nil, false, home)
	var sawReset bool
	for _, m := range client.messages {
		for _, msg := range m {
			if strings.Contains(msg.Content, "divergence reset") {
				sawReset = true
			}
		}
	}
	if !sawReset {
		t.Fatal("divergence reset message never injected")
	}
	files, _ := filepath.Glob(filepath.Join(home, "traces", "*.jsonl"))
	var traced bool
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "midflight_signal") && strings.Contains(line, "divergence") {
				traced = true
			}
		}
	}
	if !traced {
		t.Fatal("midflight_signal divergence event was not logged to the trace")
	}
}

// Control: token bloat alone does not reset a normal chat turn — the
// detector is stage-scoped (traceTags must be present), so an explorative
// chat session with growing context still completes normally.
func TestLoopDivergenceSkippedOutsideStage(t *testing.T) {
	tools := NewRegistry()
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}
	client.script[0].Usage.InputTokens = 400
	result := RunLoopContext(context.Background(), client, "chat", "", []Message{{Role: "user", Content: "hi"}}, tools, 10, 100, nil, false, t.TempDir())
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete (no reset outside stages)", result.Status)
	}
}
