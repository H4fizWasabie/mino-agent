package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// post_mortem (CTX-017): after a failed run the harness extracts a bounded
// failure-evidence block from the run's trace window so the LLM can diagnose
// from evidence instead of re-scanning (re-scanning itself churns to the cap —
// observed 2026-08-12). The harness finds the run and extracts the signals;
// the LLM renders the wayfinder-style ticket. Claims must cite the returned
// iteration numbers, or be labeled a hypothesis.

func makePostMortemTool(home string) *Tool {
	return &Tool{
		Name:        "post_mortem",
		Description: "Diagnose a failed playbook run: extract bounded failure evidence (parse failures, outcome contradictions, stage-rewrite streaks, iteration usage, final reply) from the run's trace for the LLM to map to a mechanism. Use after a run_playbook result reports 'failed', or when the user asks why a run failed. Pass 'playbook' to target one; omit to use the most recent failed run.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"playbook": map[string]any{"type": "string", "description": "Optional playbook name to diagnose; omit for the most recent failed run across all playbooks."},
			},
		},
		Fn: func(args map[string]any) string {
			name, _ := args["playbook"].(string)
			ev, runID, err := extractFailureEvidence(home, name)
			if err != nil {
				return fmt.Sprintf("post_mortem error: %v", err)
			}
			if runID == "" {
				return "post_mortem: no failed run found to diagnose."
			}
			return ev
		},
	}
}

// extractFailureEvidence finds the newest failed run (across all playbooks, or
// the named one) and returns its trace evidence. Returns ("", "", nil) when no
// failed run exists.
func extractFailureEvidence(home, playbookName string) (string, string, error) {
	names := []string{playbookName}
	if playbookName == "" {
		names = ListPlaybooks(home)
	}
	var best *PlaybookRun
	var bestPb string
	for _, name := range names {
		if name == "" {
			continue
		}
		pb, err := loadPlaybookWorkspace(home, name)
		if err != nil || pb == nil {
			continue
		}
		run := newestFailedRun(pb)
		if run == nil {
			continue
		}
		if best == nil || run.CreatedAt.After(best.CreatedAt) {
			best, bestPb = run, name
		}
	}
	if best == nil {
		return "", "", nil
	}
	ev := traceFailureEvidence(home, best, bestPb)
	if ev == "" {
		ev = fmt.Sprintf("run %s (%s) failed, but no trace signals were found in the run window — inspect the run's stage outputs.", best.ID, bestPb)
	}
	return ev, best.ID, nil
}

// newestFailedRun returns the most recently created run with Status "failed".
func newestFailedRun(pb *PlaybookWorkspace) *PlaybookRun {
	entries, err := os.ReadDir(playbookRunsDir(pb))
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	for _, id := range ids {
		data, err := os.ReadFile(filepath.Join(playbookRunsDir(pb), id, "state.json"))
		if err != nil {
			continue
		}
		var run PlaybookRun
		if json.Unmarshal(data, &run) == nil && run.Status == "failed" {
			return &run
		}
	}
	return nil
}

// traceFailureEvidence scans the run's trace window (CreatedAt..UpdatedAt) and
// returns a bounded evidence block of failure signals with iteration numbers
// the LLM can cite. Bounded: only signal types, last-3 turns, ~1200 chars.
func traceFailureEvidence(home string, run *PlaybookRun, pbName string) string {
	date := run.CreatedAt.UTC().Format("2006-01-02")
	fpath := filepath.Join(home, "traces", date+".jsonl")
	f, err := os.Open(fpath)
	if err != nil {
		return ""
	}
	defer f.Close()
	start, end := run.CreatedAt.UTC(), run.UpdatedAt.UTC()
	var parseFails, contradicted, rewrites, turns, gates []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var e struct {
			Type       string `json:"type"`
			TS         string `json:"ts"`
			Iteration  int    `json:"iteration"`
			Iterations int    `json:"iterations"`
			Reply      string `json:"reply"`
			Stage      string `json:"stage"`
			Streak     int    `json:"streak"`
			Decision   string `json:"decision"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Type == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339, e.TS)
		if err != nil || ts.Before(start) || ts.After(end) {
			continue
		}
		switch e.Type {
		case "tool_call_parse_failed":
			parseFails = append(parseFails, fmt.Sprintf("%d", e.Iteration))
		case "outcome_claim_contradicted":
			contradicted = append(contradicted, fmt.Sprintf("%d", e.Iteration))
		case "stage_rewrite_streak":
			rewrites = append(rewrites, fmt.Sprintf("stage %q streak %d", e.Stage, e.Streak))
		case "turn_end":
			reply := strings.TrimSpace(e.Reply)
			if len(reply) > 200 {
				reply = reply[:200]
			}
			turns = append(turns, fmt.Sprintf("iterations=%d reply=%q", e.Iterations, reply))
		case "gate":
			if e.Decision != "" {
				gates = append(gates, e.Decision)
			}
		}
	}
	if len(turns) > 3 {
		turns = turns[len(turns)-3:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "FAILURE EVIDENCE — run %s (%s), status %s", run.ID, pbName, run.Status)
	if len(parseFails) > 0 {
		fmt.Fprintf(&b, "\n- %d parse-failure(s) at iterations %s (repeated malformed tool calls — spiral signature)", len(parseFails), strings.Join(parseFails, ", "))
	}
	if len(contradicted) > 0 {
		fmt.Fprintf(&b, "\n- %d outcome-claim-contradiction(s) at iteration(s) %s (fabrication signal)", len(contradicted), strings.Join(contradicted, ", "))
	}
	if len(rewrites) > 0 {
		fmt.Fprintf(&b, "\n- %d stage-rewrite-streak(s): %s", len(rewrites), strings.Join(rewrites, "; "))
	}
	if len(turns) > 0 {
		fmt.Fprintf(&b, "\n- run usage: %s", strings.Join(turns, " | "))
	}
	if len(gates) > 0 {
		fmt.Fprintf(&b, "\n- gate decisions: %s", strings.Join(gates, ", "))
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String()
}
