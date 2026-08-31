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

func TestDashboardSessionMenuCapturesAsyncAnchor(t *testing.T) {
	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	app := string(script)
	if !strings.Contains(app, "const anchor = ev.currentTarget;") {
		t.Fatal("session history menu must capture its event anchor before awaiting data")
	}
	if !strings.Contains(app, "anchor.getBoundingClientRect()") {
		t.Fatal("session history menu must position from the captured anchor")
	}
}

func TestDashboardNavigationAndLegacyHashContract(t *testing.T) {
	index, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ name, want string }{
		{"now", `href="#today" data-v="today"`},
		{"work", `href="#work" data-v="work"`},
		{"memory", `href="#memory/overview" data-v="memory"`},
		{"routines", `href="#system/schedules" data-v="system"`},
		{"system", `href="#system/overview" data-v="system"`},
	} {
		if !strings.Contains(string(index), route.want) {
			t.Errorf("primary navigation must expose the canonical %q surface", route.name)
		}
	}

	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), `if(raw==="today") route=["universe","now"]`) ||
		strings.Contains(string(script), `else if(raw==="work") route=["universe","work"]`) {
		t.Fatal("canonical Today and Work routes must not redirect back into a removed Galaxy lens")
	}
	for _, redirect := range []string{
		`if(raw==="overview") route=["work",null]`,
		`else if(raw==="gateway"||raw==="chat") route=["conversations",null]`,
		`else if(raw==="loop") route=["system","traces"]`,
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

func TestDashboardConversationWorkbenchContract(t *testing.T) {
	index, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, control := range []string{
		`id="workbench-resizer" role="separator" tabindex="0"`,
		`id="workbench-maximize"`,
		`id="dock-reopen"`,
		`aria-label="Open conversation workbench"`,
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
		`function workbenchMinimizeOnClose(wasOpen,open)`,
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
		`body.workbench-minimized #dock`,
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
const names=["composerHeight","shouldSubmitComposer","chatStatusFailed","shouldStickChat","workbenchHeightForKey","workbenchFocusTarget","workbenchMinimizeOnClose"];
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
equal(box.workbenchMinimizeOnClose(true,false),true,"close open workbench");
equal(box.workbenchMinimizeOnClose(false,false),false,"close collapsed workbench");
equal(box.workbenchMinimizeOnClose(true,true),false,"open workbench");
`
	cmd := exec.Command(node, "-e", harness, filepath.Join("static", "app.js"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dashboard workbench behavior failed: %v\n%s", err, out)
	}
}

func TestDashboardPlaybookRunsCappedWithExpander(t *testing.T) {
	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, behavior := range []string{
		`function playbookRunRow(pb,r)`,
		`function splitPlaybookRuns(runs)`,
		`splitPlaybookRuns(pb.runs||[])`,
		`playbook-runs-more`,
		"older run",
	} {
		if !strings.Contains(string(script), behavior) {
			t.Errorf("playbook runs cap is missing %q", behavior)
		}
	}

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
const box={}; vm.runInNewContext(extract("splitPlaybookRuns"),box);
function equal(got,want,label){if(got!==want) throw new Error(label+": got "+got+", want "+want)}
const many=Array.from({length:83},(_,i)=>({id:"r"+(83-i)}));
let v=box.splitPlaybookRuns(many);
equal(v.top.length,10,"inline cap");
equal(v.rest.length,73,"expander remainder");
equal(v.top[0].id,"r83","newest first");
equal(v.rest[72].id,"r1","oldest last");
v=box.splitPlaybookRuns(many.slice(0,10));
equal(v.top.length,10,"exactly ten stays inline");
equal(v.rest.length,0,"no expander at ten");
v=box.splitPlaybookRuns([]);
equal(v.top.length,0,"empty runs");
`
	cmd := exec.Command(node, "-e", harness, filepath.Join("static", "app.js"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("playbook runs cap behavior failed: %v\n%s", err, out)
	}
}
