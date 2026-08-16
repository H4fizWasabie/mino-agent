package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// CTX-018: design-time contract review — the harness applies deterministic
// agentic-principle checks (research boundedness, verification, grounding) and
// returns them for the LLM to render risk-flags. Flags are risks, not
// assertions.

func TestAuditPlaybookContractsFlagsRisks(t *testing.T) {
	home := t.TempDir()
	pb := filepath.Join(home, "playbooks", "test-pb")
	os.MkdirAll(filepath.Join(pb, "stages", "01-good"), 0700)
	os.MkdirAll(filepath.Join(pb, "stages", "02-loose"), 0700)
	os.WriteFile(filepath.Join(pb, "CONTEXT.md"), []byte("# Test PB\n"), 0600)
	os.WriteFile(filepath.Join(pb, "stages", "01-good", "CONTEXT.md"),
		[]byte("# Good stage\nResearch in ONE bounded pass, then COMMIT. Verify the output by reading it back. Never fabricate results.\n\n## Outputs\n| Artifact | Path |\n| --- | --- |\n| report | output/report.md |\n"), 0600)
	os.WriteFile(filepath.Join(pb, "stages", "02-loose", "CONTEXT.md"),
		[]byte("# Loose stage\nResearch the topic thoroughly and write the report.\n\n## Outputs\n| Artifact | Path |\n| --- | --- |\n| report | output/report.md |\n"), 0600)

	audit := auditPlaybookContracts(home, "test-pb")
	for _, want := range []string{"test-pb", "01-good", "02-loose"} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit missing %q:\n%s", want, audit)
		}
	}
	// Split into per-stage sections and assert on each.
	sections := map[string]string{}
	for _, s := range strings.Split(audit, "### Stage ") {
		for _, name := range []string{"01-good", "02-loose"} {
			if strings.HasPrefix(s, name) {
				sections[name] = s
			}
		}
	}
	// Stage 01 is bounded + verified + grounded → no RISK notes.
	if !strings.Contains(sections["01-good"], "research_bounded: true") {
		t.Errorf("stage 01 should be flagged bounded (has a commit boundary):\n%s", sections["01-good"])
	}
	if strings.Contains(sections["01-good"], "RISK") {
		t.Errorf("stage 01 should have no RISK flags:\n%s", sections["01-good"])
	}
	// Stage 02 is loose → the harness flags the churn risk for the LLM to render.
	if !strings.Contains(sections["02-loose"], "research_bounded: false") {
		t.Errorf("stage 02 should be flagged as unbounded (churn risk):\n%s", sections["02-loose"])
	}
	if !strings.Contains(sections["02-loose"], "RISK") {
		t.Errorf("stage 02 should carry a RISK note:\n%s", sections["02-loose"])
	}
}

// #238: "every" + glob + no bound must fail the gate. This is the exact
// weekly-audit shape that stayed un-flagged: a stage demanding coverage of
// "every published post" across a glob, laundered into "bounded" by a stray
// boundary word ("send EXACTLY ONCE") elsewhere in the contract.
func TestAuditPlaybookFlagsUnboundedEveryGlob(t *testing.T) {
	home := t.TempDir()
	pb := filepath.Join(home, "playbooks", "unbounded-pb")
	os.MkdirAll(filepath.Join(pb, "stages", "01-audit"), 0700)
	os.WriteFile(filepath.Join(pb, "CONTEXT.md"), []byte("# Unbounded PB\n"), 0600)
	os.WriteFile(filepath.Join(pb, "stages", "01-audit", "CONTEXT.md"),
		[]byte("# Audit\nGlob `/home/mino/.mino/playbooks/*/runs/*/stages/*/output/*.md` and read the post texts of every published post. Send the summary EXACTLY ONCE.\n\n## Outputs\n| Artifact | Path |\n| --- | --- |\n| report | output/audit.md |\n"), 0600)

	audit := auditPlaybookContracts(home, "unbounded-pb")
	if !strings.Contains(audit, "research_bounded: false") {
		t.Fatalf("every+glob+no-bound contract must fail the gate (boundary word elsewhere must not launder it):\n%s", audit)
	}
	if !strings.Contains(audit, "RISK") {
		t.Fatalf("unbounded contract must carry the churn RISK note:\n%s", audit)
	}
	// The run-time injection uses the same check.
	st := WorkspaceStage{Name: "audit", Context: "Glob `*.md` and read every file. Send the summary EXACTLY ONCE."}
	if got := stageRiskFlags(st); got == "" || !strings.Contains(got, "research_unbounded") {
		t.Fatalf("every+no-bound stage must flag research_unbounded, got %q", got)
	}
	// A bounded contract that avoids "every" stays clean.
	ok := WorkspaceStage{Name: "audit", Context: "Glob `*.md`, read at most the 10 most recent runs, stop at 30 iterations. Verify the output by reading it back. Never fabricate. Send the summary EXACTLY ONCE."}
	if got := stageRiskFlags(ok); got != "" {
		t.Fatalf("bounded contract must have no flags, got %q", got)
	}
}

func TestNeedsPlaybookAuditAdaptive(t *testing.T) {
	home := t.TempDir()
	pbDir := filepath.Join(home, "playbooks", "p")
	stageDir := filepath.Join(pbDir, "stages", "01-x")
	os.MkdirAll(stageDir, 0700)
	ctxPath := filepath.Join(stageDir, "CONTEXT.md")
	os.WriteFile(ctxPath, []byte("# c\n## Outputs\n| A | Path |\n| --- | --- |\n| r | output/r.md |\n"), 0600)
	pb := &PlaybookWorkspace{Name: "p", Dir: pbDir, Stages: []WorkspaceStage{{Number: 1, Name: "x", Dir: stageDir, Context: "# c\n"}}}
	writeRun := func(id, status string, updated time.Time) {
		os.MkdirAll(filepath.Join(pbDir, "runs", id), 0700)
		data, _ := json.Marshal(PlaybookRun{ID: id, Status: status, CreatedAt: updated.Add(-time.Minute), UpdatedAt: updated})
		os.WriteFile(filepath.Join(pbDir, "runs", id, "state.json"), data, 0600)
	}
	now := time.Now().UTC()

	if !needsPlaybookAudit(pb) {
		t.Fatal("new playbook (no history) must audit")
	}

	writeRun("r2", "complete", now)
	os.Chtimes(ctxPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)) // contract unchanged since run
	if needsPlaybookAudit(pb) {
		t.Fatal("stable, recently-successful playbook must NOT audit")
	}

	os.Chtimes(ctxPath, now.Add(time.Hour), now.Add(time.Hour)) // contract changed after run
	if !needsPlaybookAudit(pb) {
		t.Fatal("contract changed since last run must audit")
	}

	os.Chtimes(ctxPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	writeRun("r3", "failed", now.Add(time.Minute)) // newest run failed
	if !needsPlaybookAudit(pb) {
		t.Fatal("recent failed run must audit")
	}
}

func TestStageRiskFlags(t *testing.T) {
	loose := WorkspaceStage{Name: "loose", Context: "Research the topic thoroughly."}
	if got := stageRiskFlags(loose); got == "" || !strings.Contains(got, "research_unbounded") {
		t.Fatalf("loose stage should flag research_unbounded, got %q", got)
	}
	bounded := WorkspaceStage{Name: "bounded", Context: "Do ONE search, then COMMIT. Verify and never fabricate."}
	if got := stageRiskFlags(bounded); got != "" {
		t.Fatalf("bounded+verified+grounded stage should have no flags, got %q", got)
	}
}
