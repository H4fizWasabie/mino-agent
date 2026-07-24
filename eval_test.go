package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestProjectStateIsExplicitAndRoundTrips(t *testing.T) {
	home := makeTestHome(t)
	tools := makeEvalTools(home)
	if got := tools.Execute("project_get", map[string]any{"name": "resume-builder"}); !strings.Contains(got, "No project state found") {
		t.Fatalf("unexpected missing-state response: %q", got)
	}
	if got := tools.Execute("project_update", map[string]any{
		"name": "resume-builder", "objective": "Improve the UI", "next_action": "Review on laptop",
	}); !strings.Contains(got, "updated") {
		t.Fatalf("update response: %q", got)
	}
	got := tools.Execute("project_get", map[string]any{"name": "resume-builder"})
	for _, want := range []string{"Improve the UI", "active", "Review on laptop"} {
		if !strings.Contains(got, want) {
			t.Fatalf("project state missing %q: %s", want, got)
		}
	}
	tools.Execute("project_update", map[string]any{"name": "resume-builder", "status": "blocked", "blocker": "Waiting for review"})
	tools.Execute("project_update", map[string]any{"name": "resume-builder", "next_action": "Ask for review"})
	got = tools.Execute("project_get", map[string]any{"name": "resume-builder"})
	if !strings.Contains(got, "status: blocked") || !strings.Contains(got, "Ask for review") {
		t.Fatalf("partial update reset project status: %s", got)
	}
}

func TestProjectStateDoesNotAppearForUntrackedTasks(t *testing.T) {
	home := makeTestHome(t)
	tools := makeEvalTools(home)
	if got := tools.Execute("project_get", map[string]any{"name": "one-off"}); !strings.Contains(got, "No project state found") {
		t.Fatalf("unexpected implicit project creation: %q", got)
	}
}

func TestProjectToolsRemainAvailableAsAPair(t *testing.T) {
	f := NewToolFilter([]string{"project_get", "project_update"}, 0)
	tools := []ToolDef{
		{Name: "project_get", Description: "Read project state"},
		{Name: "project_update", Description: "Update project state"},
	}
	got := f.Filter("update the project", tools, nil)
	if len(got) != 2 {
		t.Fatalf("project tool pair was filtered: %#v", got)
	}
}

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
		scriptedResp([]ContentBlock{finishBlock("blocked", "paused for test")}, "tool_use"),
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
		scriptedResp([]ContentBlock{finishBlock("blocked", "Awaiting your approval.")}, "tool_use"),
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

func TestPendingApprovalCannotCompleteTurn(t *testing.T) {
	home := makeTestHome(t)
	tools := makeEvalTools(home)
	script := []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("request_approval", map[string]any{
			"action_id": "delete-file",
			"title":     "Delete a file",
			"details":   "The file is no longer needed",
			"exec_plan": "rm /tmp/file",
		})}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Deleted.")}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("blocked", "Waiting for approval.")}, "tool_use"),
	}
	result := RunLoop(&fakeClient{script: script}, "eval", "", nil, tools, 4, 2048, nil, false, nil, home, nil)
	if result.Status != "blocked" || result.Iterations != 3 {
		t.Fatalf("pending approval completed the turn: %#v", result)
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

func TestReadOnlyStreakNudgesModelTowardMutation(t *testing.T) {
	home := makeTestHome(t)
	path := filepath.Join(home, "target.txt")
	if err := os.WriteFile(path, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	tools := NewRegistry()
	tools.Register(behaves(makeReadTool(), BehaviorObserve))
	tools.Register(behaves(&Tool{Name: "probe_mutate", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string {
		if err := os.WriteFile(path, []byte("after"), 0644); err != nil {
			return "Error: " + err.Error()
		}
		return "mutated " + path
	}}, BehaviorMutate))
	script := make([]*LLMResponse, 0, 7)
	for i := 0; i < maxReadOnlyStreak; i++ {
		script = append(script, scriptedResp([]ContentBlock{toolBlock("read_file", map[string]any{"path": path, "offset": i})}, "tool_use"))
	}
	script = append(script,
		scriptedResp([]ContentBlock{toolBlock("probe_mutate", map[string]any{})}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Fixed.")}, "tool_use"),
	)
	result := RunLoop(&fakeClient{script: script}, "eval", "Fix the target file", nil, tools, 10, 2048, nil, false, nil, home, nil)
	if result.Status != "complete" || result.Iterations != 7 {
		t.Fatalf("result=%#v", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "after" {
		t.Fatalf("mutation did not run: %q", data)
	}
}

func TestSuccessfulMutationCannotFinalizeFromNarration(t *testing.T) {
	home := makeTestHome(t)
	tools := NewRegistry()
	tools.Register(behaves(&Tool{Name: "project_update", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string { return "mutation succeeded" }}, BehaviorMutate))
	script := []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("project_update", map[string]any{})}, "tool_use"),
		scriptedResp([]ContentBlock{textBlock("The change is ready.")}, "end_turn"),
		scriptedResp([]ContentBlock{textBlock("The requested change is complete.")}, "end_turn"),
		scriptedResp([]ContentBlock{textBlock("Final result: mutation succeeded.")}, "end_turn"),
	}
	result := RunLoop(&fakeClient{script: script}, "eval", "make the requested change", nil, tools, 6, 2048, nil, false, nil, home, nil)
	if result.Status != "stalled" || result.Iterations != 4 {
		t.Fatalf("result=%#v", result)
	}
}

func TestReadOnlySuccessCannotImplicitlyCompleteMutationTask(t *testing.T) {
	home := makeTestHome(t)
	tools := NewRegistry()
	tools.Register(behaves(&Tool{Name: "read_file", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string { return "inspected" }}, BehaviorObserve))
	script := []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("read_file", map[string]any{})}, "tool_use"),
		scriptedResp([]ContentBlock{textBlock("I found the issue.")}, "end_turn"),
		scriptedResp([]ContentBlock{textBlock("The change should work.")}, "end_turn"),
		scriptedResp([]ContentBlock{textBlock("Done.")}, "end_turn"),
	}
	result := RunLoop(&fakeClient{script: script}, "eval", "fix the file", nil, tools, 4, 2048, nil, false, nil, home, nil)
	if result.Status != "stalled" {
		t.Fatalf("read-only work implicitly completed: %#v", result)
	}
}

func TestImplicitCompletionUsesFileVerification(t *testing.T) {
	home := makeTestHome(t)
	missing := filepath.Join(home, "missing.txt")
	tools := NewRegistry()
	tools.Register(behaves(&Tool{Name: "write_file", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string {
		return "Wrote 1 bytes to " + missing
	}}, BehaviorMutate))
	script := []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("write_file", map[string]any{})}, "tool_use"),
		scriptedResp([]ContentBlock{textBlock("The file is ready.")}, "end_turn"),
		scriptedResp([]ContentBlock{textBlock("The requested write is complete.")}, "end_turn"),
		scriptedResp([]ContentBlock{textBlock("Final result: file created.")}, "end_turn"),
	}
	result := RunLoop(&fakeClient{script: script}, "eval", "create the file", nil, tools, 4, 2048, nil, false, nil, home, nil)
	if result.Status == "complete" {
		t.Fatalf("missing file passed implicit verification: %#v", result)
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
	tools.Register(makeEditTool())
	tools.Register(makeReadTool())
	tests := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"nil object", "bash", nil},
		{"missing required field", "bash", map[string]any{}},
		{"empty bash command", "bash", map[string]any{"command": "  "}},
		{"wrong field type", "bash", map[string]any{"command": 7}},
		{"missing write content", "write_file", map[string]any{"path": "/tmp/unused"}},
		{"invalid enum", "write_file", map[string]any{"path": "/tmp/unused", "content": "", "mode": "replace"}},
		{"fractional integer", "read_file", map[string]any{"path": "/tmp/unused", "offset": 1.5}},
		{"invalid nested edit", "edit_file", map[string]any{"path": "/tmp/unused", "edits": []any{map[string]any{"oldText": "a", "newText": false}}}},
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

func TestSyncFileReturnsVerifiedReceipt(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.html")
	destination := filepath.Join(dir, "nested", "destination.html")
	if err := os.WriteFile(source, []byte("<main>Mino</main>"), 0640); err != nil {
		t.Fatal(err)
	}
	tools := NewRegistry()
	tools.Register(makeSyncFileTool())
	got := tools.Execute("sync_file", map[string]any{"source": source, "destination": destination})
	if !strings.HasPrefix(got, "sync_receipt ") || !strings.Contains(got, `"verified":true`) {
		t.Fatalf("sync_file = %q", got)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "<main>Mino</main>" {
		t.Fatalf("destination=%q err=%v", data, err)
	}
}

func TestRawSCPRequiresStructuredTransferProof(t *testing.T) {
	home := makeTestHome(t)
	tools := NewRegistry()
	tools.Register(&Tool{Name: "bash", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string {
		return "(no output)"
	}})
	script := []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("bash", map[string]any{"command": "scp /tmp/a user@laptop:/tmp/a"})}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Transferred.")}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("blocked", "The raw transfer lacks destination proof.")}, "tool_use"),
	}
	result := RunLoop(&fakeClient{script: script}, "eval", "send the file", nil, tools, 4, 2048, nil, false, nil, home, nil)
	if result.Status != "blocked" || result.Iterations != 3 {
		t.Fatalf("raw scp completion accepted: %#v", result)
	}
}

func TestToolBehaviorMetadata(t *testing.T) {
	tools := NewRegistry()
	tools.Register(behaves(makeReadTool(), BehaviorObserve))
	tools.Register(behaves(makeWriteTool(), BehaviorMutate))
	bash := makeBashTool()
	bash.Classify = classifyBash
	tools.Register(bash)
	tests := []struct {
		name, tool, command string
		want                ToolBehavior
	}{
		{"read tool", "read_file", "", BehaviorObserve},
		{"write tool", "write_file", "", BehaviorMutate},
		{"bash observation", "bash", "sha256sum /tmp/file", BehaviorObserve},
		{"bash mutation", "bash", "cp /tmp/a /tmp/b", BehaviorUnknown},
		{"bash compound", "bash", "ls /tmp && rm /tmp/a", BehaviorUnknown},
		{"unknown extension", "extension_tool", "", BehaviorUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tools.BehaviorFor(tt.tool, map[string]any{"command": tt.command}); got != tt.want {
				t.Fatalf("BehaviorFor() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBashDefersFileCopiesToSyncTool(t *testing.T) {
	tool := makeBashTool()
	description := tool.Description
	if !strings.Contains(description, "sync_file") || strings.Contains(description, "mv, cp") {
		t.Fatalf("bash copy guidance = %q", description)
	}
	for _, command := range []string{
		"cp /tmp/a /tmp/b",
		"scp /tmp/a user@host:/tmp/b",
		"rsync /tmp/a user@host:/tmp/b",
		`\scp /tmp/a user@host:/tmp/b`,
		"$(which scp) /tmp/a user@host:/tmp/b",
		"bash -c 'scp /tmp/a user@host:/tmp/b'",
	} {
		if got := tool.Fn(map[string]any{"command": command}); !strings.HasPrefix(got, "Error: use sync_file") {
			t.Fatalf("bash accepted %q: %q", command, got)
		}
	}
}

func TestBashRewritesSupportedCommandsWithRTK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rtk")
	script := "#!/bin/sh\n[ \"$1\" = rewrite ] || exit 1\n[ \"$2\" = \"go test ./...\" ] || exit 1\nprintf 'rtk go test ./...'\nexit 3\n"
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got := rewriteBashWithRTK(context.Background(), "go test ./..."); got != "rtk go test ./..." {
		t.Fatalf("rewrite = %q", got)
	}
	if got := rewriteBashWithRTK(context.Background(), "printf hello"); got != "printf hello" {
		t.Fatalf("unsupported command changed: %q", got)
	}
}

func TestDestructiveBashRequiresAndConsumesApproval(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "delete-me")
	if err := os.WriteFile(target, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	command := "rm -- " + shellQuote(target)
	tool := makeBashToolFor(home, time.Second)
	if got := tool.Fn(map[string]any{"command": command}); !strings.HasPrefix(got, "[APPROVAL_REQUIRED]") {
		t.Fatalf("destructive command was not gated: %q", got)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("unapproved command changed the file")
	}

	approvedDir := filepath.Join(home, "approved")
	os.MkdirAll(approvedDir, 0700)
	approvalPath := filepath.Join(approvedDir, "delete-test.json")
	data, _ := json.Marshal(map[string]any{"exec_plan": command})
	os.WriteFile(approvalPath, data, 0600)
	if got := tool.Fn(map[string]any{"command": command, "approval_id": "delete-test"}); strings.HasPrefix(got, "Error:") {
		t.Fatalf("approved command failed: %q", got)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("approved command did not delete the target")
	}
	if _, err := os.Stat(approvalPath); !os.IsNotExist(err) {
		t.Fatal("approval was not consumed")
	}
}

func TestModelCannotSelfApproveDestructiveAction(t *testing.T) {
	home := makeTestHome(t)
	pending := filepath.Join(home, "pending")
	os.MkdirAll(pending, 0700)
	path := filepath.Join(pending, "delete-test.json")
	os.WriteFile(path, []byte(`{"title":"Delete file","exec_plan":"rm /tmp/file"}`), 0600)
	tool := makeResolveApprovalTool(home)
	ctx := context.WithValue(context.Background(), turnMessageKey{}, "Please clean this up.")
	got := tool.ContextFn(ctx, map[string]any{"action_id": "delete-test", "decision": "approve"})
	if !strings.HasPrefix(got, "Error: approval must come") {
		t.Fatalf("self-approval was accepted: %q", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("rejected self-approval removed the pending request")
	}
}

func TestCompletionVerifiesEveryChangedFile(t *testing.T) {
	home := makeTestHome(t)
	first := filepath.Join(home, "first.txt")
	second := filepath.Join(home, "second.txt")
	tools := NewRegistry()
	tools.Register(behaves(&Tool{Name: "write_file", Schema: map[string]any{
		"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"},
	}, Fn: func(args map[string]any) string {
		path, _ := args["path"].(string)
		if path == first {
			os.WriteFile(path, []byte("ok"), 0600)
		}
		return "Wrote 2 bytes to " + path
	}}, BehaviorMutate))
	script := []*LLMResponse{
		scriptedResp([]ContentBlock{
			{Type: "tool_use", ID: "first", Name: "write_file", Input: map[string]any{"path": first}},
			{Type: "tool_use", ID: "second", Name: "write_file", Input: map[string]any{"path": second}},
		}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Both files are ready.")}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("blocked", "The second file could not be verified.")}, "tool_use"),
	}
	result := RunLoop(&fakeClient{script: script}, "eval", "", nil, tools, 4, 2048, nil, false, nil, home, nil)
	if result.Status != "blocked" || result.Iterations != 3 {
		t.Fatalf("missing second file passed verification: %#v", result)
	}
}

func TestOneShotScheduleRemovesItself(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	jobs := []ScheduledJob{{ID: "once", Schedule: now.Format("15:04"), Prompt: "run once", Once: true}}
	if err := writeScheduleFile(home, jobs); err != nil {
		t.Fatal(err)
	}
	calls := 0
	scheduler := NewScheduler(home, now.Location(), func(string, bool) { calls++ })
	scheduler.tick()
	if calls != 1 {
		t.Fatalf("callback calls = %d", calls)
	}
	data, _ := os.ReadFile(filepath.Join(home, "schedule.json"))
	var remaining []ScheduledJob
	json.Unmarshal(data, &remaining)
	if len(remaining) != 0 {
		t.Fatalf("one-shot schedule remained: %#v", remaining)
	}
}

func TestScheduleRejectsInvalidAndExactDuplicate(t *testing.T) {
	home := t.TempDir()
	if got := addScheduledJob(home, map[string]any{"id": "bad id", "schedule": "09:00", "prompt": "x"}); !strings.HasPrefix(got, "Error:") {
		t.Fatalf("invalid ID accepted: %q", got)
	}
	if got := addScheduledJob(home, map[string]any{"id": "first", "schedule": "09:00", "prompt": "Daily briefing"}); strings.HasPrefix(got, "Error:") {
		t.Fatal(got)
	}
	if got := addScheduledJob(home, map[string]any{"id": "second", "schedule": "09:00", "prompt": "Daily briefing"}); !strings.Contains(got, "duplicate schedule") {
		t.Fatalf("duplicate accepted: %q", got)
	}
}

func TestCopyFailureMakesSyncToolAvailable(t *testing.T) {
	home := makeTestHome(t)
	source := filepath.Join(home, "source")
	destination := filepath.Join(home, "destination")
	if err := os.WriteFile(source, []byte("verified"), 0644); err != nil {
		t.Fatal(err)
	}
	tools := NewRegistry()
	bash := makeBashTool()
	bash.Classify = classifyBash
	tools.Register(bash)
	tools.Register(behaves(makeSyncFileTool(), BehaviorMutate))
	tools.SetFilter(NewToolFilter([]string{"bash"}, 0))
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("bash", map[string]any{"command": "cp " + source + " " + destination})}, "tool_use"),
		scriptedResp([]ContentBlock{toolBlock("sync_file", map[string]any{"source": source, "destination": destination})}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Copied and verified.")}, "tool_use"),
	}}
	result := RunLoop(client, "eval", "", nil, tools, 4, 2048, nil, false, nil, home, nil)
	if result.Status != "complete" || len(client.toolSets) < 2 {
		t.Fatalf("result=%#v toolsets=%#v", result, client.toolSets)
	}
	found := false
	for _, schema := range client.toolSets[1] {
		found = found || schema.Name == "sync_file"
	}
	if !found {
		t.Fatalf("sync_file not exposed after rejected copy: %#v", client.toolSets[1])
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

func TestSuccessfulReadDoesNotClearFailedMutation(t *testing.T) {
	home := makeTestHome(t)
	tools := NewRegistry()
	tools.Register(behaves(&Tool{Name: "project_update", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string {
		return "Error: update failed"
	}}, BehaviorMutate))
	tools.Register(behaves(&Tool{Name: "read_file", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string {
		return "state inspected"
	}}, BehaviorObserve))
	script := []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("project_update", map[string]any{})}, "tool_use"),
		scriptedResp([]ContentBlock{toolBlock("read_file", map[string]any{})}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "Done despite failure.")}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("blocked", "The project update failed.")}, "tool_use"),
	}
	result := RunLoop(&fakeClient{script: script}, "eval", "update the project", nil, tools, 5, 2048, nil, false, nil, home, nil)
	if result.Status != "blocked" || result.Iterations != 4 {
		t.Fatalf("failed mutation was cleared by read: %#v", result)
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

func TestRunLoopRetiresCompletedCheckpoint(t *testing.T) {
	home := makeTestHome(t)
	checkpoint := NewCheckpointManager(home, "task")
	tools := NewRegistry()
	tools.Register(behaves(&Tool{Name: "probe", Schema: map[string]any{"type": "object"}, Fn: func(map[string]any) string {
		return "done"
	}}, BehaviorMutate))
	RunLoop(&fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("probe", map[string]any{})}, "tool_use"),
		scriptedResp([]ContentBlock{finishBlock("complete", "complete")}, "tool_use"),
	}}, "eval", "", []Message{{Role: "user", Content: "goal"}}, tools, 3, 2048, nil, false, checkpoint, home, nil)
	if checkpoint.Load() != nil {
		t.Fatal("completed task checkpoint was retained")
	}
}

func TestStaleCheckpointExpires(t *testing.T) {
	home := t.TempDir()
	checkpoint := NewCheckpointManager(home, "stale")
	checkpoint.Save("old goal", 1, []string{"read_file"}, nil)
	old := time.Now().Add(-checkpointMaxAge - time.Hour)
	if err := os.Chtimes(checkpoint.path(), old, old); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(checkpoint.path())
	if err != nil {
		t.Fatal(err)
	}
	var snapshot TaskSnapshot
	if json.Unmarshal(data, &snapshot) != nil {
		t.Fatal("invalid checkpoint fixture")
	}
	snapshot.UpdatedAt = old.UTC().Format(time.RFC3339)
	data, _ = json.Marshal(snapshot)
	if err := os.WriteFile(checkpoint.path(), data, 0644); err != nil {
		t.Fatal(err)
	}
	if checkpoint.Load() != nil {
		t.Fatal("stale checkpoint was resumed")
	}
	if _, err := os.Stat(checkpoint.path()); !os.IsNotExist(err) {
		t.Fatal("stale checkpoint file was not retired")
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

	ctx := context.WithValue(context.Background(), turnMessageKey{}, "Yes, approve test-approve and go ahead.")
	RunLoopContext(ctx, &fakeClient{script: script}, "eval", "", nil, tools, 10, 2048, nil, false, nil, home, nil)

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
