package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// audit_playbook (CTX-018): design-time contract review. The harness extracts
// each stage's contract and applies deterministic agentic-principle checks
// (research boundedness, verification, grounding, size, tool references). The
// LLM renders a RISK-FLAG list from it — flags are risks, not assertions: a
// prediction is never confirmed until the run actually fails (CTX-018).

var (
	commitBoundaryRe = regexp.MustCompile(`(?i)\b(commit|do not loop|do not re-|single pass|bounded|at most|stop after|abandon|once|exactly)\b`)
	verifyRe         = regexp.MustCompile(`(?i)\b(verify|verified|read back|check that|confirm|read it back)\b`)
	groundingRe      = regexp.MustCompile(`(?i)\b(verbatim|exact result|the tool returned|never fabricate|fabrication|fabricated|grounded|grounding)\b`)
	toolRefRe        = regexp.MustCompile(`(?i)\b(read_file|write_file|bash|search_web|generate_image|view_image|send_message|remember|save_note|manage_memory|run_playbook|list_playbooks|system_check|post_mortem|manage_playbook|sqlite3|grep)\b`)
)

func makeAuditPlaybookTool(home string) *Tool {
	return &Tool{
		Name:        "audit_playbook",
		Description: "Design-time contract review: extract a playbook's stage contracts with deterministic agentic-principle flags (research boundedness, verification, grounding, size, tool references). Use to pre-review a playbook for churn/spin risk BEFORE running it. The data returned is for you to render RISK-FLAGS — flags are risks, not assertions. Pass a playbook name, or omit for all playbooks.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"playbook": map[string]any{"type": "string", "description": "Playbook to audit; omit for all playbooks."},
			},
		},
		Fn: func(args map[string]any) string {
			name, _ := args["playbook"].(string)
			return auditPlaybookContracts(home, name)
		},
	}
}

func auditPlaybookContracts(home, name string) string {
	names := []string{name}
	if name == "" {
		names = ListPlaybooks(home)
	}
	var b strings.Builder
	for _, n := range names {
		if n == "" {
			continue
		}
		pb, err := loadPlaybookWorkspace(home, n)
		if err != nil || pb == nil {
			continue
		}
		fmt.Fprintf(&b, "## Playbook: %s\n", n)
		for _, st := range pb.Stages {
			text := st.Context
			bounded := commitBoundaryRe.MatchString(text)
			fmt.Fprintf(&b, "### Stage %02d-%s\n", st.Number, st.Name)
			fmt.Fprintf(&b, "- size: %d chars\n", len(text))
			fmt.Fprintf(&b, "- research_bounded: %v%s\n", bounded, riskNote(!bounded, "no explicit commit/boundary instruction → research churn risk"))
			fmt.Fprintf(&b, "- verification: %v%s\n", verifyRe.MatchString(text), riskNote(!verifyRe.MatchString(text), "no verify step → unverified-completion risk"))
			fmt.Fprintf(&b, "- grounding: %v%s\n", groundingRe.MatchString(text), riskNote(!groundingRe.MatchString(text), "no grounding/fabrication guard → fabrication risk"))
			fmt.Fprintf(&b, "- tools referenced: %v\n", uniqueMatches(toolRefRe, text))
			if len(text) > 1000 {
				text = text[:1000]
			}
			fmt.Fprintf(&b, "- contract excerpt: %q\n", text)
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return "audit_playbook: no playbooks found."
	}
	return strings.TrimSpace(b.String())
}

func riskNote(risky bool, note string) string {
	if risky {
		return " ⚠ RISK: " + note
	}
	return ""
}

// needsPlaybookAudit (CTX-018): the design-time gate. Audit ONLY when there is
// reason to suspect a problem — new playbook (no history), the last run failed,
// or a stage contract changed since the last run. A stable playbook (unchanged
// contract, recent success) skips the audit so its runs cost nothing extra.
func needsPlaybookAudit(pb *PlaybookWorkspace) bool {
	last, err := latestPlaybookRun(pb)
	if err != nil || last == nil {
		return true // new playbook, no history
	}
	if last.Status == "failed" {
		return true // recent failure → audit before retry
	}
	for _, st := range pb.Stages {
		info, err := os.Stat(filepath.Join(st.Dir, "CONTEXT.md"))
		if err != nil {
			continue
		}
		if info.ModTime().After(last.UpdatedAt) {
			return true // contract changed since the last run
		}
	}
	return false
}

// stageRiskFlags returns a compact design-time audit block for one stage, or ""
// when the stage carries no agentic-principle risk flags. Injected into the
// stage prompt before execution so the LLM sees the risk and can avoid it.
func stageRiskFlags(stage WorkspaceStage) string {
	text := stage.Context
	var flags []string
	if !commitBoundaryRe.MatchString(text) {
		flags = append(flags, "research_unbounded (no commit/boundary instruction → churn risk)")
	}
	if !verifyRe.MatchString(text) {
		flags = append(flags, "no_verify_step (unverified-completion risk)")
	}
	if !groundingRe.MatchString(text) {
		flags = append(flags, "no_grounding_guard (fabrication risk)")
	}
	if len(flags) == 0 {
		return ""
	}
	return "⚠ DESIGN-TIME AUDIT (stage " + stage.Name + "): " + strings.Join(flags, "; ")
}

func uniqueMatches(re *regexp.Regexp, s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllString(s, -1) {
		m = strings.ToLower(m)
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}
