package main

import (
	"testing"
	"time"
)

// stallPredicate is the wedge signature (turn in flight, no loop activity past
// the threshold) — the shape checkLoopStall inlines in production.
func stallPredicate(threshold time.Duration) bool {
	loopWatch.mu.Lock()
	defer loopWatch.mu.Unlock()
	return loopWatch.inFlight > 0 && time.Since(loopWatch.lastActivity) > threshold
}

// OBS-001: the stall heartbeat fires on per-active-turn staleness — the wedge
// signature (turn in flight, no loop activity) — and pages once per episode.
func TestLoopStallHeartbeat(t *testing.T) {
	loopWatch = loopWatchState{} // clean slate

	// No turn in flight → never stalled.
	if stallPredicate(time.Second) {
		t.Fatal("stalled with no in-flight turn")
	}
	markTurnStart()
	markLoopActivity()
	if stallPredicate(time.Second) {
		t.Fatal("stalled with fresh activity")
	}
	// Age the activity past the threshold.
	loopWatch.mu.Lock()
	loopWatch.lastActivity = time.Now().Add(-2 * time.Second)
	loopWatch.mu.Unlock()
	if !stallPredicate(time.Second) {
		t.Fatal("not stalled after threshold — wedge undetected")
	}

	// Page once per episode (the check uses the env threshold, 10 min default).
	var pages []string
	notify := func(m string) { pages = append(pages, m) }
	loopWatch.mu.Lock()
	loopWatch.lastActivity = time.Now().Add(-11 * time.Minute)
	loopWatch.mu.Unlock()
	checkLoopStall(notify)
	checkLoopStall(notify)
	if len(pages) != 1 {
		t.Fatalf("paged %d times, want 1 per episode", len(pages))
	}

	// Turn ends → episode resolved → next stall can page again.
	markTurnEnd()
	if stallPredicate(time.Second) {
		t.Fatal("stalled after turn ended")
	}
	markTurnStart()
	loopWatch.mu.Lock()
	loopWatch.lastActivity = time.Now().Add(-11 * time.Minute)
	loopWatch.mu.Unlock()
	checkLoopStall(notify)
	if len(pages) != 2 {
		t.Fatalf("paged %d times after episode reset, want 2", len(pages))
	}
}

// OBS-001: logTrace feeds the watcher — loop events bump activity, turn
// markers move the in-flight counter. The wedge kept background tickers
// tracing while the loop was frozen; only loop events count.
func TestLogTraceFeedsLoopWatch(t *testing.T) {
	loopWatch = loopWatchState{}
	home := t.TempDir()
	logTrace(home, "turn_start", nil)
	logTrace(home, "llm", map[string]any{"in": 1})
	logTrace(home, "graph_edge_judgment", map[string]any{"edges": 1}) // background — must NOT count as loop activity

	loopWatch.mu.Lock()
	inFlight := loopWatch.inFlight
	loopWatch.mu.Unlock()
	if inFlight != 1 {
		t.Fatalf("inFlight = %d, want 1", inFlight)
	}

	// Background traces must not refresh the loop clock: simulate the wedge —
	// only background traces flow after the activity mark.
	loopWatch.mu.Lock()
	loopWatch.lastActivity = time.Now().Add(-2 * time.Second)
	loopWatch.mu.Unlock()
	logTrace(home, "graph_edge_judgment", nil) // would refresh if wrongly counted
	if !stallPredicate(time.Second) {
		t.Fatal("background trace refreshed the loop clock — wedge undetected")
	}
	// Real loop activity does refresh.
	logTrace(home, "tool", map[string]any{"tool": "bash"})
	if stallPredicate(time.Second) {
		t.Fatal("loop activity did not refresh the clock")
	}
	logTrace(home, "turn_end", nil)
	loopWatch.mu.Lock()
	inFlight = loopWatch.inFlight
	loopWatch.mu.Unlock()
	if inFlight != 0 {
		t.Fatalf("inFlight = %d after turn_end, want 0", inFlight)
	}
}
