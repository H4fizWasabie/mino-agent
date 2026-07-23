package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	return BuildRegistry(db, home, mem)
}

// --- Tests ---

func TestScheduleTaskActuallyWritesJSON(t *testing.T) {
	home := makeTestHome(t)
	tools := makeEvalTools(home)

	// Script: LLM calls schedule_task, then replies "Done!"
	script := []*LLMResponse{
		scriptedResp([]ContentBlock{
			toolBlock("schedule_task", map[string]any{
				"id":       "test-eval-task",
				"schedule": "08:00",
				"prompt":   "say hello",
			}),
		}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Task created!")}, "tool_use"),
	}

	fake := &fakeClient{script: script}
	result := RunLoop(fake, "eval", "", nil, tools, 10, 2048, nil, false, nil, home, nil)

	// Task file must exist
	data, err := os.ReadFile(filepath.Join(home, "schedule.json"))
	if err != nil {
		t.Fatalf("schedule.json not created: %v", err)
	}
	var jobs []ScheduledJob
	json.Unmarshal(data, &jobs)
	found := false
	for _, j := range jobs {
		if j.ID == "test-eval-task" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("schedule.json doesn't contain test-eval-task. got: %s", string(data))
	}
	_ = result
}

func TestNotifyCheckpointSavedAfterTool(t *testing.T) {
	home := makeTestHome(t)
	tools := makeEvalTools(home)
	chk := NewCheckpointManager(home, "eval-session")

	script := []*LLMResponse{
		scriptedResp([]ContentBlock{
			toolBlock("schedule_task", map[string]any{
				"id":       "chk-test",
				"schedule": "09:00",
				"prompt":   "checkpoint me",
			}),
		}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "done")}, "tool_use"),
	}

	RunLoop(&fakeClient{script: script}, "eval", "", []Message{{Role: "user", Content: "checkpoint me"}}, tools, 10, 2048, nil, false, chk, home, nil)

	data, _ := os.ReadFile(filepath.Join(home, "active_tasks", "eval-session.json"))
	if len(data) == 0 {
		t.Error("checkpoint not written after tool execution")
	}
	if snap := chk.Load(); snap == nil || len(snap.ToolsUsed) == 0 || snap.Goal != "checkpoint me" {
		t.Errorf("checkpoint = %#v", snap)
	}
}

func TestRequestApprovalCreatesPendingFile(t *testing.T) {
	home := makeTestHome(t)
	tools := makeEvalTools(home)

	script := []*LLMResponse{
		scriptedResp([]ContentBlock{
			toolBlock("request_approval", map[string]any{
				"action_id": "delete-emails-7",
				"title":     "Delete 7 promotional emails",
				"details":   "Found 7 old promotional emails safe to delete",
				"exec_plan": "Call MCP_google_deleteEmail for IDs: 1,2,3,4,5,6,7",
			}),
		}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("blocked", "Awaiting your approval, Abah.")}, "tool_use"),
	}

	RunLoop(&fakeClient{script: script}, "eval", "", nil, tools, 10, 2048, nil, false, nil, home, nil)

	path := filepath.Join(home, "pending", "delete-emails-7.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("pending approval file not created: %v", err)
	}
	var req map[string]any
	json.Unmarshal(data, &req)
	if req["title"] != "Delete 7 promotional emails" {
		t.Errorf("wrong title: %v", req["title"])
	}
}

func TestBluffingDoesNotCreateArtifact(t *testing.T) {
	// This is the bug we fixed! LLM says "Done!" but never calls the tool.
	home := makeTestHome(t)
	tools := makeEvalTools(home)

	// Script: LLM just replies with text claiming success — NO tool_use block
	script := []*LLMResponse{
		scriptedResp([]ContentBlock{
			textBlock("All set! I've created the gmail-cleanup task for 12:30 PM weekdays. ✅"),
		}, "end_turn"),
	}

	fake := &fakeClient{script: script}
	result := RunLoop(fake, "eval", "", nil, tools, 10, 2048, nil, false, nil, home, nil)

	// schedule.json must NOT exist or be empty (no tool was called)
	data, _ := os.ReadFile(filepath.Join(home, "schedule.json"))
	if strings.Contains(string(data), "gmail-cleanup") {
		t.Errorf("BLUFF DETECTED: LLM claimed to create task but never called schedule_task.\nReply was: %s", result.Reply)
	}

	if result.Iterations != 2 || strings.Contains(result.Reply, "All set") {
		t.Errorf("plain-text bluff became final: iterations=%d reply=%q", result.Iterations, result.Reply)
	}
}

func TestExplicitCompletionEndsInOneIteration(t *testing.T) {
	home := makeTestHome(t)
	tools := makeEvalTools(home)

	script := []*LLMResponse{
		scriptedResp([]ContentBlock{finishBlock("complete", "Paris is the capital of France.")}, "tool_use"),
	}

	result := RunLoop(&fakeClient{script: script}, "eval", "", nil, tools, 10, 2048, nil, false, nil, home, nil)

	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", result.Iterations)
	}
	if result.Reply != "Paris is the capital of France." {
		t.Errorf("wrong reply: %s", result.Reply)
	}
	if result.Status != "complete" {
		t.Errorf("status = %q", result.Status)
	}
}

func TestNarrationAndFakeToolTrailCannotFinishTask(t *testing.T) {
	home := makeTestHome(t)
	path := filepath.Join(home, "bash.txt")
	os.WriteFile(path, []byte("latest PO is PO-42"), 0600)
	tools := makeEvalTools(home)
	script := []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock(`Let me read it properly: [tools used: read_file(path="bash.txt")]`)}, "end_turn"),
		scriptedResp([]ContentBlock{toolBlock("read_file", map[string]any{"path": path})}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "The latest PO is PO-42.")}, "tool_use"),
	}
	var progress int
	result := RunLoop(&fakeClient{script: script}, "eval", "", nil, tools, 10, 2048, func(kind string, _ map[string]any) {
		if kind == "progress" {
			progress++
		}
	}, false, nil, home, nil)

	if result.Status != "complete" || result.Reply != "The latest PO is PO-42." || result.Iterations != 3 {
		t.Fatalf("result = %#v", result)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "read_file" || progress != 1 {
		t.Fatalf("tool calls = %#v, progress = %d", result.ToolCalls, progress)
	}
}

func TestCompletionToolIsAlwaysAvailable(t *testing.T) {
	home := makeTestHome(t)
	fake := &fakeClient{script: []*LLMResponse{scriptedResp([]ContentBlock{finishBlock("complete", "done")}, "tool_use")}}
	RunLoop(fake, "eval", "", nil, makeEvalTools(home), 2, 2048, nil, false, nil, home, nil)
	for _, tool := range fake.tools {
		if tool.Name == completionToolName {
			return
		}
	}
	t.Fatal("complete_task schema was not sent to the model")
}

func TestSuccessfulToolObservationIsExplicit(t *testing.T) {
	home := makeTestHome(t)
	tools := NewRegistry()
	tools.Register(&Tool{Name: "probe", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string {
		return "(no output)"
	}})
	fake := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("probe", map[string]any{"target": "laptop"})}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Sent.")}, "tool_use"),
	}}

	result := RunLoop(fake, "eval", "", nil, tools, 5, 2048, nil, false, nil, home, nil)
	if result.Status != "complete" || len(fake.messages) != 2 {
		t.Fatalf("result=%#v calls=%d", result, len(fake.messages))
	}
	context := fake.messages[1]
	got := context[len(context)-2].Content + "\n" + context[len(context)-1].Content
	for _, want := range []string{`[tool_call: probe({"target":"laptop"})]`, "tool=probe status=ok cached=false", "action_receipt", `"proof":true`, "After status=ok"} {
		if !strings.Contains(got, want) {
			t.Fatalf("observation missing %q: %s", want, got)
		}
	}
}

func TestRepeatedSuccessfulToolExecutesOnce(t *testing.T) {
	home := makeTestHome(t)
	tools := NewRegistry()
	runs := 0
	tools.Register(&Tool{Name: "probe", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string {
		runs++
		return "sent"
	}})
	args := map[string]any{"target": "laptop"}
	fake := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("probe", args)}, "tool_use"),
		scriptedResp([]ContentBlock{toolBlock("probe", args)}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Sent once.")}, "tool_use"),
	}}

	result := RunLoop(fake, "eval", "", nil, tools, 8, 2048, nil, false, nil, home, nil)
	if result.Status != "complete" || result.Iterations != 3 || runs != 1 {
		t.Fatalf("runs=%d result=%#v", runs, result)
	}
	got := fake.messages[2][len(fake.messages[2])-1].Content
	if !strings.Contains(got, "cached=true") || !strings.Contains(got, "already ran") || !strings.Contains(got, `"proof":true`) {
		t.Fatalf("duplicate observation was not authoritative: %s", got)
	}
}

func TestRepeatedNoProgressStopsEarly(t *testing.T) {
	home := makeTestHome(t)
	tools := NewRegistry()
	runs := 0
	tools.Register(&Tool{Name: "probe", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string {
		runs++
		return "sent"
	}})
	var script []*LLMResponse
	for i := 0; i < 10; i++ {
		script = append(script, scriptedResp([]ContentBlock{toolBlock("probe", map[string]any{"target": "laptop"})}, "tool_use"))
	}
	fake := &fakeClient{script: script}

	result := RunLoop(fake, "eval", "", nil, tools, 10, 2048, nil, false, nil, home, nil)
	if result.Status != "stalled" || result.Iterations != 4 || runs != 1 {
		t.Fatalf("runs=%d result=%#v", runs, result)
	}
	if len(fake.toolSets[3]) != 1 || fake.toolSets[3][0].Name != completionToolName {
		t.Fatalf("finalization turn tools=%#v", fake.toolSets[3])
	}
}

func TestTruncatedToolArgumentsAreNotExecuted(t *testing.T) {
	home := makeTestHome(t)
	path := filepath.Join(home, "truncated.txt")
	truncated := scriptedResp([]ContentBlock{toolBlock("write_file", nil)}, "length")
	truncated.Usage.OutputTokens = 2048
	fake := &fakeClient{script: []*LLMResponse{
		truncated,
		scriptedResp([]ContentBlock{finishBlock("complete", "Recovered.")}, "tool_use"),
	}}

	result := RunLoop(fake, "eval", "", []Message{{Role: "user", Content: "write " + path}}, makeEvalTools(home), 5, 2048, nil, false, nil, home, nil)
	if result.Status != "complete" || len(result.ToolCalls) != 0 {
		t.Fatalf("result=%#v", result)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("truncated tool call created %s", path)
	}
	got := fake.messages[1][len(fake.messages[1])-1].Content
	if !strings.Contains(got, "output ceiling") || !strings.Contains(got, "mode=append") {
		t.Fatalf("recovery prompt = %q", got)
	}
}

func TestToolArgumentsAreValidated(t *testing.T) {
	tools := NewRegistry()
	tools.Register(makeBashTool())
	tools.Register(makeWriteTool())
	tests := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"nil object", "bash", nil},
		{"missing required field", "bash", map[string]any{}},
		{"empty bash command", "bash", map[string]any{"command": "  "}},
		{"missing write content", "write_file", map[string]any{"path": "/tmp/unused"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tools.Execute(tt.tool, tt.args); !strings.HasPrefix(got, "Error:") {
				t.Fatalf("Execute() = %q", got)
			}
		})
	}
}

func TestWriteFileSupportsChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.html")
	tool := NewRegistry()
	tool.Register(makeWriteTool())
	if got := tool.Execute("write_file", map[string]any{"path": path, "content": "first", "mode": "overwrite"}); !strings.HasPrefix(got, "Wrote ") {
		t.Fatal(got)
	}
	if got := tool.Execute("write_file", map[string]any{"path": path, "content": "-second", "mode": "append"}); !strings.HasPrefix(got, "Appended ") {
		t.Fatal(got)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "first-second" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestBlockedCompletionIsExplicit(t *testing.T) {
	home := makeTestHome(t)
	result := RunLoop(&fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{finishBlock("blocked", "I need approval to delete it.")}, "tool_use"),
	}}, "eval", "", nil, makeEvalTools(home), 2, 2048, nil, false, nil, home, nil)
	if result.Status != "blocked" || result.Reply != "I need approval to delete it." {
		t.Fatalf("result = %#v", result)
	}
}

func TestFailedToolMustRecoverOrBlockBeforeCompletion(t *testing.T) {
	home := makeTestHome(t)
	tools := NewRegistry()
	tools.Register(&Tool{Name: "probe", Schema: map[string]any{"type": "object"}, Fn: func(args map[string]any) string {
		if args["mode"] == "fail" {
			return "Error: temporary failure"
		}
		return "recovered"
	}})
	script := []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("probe", map[string]any{"mode": "fail"})}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Done despite the error.")}, "tool_use"),
		scriptedResp([]ContentBlock{toolBlock("probe", map[string]any{"mode": "recover"})}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Recovered and completed.")}, "tool_use"),
	}
	result := RunLoop(&fakeClient{script: script}, "eval", "", nil, tools, 6, 2048, nil, false, nil, home, nil)
	if result.Status != "complete" || result.Reply != "Recovered and completed." || result.Iterations != 4 {
		t.Fatalf("result = %#v", result)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("tool calls = %#v", result.ToolCalls)
	}
}

func TestStreamingNarrationIsNotExposedAsFinalText(t *testing.T) {
	home := makeTestHome(t)
	client := &streamingFake{&fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock("Let me read it...")}, "end_turn"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Final result.")}, "tool_use"),
	}}}
	var textEvents, progressEvents int
	result := RunLoop(client, "eval", "", nil, makeEvalTools(home), 3, 2048, func(kind string, _ map[string]any) {
		switch kind {
		case "text":
			textEvents++
		case "progress":
			progressEvents++
		}
	}, true, nil, home, nil)
	if result.Reply != "Final result." || textEvents != 2 || progressEvents != 1 {
		t.Fatalf("result=%#v text=%d progress=%d", result, textEvents, progressEvents)
	}
}

func TestCompletionCannotShareResponseWithExternalTools(t *testing.T) {
	home := makeTestHome(t)
	tools := NewRegistry()
	runs := 0
	tools.Register(&Tool{Name: "probe", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string {
		runs++
		return "ok"
	}})
	result := RunLoop(&fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{
			{Type: "tool_use", ID: "probe-1", Name: "probe", Input: map[string]any{}},
			{Type: "tool_use", ID: "finish-1", Name: completionToolName, Input: map[string]any{"status": "complete", "reply": "Too early."}},
		}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Actually complete.")}, "tool_use"),
	}}, "eval", "", nil, tools, 3, 2048, nil, false, nil, home, nil)
	if runs != 1 || result.Iterations != 2 || result.Reply != "Actually complete." {
		t.Fatalf("runs=%d result=%#v", runs, result)
	}
}

func TestCheckpointClearsOnlyOnExplicitCompletion(t *testing.T) {
	home := makeTestHome(t)
	checkpoint := NewCheckpointManager(home, "task")
	checkpoint.Save("goal", 1, []string{"probe"}, nil)
	settleCheckpoint(checkpoint, &LoopResult{Status: "blocked"})
	if checkpoint.Load() == nil {
		t.Fatal("blocked task checkpoint was cleared")
	}
	settleCheckpoint(checkpoint, &LoopResult{Status: "complete"})
	if checkpoint.Load() != nil {
		t.Fatal("completed task checkpoint was retained")
	}
}

func TestCheckpointKeepsOriginalGoal(t *testing.T) {
	checkpoint := NewCheckpointManager(t.TempDir(), "task")
	checkpoint.Save("original task", 1, []string{"read_file"}, nil)
	checkpoint.Save("system prompt\nYou were working on this before a restart", 2, []string{"read_file", "edit_file"}, nil)
	snapshot := checkpoint.Load()
	if snapshot == nil || snapshot.Goal != "original task" || snapshot.Round != 2 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestIterationGuardrailStopsRunawayLoop(t *testing.T) {
	home := makeTestHome(t)
	tools := makeEvalTools(home)

	// Script 99 save_note calls — loop should stop at maxIter
	var script []*LLMResponse
	for i := 0; i < 99; i++ {
		script = append(script, scriptedResp([]ContentBlock{
			toolBlock("save_note", map[string]any{"subject": fmt.Sprintf("x%d", i), "content": "y"}),
		}, "tool_use"))
	}

	result := RunLoop(&fakeClient{script: script}, "eval", "", nil, tools, 3, 2048, nil, false, nil, home, nil)

	if result.Iterations != 3 {
		t.Errorf("expected 3 iterations (max), got %d", result.Iterations)
	}
	if !strings.Contains(result.Reply, "iteration limit") {
		t.Errorf("expected iteration limit message, got: %s", result.Reply)
	}
}

func TestResolveApprovalApprovesAndCleansUp(t *testing.T) {
	home := makeTestHome(t)
	tools := makeEvalTools(home)

	// First create a pending approval
	pending := filepath.Join(home, "pending")
	os.MkdirAll(pending, 0700)
	os.WriteFile(filepath.Join(pending, "test-approve.json"), []byte(`{
		"action_id": "test-approve",
		"title": "Delete test file",
		"details": "test",
		"exec_plan": "Run: rm /tmp/test.txt"
	}`), 0600)

	// Now simulate LLM calling resolve_approval with approve
	script := []*LLMResponse{
		scriptedResp([]ContentBlock{
			toolBlock("resolve_approval", map[string]any{
				"action_id": "test-approve",
				"decision":  "approve",
				"reason":    "go ahead",
			}),
		}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Approval resolved.")}, "tool_use"),
	}

	RunLoop(&fakeClient{script: script}, "eval", "", nil, tools, 10, 2048, nil, false, nil, home, nil)

	// Pending file must be removed
	if _, err := os.Stat(filepath.Join(pending, "test-approve.json")); err == nil {
		t.Error("pending file not removed after approval")
	}
}

func TestResolveApprovalRejectsAndCleansUp(t *testing.T) {
	home := makeTestHome(t)
	tools := makeEvalTools(home)

	pending := filepath.Join(home, "pending")
	os.MkdirAll(pending, 0700)
	os.WriteFile(filepath.Join(pending, "test-reject.json"), []byte(`{
		"action_id": "test-reject",
		"title": "Delete important file",
		"details": "test",
		"exec_plan": "rm /important"
	}`), 0600)

	script := []*LLMResponse{
		scriptedResp([]ContentBlock{
			toolBlock("resolve_approval", map[string]any{
				"action_id": "test-reject",
				"decision":  "reject",
				"reason":    "that file is important",
			}),
		}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Rejected. File kept.")}, "tool_use"),
	}

	RunLoop(&fakeClient{script: script}, "eval", "", nil, tools, 10, 2048, nil, false, nil, home, nil)

	if _, err := os.Stat(filepath.Join(pending, "test-reject.json")); err == nil {
		t.Error("pending file not removed after rejection")
	}
}

func TestPendingApprovalsInSystemPrompt(t *testing.T) {
	home := makeTestHome(t)

	// Create a pending approval
	pending := filepath.Join(home, "pending")
	os.MkdirAll(pending, 0700)
	os.WriteFile(filepath.Join(pending, "view-approve.json"), []byte(`{
		"action_id": "view-approve",
		"title": "Delete 7 emails",
		"details": "7 old promotional emails",
		"exec_plan": "delete IDs 1-7"
	}`), 0600)

	s := &Session{settings: &Settings{Home: home}}
	sys := s.BuildSystem("hello", "dashboard")

	if !strings.Contains(sys, "PENDING APPROVALS") {
		t.Errorf("system prompt missing PENDING APPROVALS section. got:\n%s", sys)
	}
	if !strings.Contains(sys, "view-approve") {
		t.Errorf("system prompt missing pending approval ID. got:\n%s", sys)
	}
	if !strings.Contains(sys, "Delete 7 emails") {
		t.Errorf("system prompt missing pending approval title. got:\n%s", sys)
	}
}

func TestTelegramRulesOnlyInTelegramSystemPrompt(t *testing.T) {
	home := makeTestHome(t)
	s := &Session{settings: &Settings{Home: home}}

	telegram := s.BuildSystem("hello", "telegram")
	if !strings.Contains(telegram, "Reply to the user ONLY after all tools have completed") ||
		!strings.Contains(telegram, "Never say 'Let me...' in Telegram mode") {
		t.Fatalf("Telegram system prompt missing tool silence rules. got:\n%s", telegram)
	}
	if dashboard := s.BuildSystem("hello", "dashboard"); strings.Contains(dashboard, "responding via Telegram") {
		t.Fatalf("dashboard system prompt contains Telegram-only rules. got:\n%s", dashboard)
	}
}

func TestDefaultSoulRequiresCompletionWithoutToolCallCap(t *testing.T) {
	for _, want := range []string{
		"Do not impose your own tool-call limit",
		"A failed tool result is evidence, not completion",
		"Truncation is not failure",
		"Never guess missing output",
	} {
		if !strings.Contains(defaultSoul, want) {
			t.Errorf("default soul missing %q", want)
		}
	}
	if strings.Contains(defaultSoul, "After 3 tool calls") || strings.Contains(defaultSoul, "When in doubt, REPLY") {
		t.Fatalf("default soul retains an early-stop rule:\n%s", defaultSoul)
	}
}
