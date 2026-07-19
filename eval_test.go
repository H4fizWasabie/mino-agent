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
	script []*LLMResponse
	pos    int
}

func (f *fakeClient) Create(session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef) (*LLMResponse, error) {
	if f.pos >= len(f.script) {
		return &LLMResponse{StopReason: "end_turn", Content: []ContentBlock{{Type: "text", Text: "out of script"}}}, nil
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
		scriptedResp([]ContentBlock{
			textBlock("Task created!"),
		}, "end_turn"),
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
		scriptedResp([]ContentBlock{textBlock("done")}, "end_turn"),
	}

	RunLoop(&fakeClient{script: script}, "eval", "", nil, tools, 10, 2048, nil, false, chk, home, nil)

	data, _ := os.ReadFile(filepath.Join(home, "active_tasks", "eval-session.json"))
	if len(data) == 0 {
		t.Error("checkpoint not written after tool execution")
	}
	if snap := chk.Load(); snap == nil || len(snap.ToolsUsed) == 0 {
		t.Error("checkpoint doesn't contain tool use record")
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
		scriptedResp([]ContentBlock{textBlock("Awaiting your approval, Abah.")}, "end_turn"),
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

	// Verify loop ended in 1 iteration (no tool use → immediate return)
	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration (no tools = immediate return), got %d", result.Iterations)
	}
}

func TestNoToolTurnEndsInOneIteration(t *testing.T) {
	home := makeTestHome(t)
	tools := makeEvalTools(home)

	script := []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock("Paris is the capital of France.")}, "end_turn"),
	}

	result := RunLoop(&fakeClient{script: script}, "eval", "", nil, tools, 10, 2048, nil, false, nil, home, nil)

	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", result.Iterations)
	}
	if result.Reply != "Paris is the capital of France." {
		t.Errorf("wrong reply: %s", result.Reply)
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
		scriptedResp([]ContentBlock{textBlock("Executing deletion now.")}, "end_turn"),
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
		scriptedResp([]ContentBlock{textBlock("Rejected. File kept.")}, "end_turn"),
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
	sys := s.BuildSystem("hello")

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
