package main

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Tests for #445: independent, read-only tool calls in one model response
// run concurrently; anything else stays sequential, and every observable
// ordering (message history, result.ToolCalls) is unaffected either way.

func sleepyObserveTool(name string, delay time.Duration, calls *int32) *Tool {
	return &Tool{
		Name:     name,
		Behavior: BehaviorObserve,
		Schema:   map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(map[string]any) string {
			atomic.AddInt32(calls, 1)
			time.Sleep(delay)
			return name + " done"
		},
	}
}

func TestConcurrentObserveOnlyBatchRunsInParallel(t *testing.T) {
	var calls int32
	tools := NewRegistry()
	tools.Register(sleepyObserveTool("slow_a", 150*time.Millisecond, &calls))
	tools.Register(sleepyObserveTool("slow_b", 150*time.Millisecond, &calls))
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{
			{Type: "tool_use", ID: "tu_1", Name: "slow_a", Input: map[string]any{}},
			{Type: "tool_use", ID: "tu_2", Name: "slow_b", Input: map[string]any{}},
		}, "tool_use"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}

	start := time.Now()
	result := RunLoopContext(context.Background(), client, "concurrent-batch", "", []Message{{Role: "user", Content: "go"}}, tools, 5, 100, nil, t.TempDir())
	elapsed := time.Since(start)

	if result.Status != "complete" {
		t.Fatalf("result = %#v", result)
	}
	if calls != 2 {
		t.Fatalf("expected both tools to execute, got %d calls", calls)
	}
	if len(result.ToolCalls) != 2 || result.ToolCalls[0].Name != "slow_a" || result.ToolCalls[1].Name != "slow_b" {
		t.Fatalf("expected results in emission order, got %+v", result.ToolCalls)
	}
	// Sequential would take ~300ms; parallel should land close to ~150ms.
	// Generous margin against CI scheduling jitter, still well short of
	// the sequential floor.
	if elapsed >= 280*time.Millisecond {
		t.Fatalf("expected concurrent execution well under 280ms, took %s", elapsed)
	}
}

func TestMixedBatchWithMutateStaysSequential(t *testing.T) {
	var calls int32
	tools := NewRegistry()
	tools.Register(sleepyObserveTool("obs_tool", 100*time.Millisecond, &calls))
	tools.Register(&Tool{
		Name:     "mutate_tool",
		Behavior: BehaviorMutate,
		Schema:   map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(map[string]any) string {
			atomic.AddInt32(&calls, 1)
			time.Sleep(100 * time.Millisecond)
			return "mutated"
		},
	})
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{
			{Type: "tool_use", ID: "tu_1", Name: "obs_tool", Input: map[string]any{}},
			{Type: "tool_use", ID: "tu_2", Name: "mutate_tool", Input: map[string]any{}},
		}, "tool_use"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}

	start := time.Now()
	result := RunLoopContext(context.Background(), client, "mixed-batch", "", []Message{{Role: "user", Content: "go"}}, tools, 5, 100, nil, t.TempDir())
	elapsed := time.Since(start)

	if result.Status != "complete" || len(result.ToolCalls) != 2 {
		t.Fatalf("result = %#v", result)
	}
	// A batch containing a mutating call must stay fully sequential:
	// ~200ms floor, not the ~100ms a concurrent run would take.
	if elapsed < 180*time.Millisecond {
		t.Fatalf("expected sequential execution (>=180ms) with a mutating call present, took %s", elapsed)
	}
}

func TestConcurrentBatchPanicIsolated(t *testing.T) {
	tools := NewRegistry()
	tools.Register(&Tool{
		Name:     "panics",
		Behavior: BehaviorObserve,
		Schema:   map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(map[string]any) string {
			panic("boom")
		},
	})
	var calls int32
	tools.Register(sleepyObserveTool("fine", 10*time.Millisecond, &calls))
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{
			{Type: "tool_use", ID: "tu_1", Name: "panics", Input: map[string]any{}},
			{Type: "tool_use", ID: "tu_2", Name: "fine", Input: map[string]any{}},
		}, "tool_use"),
		scriptedResp([]ContentBlock{textBlock("done")}, "stop"),
	}}

	result := RunLoopContext(context.Background(), client, "panic-batch", "", []Message{{Role: "user", Content: "go"}}, tools, 5, 100, nil, t.TempDir())
	if result.Status != "complete" {
		t.Fatalf("a panicking call in the batch must not crash the loop: %#v", result)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected both results despite the panic, got %+v", result.ToolCalls)
	}
	if calls != 1 {
		t.Fatalf("expected the non-panicking call to still execute, got %d calls", calls)
	}
	if got := result.ToolCalls[0].Output; !strings.Contains(got, "panicked") {
		t.Fatalf("expected the panicking call's output to report the panic, got %q", got)
	}
}
