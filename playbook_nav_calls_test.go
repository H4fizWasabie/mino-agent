package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Regression tests for #486 (## Success verification always failed under
// navigation because verifyWorkspaceStageOutputs was always called with
// calls=nil) and #485 (duplicate send_message within one stage attempt).

// writeSuccessDeclaringPlaybook writes a one-stage playbook whose stage
// declares a ## Success outcome tied to a real tool call, the same shape as
// instagram-daily-capability/03-publish and facebook-daily-ai-post/01-post.
func writeSuccessDeclaringPlaybook(t *testing.T, home, name string) {
	t.Helper()
	root := filepath.Join(home, "playbooks", name)
	if err := os.MkdirAll(filepath.Join(root, "stages", "01-post"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CONTEXT.md"), []byte("# Test playbook\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stage := "# Post\n\n## Process\n\n1. Publish and log it.\n\n## Tools\n\n- write_file\n- threads_post\n\n" +
		"## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Log | `output/log.md` | Markdown |\n\n" +
		"## Success\n\n| Outcome | Required tool call |\n| --- | --- |\n| Post published | `threads_post` returned a post ID |\n"
	if err := os.WriteFile(filepath.Join(root, "stages", "01-post", "CONTEXT.md"), []byte(stage), 0600); err != nil {
		t.Fatal(err)
	}
}

// TestNavigatePlaybookRunVerifiesSuccessFromRecordedCalls is the regression
// test for #486: before this fix, navigatePlaybookRun always verified a
// stage's ## Success outcome against a nil call list, so a stage declaring
// ## Success could never report complete even when the declared tool
// genuinely succeeded (live: Instagram post ID 18351111289174874 still
// recorded status "failed"). noteNavCall (the loop.go hook) now records the
// real call, and navigatePlaybookRun must see it via stageNavCalls.
func TestNavigatePlaybookRunVerifiesSuccessFromRecordedCalls(t *testing.T) {
	home := t.TempDir()
	writeSuccessDeclaringPlaybook(t, home, "post-test")
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	registry.Register(&Tool{Name: "threads_post", Behavior: BehaviorMutate})
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
	sessionID := "scheduled-post-test"

	first, err := navigatePlaybookRun(context.Background(), core, "post-test", "go", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "running" {
		t.Fatalf("expected stage 1 navigation, got %+v", first)
	}

	pb, err := loadPlaybookWorkspace(home, "post-test")
	if err != nil {
		t.Fatal(err)
	}
	run, err := latestPlaybookRun(pb)
	if err != nil || run == nil {
		t.Fatalf("expected a run on disk: %v", err)
	}
	stage, _ := workspaceStage(pb, 1)
	out := playbookRunOutputPath(pb, run, stage, stage.Outputs[0])
	if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte("posted"), 0600); err != nil {
		t.Fatal(err)
	}

	// Simulate what loop.go's hook does while the model works this stage: a
	// real threads_post call with a genuine 15+ digit post ID, recorded via
	// noteNavCall exactly as the tool-execution loop would.
	noteNavCall(run.ID, ToolCall{Name: "write_file", Args: map[string]any{"path": out}, Output: "ok"})
	noteNavCall(run.ID, ToolCall{Name: "threads_post", Output: `{"result": "18351111289174874"}`})

	second, err := navigatePlaybookRun(context.Background(), core, "post-test", "go", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "complete" {
		t.Fatalf("expected the run to complete once the recorded call proves ## Success, got %+v", second)
	}
}

// TestNavigatePlaybookRunFailsSuccessWithoutRecordedCall confirms the
// verification is still real, not a rubber stamp: no recorded call means
// still no completion.
func TestNavigatePlaybookRunFailsSuccessWithoutRecordedCall(t *testing.T) {
	home := t.TempDir()
	writeSuccessDeclaringPlaybook(t, home, "post-test")
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	registry.Register(&Tool{Name: "threads_post", Behavior: BehaviorMutate})
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
	sessionID := "scheduled-post-test"

	if _, err := navigatePlaybookRun(context.Background(), core, "post-test", "go", sessionID); err != nil {
		t.Fatal(err)
	}
	pb, err := loadPlaybookWorkspace(home, "post-test")
	if err != nil {
		t.Fatal(err)
	}
	run, err := latestPlaybookRun(pb)
	if err != nil || run == nil {
		t.Fatalf("expected a run on disk: %v", err)
	}
	stage, _ := workspaceStage(pb, 1)
	out := playbookRunOutputPath(pb, run, stage, stage.Outputs[0])
	if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte("posted"), 0600); err != nil {
		t.Fatal(err)
	}
	// No noteNavCall this time — declared output exists but the ## Success
	// tool call was never recorded.

	second, err := navigatePlaybookRun(context.Background(), core, "post-test", "go", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status == "complete" {
		t.Fatalf("expected verification to still fail without a recorded ## Success call, got %+v", second)
	}
}

// TestDuplicateNavSendMessageSuppressesExactRepeat is the regression test
// for #485: facebook-daily-ai-post sent an identical Telegram report twice,
// 2 seconds apart, both status "ok" — the stage contract's "send exactly
// once" was prose only. duplicateNavSendMessage must catch an exact repeat
// (same recipient, same content) within the same stage attempt.
func TestDuplicateNavSendMessageSuppressesExactRepeat(t *testing.T) {
	sessionID := "nav-session"
	runID := "run-1"
	setSessionNav(sessionID, "post-test", runID)
	defer clearSessionNav(sessionID)

	args := map[string]any{"to": "Abah", "message": "Published: my post"}
	if duplicateNavSendMessage(sessionID, args) {
		t.Fatal("first send_message call must not be treated as a duplicate")
	}
	noteNavCall(runID, ToolCall{Name: "send_message", Args: args, Output: "ok"})

	if !duplicateNavSendMessage(sessionID, args) {
		t.Fatal("identical second send_message call must be flagged as a duplicate")
	}

	different := map[string]any{"to": "Abah", "message": "A different report"}
	if duplicateNavSendMessage(sessionID, different) {
		t.Fatal("a genuinely different message must not be suppressed")
	}
}

// TestDuplicateNavSendMessageInactiveOutsideNavigation confirms the guard is
// a no-op for ordinary chat sessions that are not navigating a playbook —
// it must never suppress a legitimate repeated chat reply.
func TestDuplicateNavSendMessageInactiveOutsideNavigation(t *testing.T) {
	args := map[string]any{"to": "Abah", "message": "hello"}
	if duplicateNavSendMessage("plain-chat-session", args) {
		t.Fatal("a non-navigating session must never be treated as having a duplicate")
	}
}

// TestNavCallsClearedBetweenStageAttempts confirms the tracker resets at
// each verify — a retry or the next stage must start with an empty window,
// matching the old dedicated loop's one-call-list-per-attempt granularity.
func TestNavCallsClearedBetweenStageAttempts(t *testing.T) {
	runID := "run-clear-test"
	noteNavCall(runID, ToolCall{Name: "write_file", Output: "ok"})
	if got := stageNavCalls(runID); len(got) != 1 {
		t.Fatalf("expected 1 recorded call, got %d", len(got))
	}
	clearNavCalls(runID)
	if got := stageNavCalls(runID); len(got) != 0 {
		t.Fatalf("expected the tracker to be empty after clearNavCalls, got %d: %v", len(got), got)
	}
}

func TestDeferredToolExecutionIsRecordedForNavigation(t *testing.T) {
	runID, sessionID := "run-dispatch-test", "scheduled-dispatch-test"
	setSessionNav(sessionID, "post-test", runID)
	defer clearSessionNav(sessionID)

	registry := NewRegistry()
	registry.Register(&Tool{
		Name:     "threads_post",
		Behavior: BehaviorMutate,
		Fn:       func(map[string]any) string { return `{"result":"18351111289174874"}` },
	})
	registry.Register(makeToolCallTool(registry))
	ctx := context.WithValue(context.Background(), sessionIDKey{}, sessionID)
	got := registry.ExecuteContext(ctx, toolCallName, map[string]any{
		"name": "threads_post",
	})
	if got != `{"result":"18351111289174874"}` {
		t.Fatalf("dispatcher returned %q", got)
	}
	calls := stageNavCalls(runID)
	for _, call := range calls {
		if call.Name == "threads_post" && toolOutputStatus(call.Output) == "ok" && outcomeID.MatchString(call.Output) {
			return
		}
	}
	t.Fatalf("deferred tool call was not recorded: %+v", calls)
}
