package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// CTX-017: the harness extracts bounded failure evidence from a failed run's
// trace window (iteration numbers the LLM can cite) — so the LLM diagnoses
// from signals instead of re-scanning (which itself churns to the cap).

func TestNewestFailedRunSkipsComplete(t *testing.T) {
	dir := t.TempDir()
	write := func(id, status string) {
		os.MkdirAll(filepath.Join(dir, "runs", id), 0700)
		data, _ := json.Marshal(PlaybookRun{ID: id, Status: status, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
		os.WriteFile(filepath.Join(dir, "runs", id, "state.json"), data, 0600)
	}
	write("20260811T050000.000000000Z", "failed") // older, failed
	write("20260812T050000.000000000Z", "complete") // newer, complete
	pb := &PlaybookWorkspace{Name: "p", Dir: dir}
	run := newestFailedRun(pb)
	if run == nil || run.ID != "20260811T050000.000000000Z" {
		t.Fatalf("newestFailedRun = %+v, want the older failed run (skip newer complete)", run)
	}
}

func TestTraceFailureEvidenceBoundsWindowAndSignals(t *testing.T) {
	home := t.TempDir()
	now := time.Now().UTC()
	run := &PlaybookRun{ID: "r1", Status: "failed", CreatedAt: now.Add(-10 * time.Minute), UpdatedAt: now}
	dir := filepath.Join(home, "traces")
	os.MkdirAll(dir, 0700)
	f, _ := os.OpenFile(filepath.Join(dir, now.Format("2006-01-02")+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	line := func(typ string, ts time.Time, kv map[string]any) {
		m := map[string]any{"type": typ, "ts": ts.UTC().Format(time.RFC3339)}
		for k, v := range kv {
			m[k] = v
		}
		b, _ := json.Marshal(m)
		f.Write(append(b, '\n'))
	}
	line("tool_call_parse_failed", now.Add(-5*time.Minute), map[string]any{"iteration": 4})
	line("outcome_claim_contradicted", now.Add(-4*time.Minute), map[string]any{"iteration": 11})
	line("turn_end", now.Add(-3*time.Minute), map[string]any{"iterations": 30, "reply": "stopped after 30 iterations"})
	line("tool_call_parse_failed", now.Add(-20*time.Minute), map[string]any{"iteration": 99}) // before window
	line("gate", now.Add(-2*time.Minute), map[string]any{"decision": "cap_hit"})
	f.Close() // close before reading back — a concurrent append fd is not guaranteed visible

	ev := traceFailureEvidence(home, run, "test-pb")
	for _, want := range []string{"r1", "test-pb", "1 parse-failure", "iterations 4", "outcome-claim-contradiction", "11", "iterations=30", "cap_hit"} {
		if !strings.Contains(ev, want) {
			t.Errorf("evidence missing %q:\n%s", want, ev)
		}
	}
	if strings.Contains(ev, "99") {
		t.Errorf("out-of-window signal leaked into evidence:\n%s", ev)
	}
}
