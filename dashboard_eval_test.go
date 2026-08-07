package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDashboardToolSummaryCompactsOutput(t *testing.T) {
	for _, tc := range []struct {
		name, output, want string
	}{
		{"sentence", "Scheduled the reminder. [action_receipt {\"proof\":true}]", "Scheduled the reminder."},
		{"long", "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz", "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklm..."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dashboardToolSummary(tc.output); got != tc.want {
				t.Fatalf("dashboardToolSummary() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEvalReportRequiresReleaseEvidence(t *testing.T) {
	home := t.TempDir()
	if got := evalReport(home); got != nil {
		t.Fatalf("missing report = %#v, want nil", got)
	}
	if err := os.WriteFile(filepath.Join(home, "eval_report.json"), []byte(`{"deterministic":"pass"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := evalReport(home); got != nil {
		t.Fatalf("incomplete report = %#v, want nil", got)
	}
	if err := os.WriteFile(filepath.Join(home, "eval_report.json"), []byte(`{"deterministic":"pass","judge":"live certification"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := evalReport(home); got["judge"] != "live certification" {
		t.Fatalf("report judge = %#v", got["judge"])
	}
}

func TestDashboardSessionMenuStacksAboveDock(t *testing.T) {
	css, err := staticFiles.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(css)
	if !strings.Contains(styles, ".sessmenu{position:fixed;z-index:50") {
		t.Fatal("session history menu must use viewport positioning above the conversation dock")
	}
}

func TestDashboardViewsSurviveUnchangedPollingRefresh(t *testing.T) {
	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	app := string(script)
	if !strings.Contains(app, `nextMarkup !== renderedMarkup`) {
		t.Fatal("polling must not replace unchanged dashboard markup")
	}
	if !strings.Contains(app, `activateView(view, sub)`) {
		t.Fatal("view-specific loaders must run only after markup is installed")
	}
	if !strings.Contains(app, `[...(d.skills || [])].sort`) {
		t.Fatal("skills must render in a deterministic order across polls")
	}
	if !strings.Contains(app, `view === "memory" && sub === "graph" && activeView === "memory" && activeSub === "graph"`) {
		t.Fatal("polling must preserve the active Memory Graph canvas")
	}
	if !strings.Contains(app, `canvas.isConnected && graphState?.canvas === canvas`) {
		t.Fatal("obsolete graph animation loops must stop when their canvas is replaced")
	}
	if !strings.Contains(app, "const layoutRadius = Math.max(160, Math.min(canvas.width, canvas.height) * 0.38)") {
		t.Fatal("memory graph nodes must start in a spread-out deterministic layout")
	}
	if !strings.Contains(app, "const maxNodeSpeed = 8") {
		t.Fatal("memory graph simulation must cap node velocity")
	}
}
