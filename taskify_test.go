package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// taskifyTestCore builds the minimal Core a taskified run needs (same shape
// as playbook_test.go's fixtures).
func taskifyTestCore(t *testing.T, home string) *Core {
	t.Helper()
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	return &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}
}

// taskifyScaffold creates the five-state scaffold via the real tool boundary.
func taskifyScaffold(t *testing.T, core *Core, name, request string) {
	t.Helper()
	reply := makeTaskifyTool(core).Fn(map[string]any{"name": name, "request": request})
	if strings.HasPrefix(reply, "Error") {
		t.Fatalf("taskify failed: %s", reply)
	}
}

// taskifyWriteStub returns a stage-loop seam stub that writes the next pending
// stage's declared output and completes; capName != "" makes that stage return
// iteration_limit (with a partial file written to its output dir, the on-disk
// checkpoint). Every call is captured into prompts (stage prompts) when given.
func taskifyWriteStub(t *testing.T, home, playbook string, prompts *[]string, capName string, capIterations int) func(context.Context, LLMClient, string, string, []Message, *Registry, int, int, Observer, string) *LoopResult {
	t.Helper()
	return func(_ context.Context, _ LLMClient, _ string, _ string, msgs []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		if prompts != nil {
			*prompts = append(*prompts, msgs[len(msgs)-1].Content)
		}
		pb, err := loadPlaybookWorkspace(home, playbook)
		if err != nil {
			t.Fatal(err)
		}
		run, err := latestPlaybookRun(pb)
		if err != nil || run == nil {
			t.Fatalf("no run: %v", err)
		}
		st := nextPlaybookStage(run)
		if st == nil {
			t.Fatal("no pending stage")
		}
		stage, ok := workspaceStageFor(pb, st.Number, st.Name)
		if !ok {
			t.Fatalf("missing workspace stage %d-%s", st.Number, st.Name)
		}
		out := playbookRunOutputPath(pb, run, stage, stage.Outputs[0])
		if capName != "" && stage.Name == capName {
			// Partial state on disk — the checkpoint the continuation reads.
			partial := filepath.Join(filepath.Dir(out), "partial.md")
			if err := os.MkdirAll(filepath.Dir(partial), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(partial, []byte("PARTIAL-STATE"), 0600); err != nil {
				t.Fatal(err)
			}
			return &LoopResult{Status: "iteration_limit", Reply: "stopped at budget", Iterations: capIterations}
		}
		if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, []byte("content from "+stage.Name), 0600); err != nil {
			t.Fatal(err)
		}
		return &LoopResult{Status: "complete", Reply: "done", ToolCalls: []ToolCall{{Name: "write_file", Args: map[string]any{"path": out}}}}
	}
}

// --- Part 1: detection ---

func TestTaskIntentOffer(t *testing.T) {
	for _, msg := range []string{
		"build me a portfolio site",
		"redesign my portfolio",
		"fix this broken script",
		"create a dashboard",
		"make a report",
		"can you build the login page",
		"coding task: rewrite the billing module",
		"run this as a task: clean up the repo",
		"REDESIGN THE PORTFOLIO",
	} {
		if taskIntentOffer(msg) == "" {
			t.Fatalf("%q must offer", msg)
		}
	}
	for _, msg := range []string{
		"what time is it",
		"hello",
		"thanks!",
		"make sure the server is up",
		"can you make sure the backup ran",
		"ok",
	} {
		if taskIntentOffer(msg) != "" {
			t.Fatalf("%q must not offer", msg)
		}
	}
	// the offer is a discussion opener, never a launch
	if offer := taskIntentOffer("build me X"); !strings.Contains(offer, "offer, not a start") || !strings.Contains(offer, "do NOT create a playbook") {
		t.Fatalf("offer must be a discussion opener: %q", offer)
	}
}

// Acceptance: a task-intent message produces the offer ONLY — no playbook is
// created until the owner approves; trivial requests never offer.
func TestTaskifyOfferOnlyUntilApproved(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		w.Write([]byte(`{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	defer srv.Close()
	t.Setenv("MINO_TEST_KEY", "k")
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "providers.json"), []byte(`{"providers":[{"name":"t","priority":1,"base_url":"`+srv.URL+`","api_key_env":"MINO_TEST_KEY","model":"test-model"}]}`), 0600)
	pm, err := NewProviderManager(home, &Settings{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	settings := &Settings{Home: home, ContextChars: 20000, MaxTokens: 100, MaxIter: 5, Timezone: "Asia/Kuala_Lumpur"}
	core := &Core{Settings: settings, Client: pm, Sessions: NewSessionManager(settings, nil), Tools: NewRegistry()}

	lastUser := func() string {
		t.Helper()
		if len(bodies) == 0 {
			t.Fatal("no provider request captured")
		}
		var p struct {
			Messages []struct {
				Role    string
				Content string
			}
		}
		if err := json.Unmarshal([]byte(bodies[len(bodies)-1]), &p); err != nil {
			t.Fatalf("parse request: %v", err)
		}
		return p.Messages[len(p.Messages)-1].Content
	}

	core.RespondForContext(context.Background(), "s", "redesign my portfolio", "test", nil, false)
	if !strings.Contains(lastUser(), "want me to run it as a structured task?") {
		t.Fatalf("task-intent turn must carry the offer")
	}
	// offer only: no playbook may exist yet
	if _, err := os.Stat(filepath.Join(home, "playbooks")); !os.IsNotExist(err) {
		t.Fatal("the offer must not scaffold a playbook")
	}

	core.RespondForContext(context.Background(), "s", "what time is it", "test", nil, false)
	if strings.Contains(lastUser(), "structured task") {
		t.Fatalf("trivial request must never offer")
	}

	core.RespondForContext(context.Background(), "s", "yes run it", "test", nil, false)
	if strings.Contains(lastUser(), "structured task") {
		t.Fatalf("approval-turn wording must not re-offer")
	}
	if _, err := os.Stat(filepath.Join(home, "playbooks")); !os.IsNotExist(err) {
		t.Fatal("approval alone must not scaffold — the model scaffolds via taskify after the owner approves the offer")
	}
}

// --- Part 2: the scaffold ---

func TestBuildTaskifyStages(t *testing.T) {
	stages := buildTaskifyStages("redesign my portfolio", "")
	if len(stages) != 5 {
		t.Fatalf("scaffold must have 5 stages, got %d", len(stages))
	}
	want := []string{"00-plan", "01-decompose", "02-act", "03-observe", "04-repeat"}
	for i, w := range want {
		if stages[i].name != w {
			t.Fatalf("stage %d = %q, want %q", i, stages[i].name, w)
		}
	}
	// every scaffold contract is bounded (#238 integration: the same check
	// taskify runs at scaffold time)
	for _, s := range stages {
		if !researchBounded(s.context) {
			t.Fatalf("stage %s contract must be bounded", s.name)
		}
	}
	// the task text reaches the run through the plan contract
	if !strings.Contains(stages[0].context, "redesign my portfolio") {
		t.Fatalf("00-plan must embed the task text")
	}
	// stages declare their inputs from the prior stage's outputs
	if !strings.Contains(stages[1].context, "../00-plan/output/plan.md") {
		t.Fatalf("01-decompose must read 00-plan's output")
	}
	if !strings.Contains(stages[2].context, "../01-decompose/output/decomposition.md") {
		t.Fatalf("02-act must read the decomposition")
	}
	// the loop shape is documented in the scaffold: repeat closes the loop
	if !strings.Contains(stages[4].context, "split_stage") {
		t.Fatalf("04-repeat must propose the checkpoint split for gaps")
	}
	// act contract override
	custom := "## Process\nDo the thing once.\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| X | `output/x.md` | Markdown |\n"
	withOverride := buildTaskifyStages("t", custom)
	if !strings.Contains(withOverride[2].context, "Do the thing once.") {
		t.Fatalf("act_contract override must replace the 02-act contract")
	}
}

// #238 integration: unbounded stage contracts are flagged at scaffold time,
// not after 50 iterations.
func TestTaskifyRefusesUnboundedActContract(t *testing.T) {
	home := t.TempDir()
	core := taskifyTestCore(t, home)
	reply := makeTaskifyTool(core).Fn(map[string]any{
		"name":         "task-x",
		"request":      "audit the posts",
		"act_contract": "## Process\nRead every published post and score it.\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Report | `output/report.md` | Markdown |\n",
	})
	if !strings.Contains(reply, "unbounded") {
		t.Fatalf("unbounded contract must be refused at scaffold time, got: %s", reply)
	}
	if _, err := os.Stat(filepath.Join(home, "playbooks", "task-x")); !os.IsNotExist(err) {
		t.Fatal("refused scaffold must not leave a playbook behind")
	}
}

func TestTaskifyScaffoldShapeAndGateConfig(t *testing.T) {
	home := t.TempDir()
	core := taskifyTestCore(t, home)
	taskifyScaffold(t, core, "task-x", "redesign my portfolio")
	pb, err := loadPlaybookWorkspace(home, "task-x")
	if err != nil {
		t.Fatal(err)
	}
	// same engine: ordinary playbook layout, approval gate in the config
	if pb.Config["approval_gate"] != "01-decompose" {
		t.Fatalf("config must carry the approval gate, got %q", pb.Config["approval_gate"])
	}
	if !taskifiedPlaybook(pb) {
		t.Fatal("scaffold must be recognized as taskified")
	}
	if len(pb.Stages) != 5 || pb.Stages[0].Name != "plan" || pb.Stages[0].Number != 0 {
		t.Fatalf("scaffold stages wrong: %+v", pb.Stages)
	}
}

// --- Part 3: the gate ---

func TestGatePauseReply(t *testing.T) {
	home := t.TempDir()
	pb := &PlaybookWorkspace{Name: "t", Dir: filepath.Join(home, "playbooks", "t")}
	run := &PlaybookRun{ID: "r1"}
	gate := WorkspaceStage{Number: 1, Name: "decompose"}
	text := gatePauseReply(pb, run, gate)
	for _, want := range []string{"r1", "01-decompose", "approval", "resumes only after the owner explicitly approves"} {
		if !strings.Contains(text, want) {
			t.Fatalf("gatePauseReply missing %q: %s", want, text)
		}
	}
}

// Acceptance: the run pauses at 01-decompose — nothing executes before the
// owner approves; approval (harness-owned) resumes the run at 02-act.
func TestTaskifyRunGatePausesAwaitingApproval(t *testing.T) {
	home := t.TempDir()
	core := taskifyTestCore(t, home)
	taskifyScaffold(t, core, "task-x", "redesign my portfolio")
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	runPlaybookStageLoop = taskifyWriteStub(t, home, "task-x", nil, "", 0)

	// first run: plan + decompose execute, then the gate pauses before act
	result, err := RunPlaybook(context.Background(), core, "task-x", "yes run it", "sess", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "awaiting_approval" || !strings.Contains(result.Reply, "approval gate") {
		t.Fatalf("run must pause at the gate: %+v", result)
	}
	pb, _ := loadPlaybookWorkspace(home, "task-x")
	run, err := latestPlaybookRun(pb)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "awaiting_approval" || run.GateApproved {
		t.Fatalf("run state wrong: status=%s gate_approved=%v", run.Status, run.GateApproved)
	}
	if run.Stages[0].Status != "complete" || run.Stages[1].Status != "complete" || run.Stages[2].Status != "pending" {
		t.Fatalf("stages wrong: %+v", run.Stages)
	}
	// a re-run without approval pauses again, not through the gate
	result, err = RunPlaybook(context.Background(), core, "task-x", "yes run it", "sess", nil)
	if err != nil || result.Status != "awaiting_approval" {
		t.Fatalf("unapproved re-run must pause again: %+v err=%v", result, err)
	}

	// owner approves (harness-owned; the model can never approve its own run)
	routing := approvePendingTaskGate(home, "sess", "approved")
	if !strings.Contains(routing, "run_playbook") {
		t.Fatalf("approval must route to resume: %q", routing)
	}
	run, _ = latestPlaybookRun(pb)
	if !run.GateApproved || run.Status != "running" {
		t.Fatalf("approval must mark the run approved: %+v", run)
	}

	// resume: act → observe → repeat
	result, err = RunPlaybook(context.Background(), core, "task-x", "yes run it", "sess", nil)
	if err != nil || result.Status != "complete" {
		t.Fatalf("approved resume must complete: %+v err=%v", result, err)
	}
	run, _ = latestPlaybookRun(pb)
	if run.Status != "complete" || result.StagesRun != 3 {
		t.Fatalf("resume must run exactly act/observe/repeat: %+v stagesRun=%d", run, result.StagesRun)
	}
	for i, st := range run.Stages {
		if st.Status != "complete" {
			t.Fatalf("stage %d not complete: %+v", i, st)
		}
	}
}

func TestApprovePendingTaskGate(t *testing.T) {
	home := t.TempDir()
	core := taskifyTestCore(t, home)
	taskifyScaffold(t, core, "task-x", "redesign my portfolio")
	// nothing pending yet
	if got := approvePendingTaskGate(home, "sess", "approved"); got != "" {
		t.Fatalf("no pending run must approve nothing, got %q", got)
	}
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	runPlaybookStageLoop = taskifyWriteStub(t, home, "task-x", nil, "", 0)
	if _, err := RunPlaybook(context.Background(), core, "task-x", "yes run it", "sess", nil); err != nil {
		t.Fatal(err)
	}
	// negative or unrelated messages never approve
	for _, msg := range []string{"no", "not yet", "change the plan", "looks bad", "what is this"} {
		if got := approvePendingTaskGate(home, "sess", msg); got != "" {
			t.Fatalf("%q must not approve, got %q", msg, got)
		}
	}
	// a different session must not approve this run
	if got := approvePendingTaskGate(home, "other-session", "yes"); got != "" {
		t.Fatalf("other session must not approve, got %q", got)
	}
	// affirmative (no commas — a qualified reply is not an approval)
	got := approvePendingTaskGate(home, "sess", "yes run it")
	if !strings.Contains(got, "run_playbook") || !strings.Contains(got, "task-x") {
		t.Fatalf("approval routing wrong: %q", got)
	}
	pb, _ := loadPlaybookWorkspace(home, "task-x")
	run, _ := latestPlaybookRun(pb)
	if !run.GateApproved || run.Status != "running" {
		t.Fatalf("run must be approved and running: %+v", run)
	}
	// already approved: nothing left to approve
	if got := approvePendingTaskGate(home, "sess", "yes"); got != "" {
		t.Fatalf("second approval must be a no-op, got %q", got)
	}
}

// --- Part 3: checkpoint split + cap-resume (#236 absorption) ---

func TestSplitOfferText(t *testing.T) {
	home := t.TempDir()
	pb := &PlaybookWorkspace{Name: "t", Dir: filepath.Join(home, "playbooks", "t")}
	run := &PlaybookRun{ID: "r1"}
	stage := WorkspaceStage{Number: 2, Name: "act"}
	text := splitOfferText(pb, run, stage, 40)
	for _, want := range []string{"02-act", "40 of 50", "split_stage", "accept", "decline", "02-act-b", "never from zero"} {
		if !strings.Contains(text, want) {
			t.Fatalf("splitOfferText missing %q: %s", want, text)
		}
	}
}

// Acceptance: an over-scoped stage triggers the split offer; a re-run resumes
// at the failed stage (never from zero); accepting creates the checkpoint and
// the continuation stage 02-act-b, and the resumed run continues mid-task.
func TestTaskifySplitOfferAndCheckpointResume(t *testing.T) {
	home := t.TempDir()
	core := taskifyTestCore(t, home)
	taskifyScaffold(t, core, "task-x", "build the widget")
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()

	// stage 02-act burns its budget (>80%) without declared outputs
	runPlaybookStageLoop = taskifyWriteStub(t, home, "task-x", nil, "", 0)
	if _, err := RunPlaybook(context.Background(), core, "task-x", "yes run it", "sess", nil); err != nil {
		t.Fatal(err)
	}
	approvePendingTaskGate(home, "sess", "approved")
	runPlaybookStageLoop = taskifyWriteStub(t, home, "task-x", nil, "act", 40)
	result, err := RunPlaybook(context.Background(), core, "task-x", "yes run it", "sess", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "awaiting_split" || !strings.Contains(result.Reply, "checkpoint split") {
		t.Fatalf("capped stage must offer the split: %+v", result)
	}
	pb, _ := loadPlaybookWorkspace(home, "task-x")
	run, _ := latestPlaybookRun(pb)
	if run.Status != "awaiting_split" {
		t.Fatalf("run must be awaiting_split: %+v", run)
	}
	if run.Stages[0].Status != "complete" || run.Stages[1].Status != "complete" || run.Stages[2].Status != "failed" {
		t.Fatalf("stages wrong after cap: %+v", run.Stages)
	}

	// re-run WITHOUT deciding: resumes at the failed stage, not from zero
	result, err = RunPlaybook(context.Background(), core, "task-x", "continue", "sess", nil)
	if err != nil || result.Status != "awaiting_split" {
		t.Fatalf("re-run must re-offer the split: %+v err=%v", result, err)
	}
	run, _ = latestPlaybookRun(pb)
	if run.Stages[0].Attempts != 1 || run.Stages[1].Attempts != 1 || run.Stages[2].Attempts != 2 {
		t.Fatalf("cap-hit resume must not re-run completed stages: %+v", run.Stages)
	}

	// accept: checkpoint + continuation stage 02-act-b
	contract := "## Process\nContinue the task from the partial state declared below, finish the remaining decomposed steps, and write `output/completion.md` exactly once.\n## Inputs\n\n| Source | File/Location | Section/Scope | Why |\n| --- | --- | --- | --- |\n| Partial state | `../02-act/output/partial.md` | Full file | The checkpoint to continue from |\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Completion report | `output/completion.md` | Markdown |\n"
	reply := makeSplitStageTool(core).Fn(map[string]any{"playbook": "task-x", "action": "accept", "name": "act-b", "contract": contract})
	if !strings.Contains(reply, "02-act-b") {
		t.Fatalf("accept must create the continuation stage: %s", reply)
	}
	run, _ = latestPlaybookRun(pb)
	if run.Status != "running" {
		t.Fatalf("accepted run must reopen as running: %+v", run)
	}
	if run.Stages[2].Status != "complete" || !strings.Contains(run.Stages[2].Error, "checkpoint") {
		t.Fatalf("split stage must be checkpoint-complete: %+v", run.Stages[2])
	}
	if run.Stages[3].Name != "act-b" || run.Stages[3].Status != "pending" {
		t.Fatalf("continuation must follow the checkpoint stage: %+v", run.Stages)
	}
	if _, err := os.Stat(filepath.Join(home, "playbooks", "task-x", "stages", "02-act-b", "CONTEXT.md")); err != nil {
		t.Fatalf("continuation contract must be written: %v", err)
	}

	// resume: continues at 02-act-b (mid-task), then observe + repeat
	runPlaybookStageLoop = taskifyWriteStub(t, home, "task-x", nil, "", 0)
	result, err = RunPlaybook(context.Background(), core, "task-x", "continue", "sess", nil)
	if err != nil || result.Status != "complete" {
		t.Fatalf("resumed run must complete: %+v err=%v", result, err)
	}
	run, _ = latestPlaybookRun(pb)
	if run.Status != "complete" || result.StagesRun != 3 {
		t.Fatalf("resume must run act-b/observe/repeat only: %+v stagesRun=%d", run, result.StagesRun)
	}
	for i, st := range run.Stages {
		if st.Status != "complete" {
			t.Fatalf("stage %d not complete: %+v", i, st)
		}
	}
	if run.Stages[3].Attempts != 1 {
		t.Fatalf("continuation must run exactly once: %+v", run.Stages[3])
	}
	// the continuation contract's declared input resolves to the checkpoint's
	// partial state (proving the checkpoint, not a restart)
	pb, _ = loadPlaybookWorkspace(home, "task-x")
	contStage, _ := workspaceStageFor(pb, 2, "act-b")
	if len(contStage.Inputs) == 0 || !strings.Contains(contStage.Inputs[0].Path, "../02-act/output/partial.md") {
		t.Fatalf("continuation must declare the checkpoint partial as input: %+v", contStage.Inputs)
	}
}

func TestTaskifySplitDeclineFailsRun(t *testing.T) {
	home := t.TempDir()
	core := taskifyTestCore(t, home)
	taskifyScaffold(t, core, "task-x", "build the widget")
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	runPlaybookStageLoop = taskifyWriteStub(t, home, "task-x", nil, "", 0)
	if _, err := RunPlaybook(context.Background(), core, "task-x", "yes run it", "sess", nil); err != nil {
		t.Fatal(err)
	}
	approvePendingTaskGate(home, "sess", "approved")
	runPlaybookStageLoop = taskifyWriteStub(t, home, "task-x", nil, "act", 40)
	if _, err := RunPlaybook(context.Background(), core, "task-x", "yes run it", "sess", nil); err != nil {
		t.Fatal(err)
	}
	reply := makeSplitStageTool(core).Fn(map[string]any{"playbook": "task-x", "action": "decline"})
	if !strings.Contains(reply, "declined") {
		t.Fatalf("decline reply wrong: %s", reply)
	}
	pb, _ := loadPlaybookWorkspace(home, "task-x")
	run, _ := latestPlaybookRun(pb)
	if run.Status != "failed" || run.Stages[2].Status != "failed" {
		t.Fatalf("declined run must be failed: %+v", run)
	}
	// a non-awaiting run cannot be declined or accepted
	if got := makeSplitStageTool(core).Fn(map[string]any{"playbook": "task-x", "action": "decline"}); !strings.Contains(got, "Error") {
		t.Fatalf("decline on a failed run must error, got %q", got)
	}
}

// Acceptance: a stage's context is its contract + prior stages' declared
// outputs — never the raw session history.
func TestTaskifyStageContextIsolated(t *testing.T) {
	home := t.TempDir()
	core := taskifyTestCore(t, home)
	taskifyScaffold(t, core, "task-x", "redesign the portfolio")
	// unrelated session history (the context tax this ticket kills)
	sess := core.Sessions.Get("sess").Session
	sess.AddExchange("logo colors debate", "dark theme looks good", "sure", nil, "test")
	sess.AddExchange("vision descriptions", "2k chars of vision text about the header", "ok", nil, "test")

	var prompts []string
	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	runPlaybookStageLoop = taskifyWriteStub(t, home, "task-x", &prompts, "", 0)
	if _, err := RunPlaybook(context.Background(), core, "task-x", "yes run it", "sess", nil); err != nil {
		t.Fatal(err)
	}
	approvePendingTaskGate(home, "sess", "approved")
	prompts = nil
	if _, err := RunPlaybook(context.Background(), core, "task-x", "yes run it", "sess", nil); err != nil {
		t.Fatal(err)
	}

	var actPrompt string
	for _, p := range prompts {
		// the stage header is "stage 02-act." — never the continuation
		// mention "stage 02-act-b" in the repeat contract
		if strings.Contains(p, "stage 02-act.") {
			actPrompt = p
		}
	}
	if actPrompt == "" {
		t.Fatalf("no 02-act stage prompt captured: %d prompts", len(prompts))
	}
	// stage 2's context = contract + stage 1's declared outputs (the plan and
	// the decomposition), written by the earlier stages' own runs
	for _, want := range []string{"content from plan", "content from decompose", "Completion report"} {
		if !strings.Contains(actPrompt, want) {
			t.Fatalf("stage 2 prompt must contain %q", want)
		}
	}
	// and nothing from the unrelated session history
	for _, banned := range []string{"logo colors debate", "vision descriptions", "dark theme looks good", "2k chars of vision"} {
		if strings.Contains(actPrompt, banned) {
			t.Fatalf("stage 2 prompt must not contain session history (%q)", banned)
		}
	}
}

// #329: the offer turn is mechanically fenced. Regression for 2026-08-22 —
// a "Coding task:" turn executed a multi-phase redesign with no approval and
// no scaffold, because the offer was prompt-only discipline.

func TestTaskifyOfferTurnFencesWorkTools(t *testing.T) {
	ctx := context.WithValue(context.Background(), taskifyOfferKey{}, true)
	for _, name := range []string{"bash", "write_file", "edit_file", "sync_file", "taskify", "run_playbook"} {
		if msg := taskifyOfferToolGuard(ctx, name); msg == "" {
			t.Fatalf("work tool %q must be fenced on an offer turn", name)
		}
	}
	// read/search/grounding tools stay available: the discussion is still real
	for _, name := range []string{"read_file", "search_web", "fetch_url", "remember", "note_session"} {
		if msg := taskifyOfferToolGuard(ctx, name); msg != "" {
			t.Fatalf("grounding tool %q must stay available on an offer turn (got %q)", name, msg)
		}
	}
	// no fence without the marker: normal turns execute everything
	plain := context.Background()
	if msg := taskifyOfferToolGuard(plain, "bash"); msg != "" {
		t.Fatalf("bash on a non-offer turn must not be fenced (got %q)", msg)
	}
}

func TestApplyTaskifyOffer(t *testing.T) {
	msg := "Coding task: redesign my portfolio"
	tail, fenced := applyTaskifyOffer("CLOCK", msg, false)
	if !fenced {
		t.Fatal("task-intent phrase must fence the turn")
	}
	if !strings.Contains(tail, taskifyOfferText) || !strings.Contains(tail, "CLOCK") {
		t.Fatal("offer tail must carry the offer text and keep the rest of the tail")
	}
	// non-task message: no offer, no fence
	tail2, fenced2 := applyTaskifyOffer("CLOCK", "what time is it?", false)
	if fenced2 || strings.Contains(tail2, taskifyOfferText) {
		t.Fatal("non-task message must not be offered or fenced")
	}
	// gate-approval turn: suppressed by design — approval is the start signal
	_, fenced3 := applyTaskifyOffer("CLOCK", msg, true)
	if fenced3 {
		t.Fatal("gate-approval turn must not be re-offered or fenced")
	}
}

// #335 (finding 2): after a fenced offer turn, the RUN-006d errors linger in
// history and the model believes work stays blocked — later UNFENCED turns
// refused to act even on explicit approval. The first unfenced turn after a
// fenced one carries a fence-lifted note expiring those errors.

func TestTaskifyFenceLiftedNote(t *testing.T) {
	note := taskifyFenceLiftedNote()
	for _, want := range []string{"fence lifted", "PREVIOUS turn only", "EXPIRED", "work tools are available", "call taskify now"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note missing %q: %s", want, note)
		}
	}
}

// The note fires exactly once: on the first unfenced turn after a fenced
// one. A re-fenced turn (the owner's next message carries task verbs again)
// gets the offer, not the note; the following unfenced turn gets the note.
func TestApplyFenceLiftedNote(t *testing.T) {
	tail := "CLOCK"
	// no previous fence → no note
	if got := applyFenceLiftedNote(tail, false, false); got != tail {
		t.Fatalf("unexpected note without a previous fence: %q", got)
	}
	// this turn is itself fenced → the offer + fence govern, no note
	if got := applyFenceLiftedNote(tail, true, true); got != tail {
		t.Fatalf("fenced turn must not carry the lifted note: %q", got)
	}
	// first unfenced turn after a fence → note appended
	got := applyFenceLiftedNote(tail, false, true)
	if !strings.Contains(got, taskifyFenceLiftedNote()) {
		t.Fatalf("fence-lifted note missing on the post-fence turn: %q", got)
	}
}

// Session-level: the flag records the fence state across turns (the app.go
// wiring: fenced turn, then unfenced turn, then another unfenced turn).
func TestSessionOfferFenceFlag(t *testing.T) {
	s := NewSession(&Settings{}, nil)
	if s.OfferFencedLastTurn() {
		t.Fatal("fresh session must not report a previous fence")
	}
	s.SetOfferFenced(true)
	if !s.OfferFencedLastTurn() {
		t.Fatal("fenced turn must be visible to the next turn")
	}
	s.SetOfferFenced(false)
	if s.OfferFencedLastTurn() {
		t.Fatal("unfenced turn must reset the flag for the following turn")
	}
}
