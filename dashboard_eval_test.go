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
	if !strings.Contains(app, `view === "universe" && activeView === "universe" && activeSub === sub && universeState?.canvas?.isConnected`) {
		t.Fatal("polling must preserve the active Living Field canvas")
	}
	if !strings.Contains(app, `universeUpdate(nextUniverse)`) {
		t.Fatal("polling must merge durable universe changes without rebuilding the field")
	}
	field, err := staticFiles.ReadFile("static/universe.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, behavior := range []string{`function universeHash(text)`, `function universeLayout(nodes,edges=[])`, `if(!canvas.isConnected||universeState!==state) return`} {
		if !strings.Contains(string(field), behavior) {
			t.Errorf("Living Field refresh contract is missing %q", behavior)
		}
	}
}

func TestDashboardNavigationAndLegacyHashContract(t *testing.T) {
	index, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, lens := range []string{"universe", "now", "work", "memory", "routines", "system"} {
		hash := "#universe"
		if lens != "universe" {
			hash += "/" + lens
		}
		want := `href="` + hash + `" data-v="universe" data-lens="` + lens + `"`
		if !strings.Contains(string(index), want) {
			t.Errorf("primary navigation must expose the %q map lens", lens)
		}
	}

	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, redirect := range []string{
		`if(raw==="overview") route=["universe",null]`,
		`else if(raw==="today") route=["universe","now"]`,
		`else if(raw==="work") route=["universe","work"]`,
		`else if(raw==="gateway"||raw==="chat") route=["conversations",null]`,
		`else if(raw==="loop") route=["system","runtime"]`,
		`else if(raw==="tools") route=["system",sub==="results"?"tool-results":sub==="mcp"?"mcp":"tools"]`,
		`else if(raw==="database") route=["system",sub?` + "`database-${sub}`" + `:"database"]`,
		`else if(raw==="files") route=["system",sub?` + "`files-${sub}`" + `:"files"]`,
		`else if(raw==="ops") route=["system",sub&&sub!=="overview"?sub:"overview"]`,
		`else if(raw==="settings") route=["system","settings"]`,
		`else if(raw==="activetasks") route=["system","schedules"]`,
		`else if(raw==="graph"||(raw==="memory"&&sub==="graph")) route=["universe","memory"]`,
	} {
		if !strings.Contains(string(script), redirect) {
			t.Errorf("dashboard must preserve legacy route redirect %q", redirect)
		}
	}
}

func TestDashboardLivingFieldSurfaceContract(t *testing.T) {
	index, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "THESIS: Living Field") {
		t.Fatal("dashboard direction contract must name the approved Living Field surface")
	}
	for _, shell := range []string{
		`<div id="view"><div class="nowfield-loading"`,
		`<script src="/static/universe.js"></script>`,
	} {
		if !strings.Contains(string(index), shell) {
			t.Errorf("Living Field shell is missing %q", shell)
		}
	}
	if strings.Contains(string(index), `<svg class="arch`) {
		t.Error("the retired runtime SVG must not remain in the production shell")
	}

	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, behavior := range []string{
		`universe(d, lens){ return universeView(U,lens||"universe"); }`,
		`fetch("/api/universe")`,
		`if(view==="universe") setTimeout(()=>initUniverse(U,sub||"universe"),20)`,
		`(r.events||[]).forEach(universeActivity)`,
	} {
		if !strings.Contains(string(script), behavior) {
			t.Errorf("Living Field app contract is missing %q", behavior)
		}
	}
	field, err := staticFiles.ReadFile("static/universe.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, behavior := range []string{
		`id="universe-canvas"`, `snapshot.nodes`, `snapshot.edges`, `function playUniverseHistory()`,
		`state.timeline=Math.min(1`, `function universeActivity(event)`, `state.activities.push`,
		`requestAnimationFrame(draw)`, `function selectUniverseNode(id)`, `Open full view`,
	} {
		if !strings.Contains(string(field), behavior) {
			t.Errorf("Living Field behavior is missing %q", behavior)
		}
	}

	styles, err := staticFiles.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{
		`body[data-view="universe"]{grid-template-columns:176px minmax(0,1fr)`,
		`.living-field{`, `.field-stage{`, `#universe-canvas{`, `.field-inspector{`,
		`.field-timeline{`, `.field-region-labels`, `.field-a11y-index{`,
		`body[data-view="universe"] #dock{grid-column:1/-1;grid-row:2}`,
		`@media(max-width:719px){body[data-view="universe"]`,
	} {
		if !strings.Contains(string(styles), rule) {
			t.Errorf("Living Field layout contract is missing %q", rule)
		}
	}
}

func TestDashboardLivingFieldBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for Living Field behavior checks")
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
const names=["universeHash","universeRand","universeRegion","universeFocus","universeNodeLink","universeLayout"];
const box={};vm.runInNewContext(names.map(extract).join("\n"),box);
function assert(ok,label){if(!ok)throw new Error(label)}
assert(box.universeHash("memory:a")===box.universeHash("memory:a"),"stable hash");
assert(box.universeFocus({kind:"reminder",attention:false},"now"),"now lens includes reminders");
assert(box.universeNodeLink({id:"responsibility:routine:test",kind:"responsibility"})==="#responsibility/routine%3Atest","responsibility deep link");
const make=()=>Array.from({length:64},(_,i)=>({id:"memory:"+i,kind:"memory",community:0,state:"semantic"}));
const edges=Array.from({length:63},(_,i)=>({source:"memory:"+i,target:"memory:"+(i+1)}));
const first=make(),second=make();box.universeLayout(first,edges);box.universeLayout(second,edges);
assert(JSON.stringify(first.map(n=>[n.x,n.y]))===JSON.stringify(second.map(n=>[n.x,n.y])),"deterministic geography");
const xs=first.map(n=>n.x),ys=first.map(n=>n.y);
assert(Math.max(...xs)-Math.min(...xs)>.35&&Math.max(...ys)-Math.min(...ys)>.25,"single stored community must still form a topology field");
`
	cmd := exec.Command(node, "-e", harness, filepath.Join("static", "universe.js"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Living Field behavior failed: %v\n%s", err, out)
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
