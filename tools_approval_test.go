package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApprovalToolsRoundTrip(t *testing.T) {
	home := t.TempDir()
	request := makeRequestApprovalTool(home)
	got := request.Fn(map[string]any{
		"action_id": "delete-test",
		"title":     "Delete test file",
		"details":   "Remove the temporary test file",
		"exec_plan": "rm /tmp/test-file",
	})
	if !strings.Contains(got, "APPROVAL_REQUIRED") {
		t.Fatalf("request approval: %q", got)
	}

	resolve := makeResolveApprovalTool(home)
	ctx := context.WithValue(context.Background(), userMessageKey{}, "yes")
	got = resolve.ContextFn(ctx, map[string]any{"action_id": "delete-test", "decision": "approve"})
	if !strings.Contains(got, "APPROVED") {
		t.Fatalf("resolve approval: %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, "approved", "delete-test.json")); err != nil {
		t.Fatalf("approved record missing: %v", err)
	}
}

func TestApprovalRequiresExplicitUserConfirmation(t *testing.T) {
	home := t.TempDir()
	makeRequestApprovalTool(home).Fn(map[string]any{
		"action_id": "needs-confirmation",
		"title":     "Change configuration",
		"details":   "Change a configuration file",
		"exec_plan": "edit config",
	})

	resolve := makeResolveApprovalTool(home)
	ctx := context.WithValue(context.Background(), userMessageKey{}, "show pending approvals")
	got := resolve.ContextFn(ctx, map[string]any{"action_id": "needs-confirmation", "decision": "approve"})
	if !strings.Contains(got, "explicit user message") {
		t.Fatalf("expected explicit confirmation error, got %q", got)
	}
}
