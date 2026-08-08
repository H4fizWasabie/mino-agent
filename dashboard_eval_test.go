package main

import (
	"os"
	"os/exec"
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

func TestDashboardConversationWorkbenchContract(t *testing.T) {
	index, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, control := range []string{
		`id="workbench-resizer" role="separator" tabindex="0"`,
		`id="workbench-maximize"`,
		`aria-label="Maximize conversation workbench"`,
		`<textarea id="dmsg"`,
		`aria-keyshortcuts="Control+Enter Meta+Enter"`,
		`data-workbench-tab="evidence"`,
		`data-workbench-tab="actions"`,
		`data-workbench-tab="links"`,
	} {
		if !strings.Contains(string(index), control) {
			t.Errorf("conversation workbench markup is missing %q", control)
		}
	}
	if strings.Contains(string(index), `<input id="dmsg"`) {
		t.Error("conversation composer must not regress to a single-line input")
	}

	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, behavior := range []string{
		`function resizeComposer()`,
		`function setWorkbenchHeight(height)`,
		`function toggleWorkbenchMaximize()`,
		`function setWorkbenchTab(tab)`,
		`if(shouldSubmitComposer(e))`,
		`pending.request=text`,
		`function retryChat(text)`,
	} {
		if !strings.Contains(string(script), behavior) {
			t.Errorf("conversation workbench behavior is missing %q", behavior)
		}
	}

	styles, err := staticFiles.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{
		`--workbench-h:45vh`,
		`grid-template-columns:minmax(0,72fr) minmax(260px,28fr)`,
		`body.ask-open #dock{height:var(--workbench-h)`,
		`#dmsg{resize:none`,
		`max-height:180px`,
		`body.workbench-max #dock`,
		`body.ask-open #dock{inset:0;display:flex;width:100%;height:100dvh`,
	} {
		if !strings.Contains(string(styles), rule) {
			t.Errorf("conversation workbench layout is missing %q", rule)
		}
	}
}

func TestDashboardConversationWorkbenchBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for dashboard behavior checks")
	}
	const harness = `
const fs=require("fs"),vm=require("vm"),source=fs.readFileSync(process.argv[1],"utf8");
function extract(name){
  const start=source.indexOf("function "+name+"(");
  if(start<0) throw new Error("missing "+name);
  const brace=source.indexOf("{",start); let depth=0;
  for(let i=brace;i<source.length;i++){
    if(source[i]==="{") depth++;
    else if(source[i]==="}"&&--depth===0) return source.slice(start,i+1);
  }
  throw new Error("unterminated "+name);
}
const names=["composerHeight","shouldSubmitComposer","chatStatusFailed","shouldStickChat","workbenchHeightForKey","workbenchFocusTarget"];
const box={}; vm.runInNewContext(names.map(extract).join("\n"),box);
function equal(got,want,label){if(got!==want) throw new Error(label+": got "+got+", want "+want)}
equal(box.composerHeight(40,false),44,"collapsed composer");
equal(box.composerHeight(40,true),112,"open composer floor");
equal(box.composerHeight(400,true),180,"long composer cap");
equal(box.shouldSubmitComposer({key:"Enter",ctrlKey:false,metaKey:false,shiftKey:false}),false,"plain Enter");
equal(box.shouldSubmitComposer({key:"Enter",ctrlKey:false,metaKey:false,shiftKey:true}),false,"Shift Enter");
equal(box.shouldSubmitComposer({key:"Enter",ctrlKey:true,metaKey:false,shiftKey:false}),true,"Ctrl Enter");
equal(box.shouldSubmitComposer({key:"Enter",ctrlKey:false,metaKey:true,shiftKey:false}),true,"Meta Enter");
for(const status of ["error","loop","iteration_limit","cancelled"]) equal(box.chatStatusFailed(status),true,status+" failure");
equal(box.chatStatusFailed("complete"),false,"complete status");
equal(box.shouldStickChat(1000,200,300),false,"preserve earlier scroll");
equal(box.shouldStickChat(1000,680,300),true,"follow near bottom");
equal(box.workbenchHeightForKey(400,"ArrowUp",1000),424,"keyboard grow");
equal(box.workbenchHeightForKey(400,"ArrowDown",1000),376,"keyboard shrink");
equal(box.workbenchHeightForKey(400,"Home",1000),280,"keyboard minimum");
equal(box.workbenchHeightForKey(400,"End",1000),800,"keyboard maximum");
const opener={isConnected:true},fallback={isConnected:true};
equal(box.workbenchFocusTarget(opener,fallback),opener,"restore opener focus");
equal(box.workbenchFocusTarget({isConnected:false},fallback),fallback,"fallback focus");
`
	cmd := exec.Command(node, "-e", harness, filepath.Join("static", "app.js"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dashboard workbench behavior failed: %v\n%s", err, out)
	}
}
