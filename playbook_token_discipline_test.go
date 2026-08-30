package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests for #453: token-efficiency discipline for workspace navigation.
// read_file nudges (never withholds) when a path is unchanged since its last
// read this run — scoped to an active playbook navigation only.

func TestReadFileNudgesOnUnchangedRereadDuringNavigation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.md")
	if err := os.WriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	setSessionNav("sess-nav", "brief", "run-1")
	defer clearSessionNav("sess-nav")
	ctx := context.WithValue(context.Background(), sessionIDKey{}, "sess-nav")

	tool := makeReadTool()
	first := tool.ContextFn(ctx, map[string]any{"path": path})
	if strings.Contains(first, "unchanged since") {
		t.Fatalf("first read must not carry a nudge, got %q", first)
	}
	if !strings.Contains(first, "hello") {
		t.Fatalf("first read must return content, got %q", first)
	}

	second := tool.ContextFn(ctx, map[string]any{"path": path})
	if !strings.Contains(second, "unchanged since") {
		t.Fatalf("second read of an unchanged file must carry the nudge, got %q", second)
	}
	if !strings.Contains(second, "hello") {
		t.Fatalf("nudge must never withhold real content, got %q", second)
	}
}

func TestReadFileNoNudgeOutsideNavigation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.md")
	if err := os.WriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), sessionIDKey{}, "sess-no-nav")

	tool := makeReadTool()
	for i := 0; i < 2; i++ {
		out := tool.ContextFn(ctx, map[string]any{"path": path})
		if strings.Contains(out, "unchanged since") {
			t.Fatalf("no active navigation: must never nudge, got %q", out)
		}
	}
}

func TestReadFileNoNudgeWhenFileChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.md")
	if err := os.WriteFile(path, []byte("v1"), 0600); err != nil {
		t.Fatal(err)
	}
	setSessionNav("sess-nav-2", "brief", "run-2")
	defer clearSessionNav("sess-nav-2")
	ctx := context.WithValue(context.Background(), sessionIDKey{}, "sess-nav-2")
	tool := makeReadTool()
	tool.ContextFn(ctx, map[string]any{"path": path})

	// Ensure a distinct mtime, then rewrite the file — a genuinely changed
	// read must never be told "unchanged" regardless of timing precision.
	if err := os.WriteFile(path, []byte("v2"), 0600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	out := tool.ContextFn(ctx, map[string]any{"path": path})
	if strings.Contains(out, "unchanged since") {
		t.Fatalf("changed file must never be nudged as unchanged, got %q", out)
	}
	if !strings.Contains(out, "v2") {
		t.Fatalf("expected the updated content, got %q", out)
	}
}

func TestClearSessionNavClearsReadTracker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.md")
	if err := os.WriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	setSessionNav("sess-nav-3", "brief", "run-3")
	ctx := context.WithValue(context.Background(), sessionIDKey{}, "sess-nav-3")
	tool := makeReadTool()
	tool.ContextFn(ctx, map[string]any{"path": path})

	clearSessionNav("sess-nav-3")
	setSessionNav("sess-nav-3", "brief", "run-3") // same run id, fresh navigation
	out := tool.ContextFn(ctx, map[string]any{"path": path})
	defer clearSessionNav("sess-nav-3")
	if strings.Contains(out, "unchanged since") {
		t.Fatalf("a cleared run's read tracker must not survive, got %q", out)
	}
}
