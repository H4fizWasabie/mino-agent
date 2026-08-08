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

func TestDashboardNavigationAndLegacyHashContract(t *testing.T) {
	index, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"today", "work", "conversations", "memory", "system"} {
		want := `href="#` + route + `" data-v="` + route + `"`
		if !strings.Contains(string(index), want) {
			t.Errorf("primary navigation must preserve %q", route)
		}
	}

	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, redirect := range []string{
		`if(raw==="overview") route=["today",null]`,
		`else if(raw==="gateway"||raw==="chat") route=["conversations",null]`,
		`else if(raw==="loop") route=["system","runtime"]`,
		`else if(raw==="tools") route=["system",sub==="results"?"tool-results":sub==="mcp"?"mcp":"tools"]`,
		`else if(raw==="database") route=["system",sub?` + "`database-${sub}`" + `:"database"]`,
		`else if(raw==="files") route=["system",sub?` + "`files-${sub}`" + `:"files"]`,
		`else if(raw==="ops") route=["system",sub&&sub!=="overview"?sub:"overview"]`,
		`else if(raw==="settings") route=["system","settings"]`,
		`else if(raw==="activetasks") route=["system","schedules"]`,
		`else if(raw==="graph") route=["memory","graph"]`,
	} {
		if !strings.Contains(string(script), redirect) {
			t.Errorf("dashboard must preserve legacy route redirect %q", redirect)
		}
	}
}

func TestDashboardNowfieldSurfaceContract(t *testing.T) {
	index, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "THESIS: Nowfield") {
		t.Fatal("dashboard direction contract must name the approved Nowfield surface")
	}
	for _, shell := range []string{
		`<div id="view"><div class="nowfield-loading"`,
	} {
		if !strings.Contains(string(index), shell) {
			t.Errorf("Nowfield shell is missing %q", shell)
		}
	}
	if strings.Count(string(index), `<a href="#memory" data-v="memory"`) < 2 {
		t.Error("Memory must remain reachable in both desktop and mobile navigation")
	}

	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, behavior := range []string{
		`function nowfieldView(d,mode)`,
		`function nowfieldAttr(value)`,
		`.replace(/"/g,"&quot;").replace(/'/g,"&#39;")`,
		`return nowfieldView(d,"today")`,
		`return nowfieldView(d,"work")`,
		`function filterNowfield()`,
		`aria-label="Search Work"`,
		`aria-label="Filter Work by status"`,
		`class="nowfield-axis"`,
		`<span>Past</span><strong>Now</strong><span>Next</span>`,
		`#responsibility/${encodeURIComponent(entry.id)}`,
		`entry.next_action`,
		`entry.due_at`,
		`entry.schedule`,
		`class="nowfield-empty"`,
		`class="nowfield-focus"`,
		`class="nowfield-focus-axis"`,
		`aria-label="Past:`,
		`aria-label="Now:`,
		`aria-label="Next:`,
		`class="nowfield-detail-link"`,
		`function renderRefreshError(error)`,
	} {
		if !strings.Contains(string(script), behavior) {
			t.Errorf("Nowfield browser contract is missing %q", behavior)
		}
	}

	styles, err := staticFiles.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{
		`.nowfield{`,
		`.nowfield-lane{`,
		`.nowfield-axis{`,
		`.nowfield-controls{`,
		`.nowfield-focus{`,
		`.nowfield-focus-axis{`,
		`.nowfield-detail-link{`,
		`.nowfield-loading{`,
		`height:calc(100vh - 204px)`,
		`body[data-view="today"] main,body[data-view="work"] main`,
		`body[data-view="responsibility"] main{width:100%`,
		`.nowfield-lanes{min-height:0`,
		`.nowfield-lane{grid-template-columns:1fr`,
	} {
		if !strings.Contains(string(styles), rule) {
			t.Errorf("Nowfield layout contract is missing %q", rule)
		}
	}
}
