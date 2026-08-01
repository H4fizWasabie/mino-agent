package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAppendSystemTimeUsesConfiguredLocation(t *testing.T) {
	zone := time.FixedZone("MYT", 8*60*60)
	got := appendSystemTime("system", time.Date(2026, 7, 26, 15, 0, 0, 0, time.UTC), zone)
	if !strings.Contains(got, "Today is 2026-07-26") || !strings.Contains(got, "MYT") {
		t.Fatalf("system time = %q", got)
	}
}

func TestLoadPlaybook(t *testing.T) {
	tmp := t.TempDir()
	playbooksDir := filepath.Join(tmp, "playbooks")

	// create a test playbook
	dir := filepath.Join(playbooksDir, "test-playbook")
	os.MkdirAll(filepath.Join(dir, "output"), 0700)

	os.WriteFile(filepath.Join(dir, "config.md"), []byte(`description: Test playbook for unit tests
schedule: every 24h
status: active
Threshold: 7
`), 0644)

	os.WriteFile(filepath.Join(dir, "01-fetch.md"), []byte(`# Fetch data

## Read

- `+"`config.md`"+` (for threshold)

## Do

1. Query the database for pending items
2. Filter by threshold from config
3. Write results to output

## Tools

- read_file
- write_file

## Write

`+"`output/01-data.md`"+`
`), 0644)

	os.WriteFile(filepath.Join(dir, "02-report.md"), []byte(`# Generate report

## Read

- `+"`output/01-data.md`"+` (the fetched data)

## Do

1. Read the data file
2. Generate a summary report
3. Stop here. Ask Abah to review.

## Write

`+"`output/02-report.md`"+`
`), 0644)

	pb, err := LoadPlaybook(playbooksDir, "test-playbook")
	if err != nil {
		t.Fatalf("LoadPlaybook: %v", err)
	}

	if pb.Name != "test-playbook" {
		t.Errorf("name = %q, want %q", pb.Name, "test-playbook")
	}
	if pb.Description != "Test playbook for unit tests" {
		t.Errorf("description = %q", pb.Description)
	}
	if pb.Schedule != "every 24h" {
		t.Errorf("schedule = %q", pb.Schedule)
	}
	if pb.Status != "active" {
		t.Errorf("status = %q", pb.Status)
	}
	if v := pb.Config["Threshold"]; v != "7" {
		t.Errorf("Config[Threshold] = %q, want %q", v, "7")
	}

	if len(pb.Stages) != 2 {
		t.Fatalf("stages = %d, want 2", len(pb.Stages))
	}

	// Stage 1
	s1 := pb.Stages[0]
	if s1.Number != 1 {
		t.Errorf("stage 1 number = %d, want 1", s1.Number)
	}
	if s1.Name != "fetch" {
		t.Errorf("stage 1 name = %q, want %q", s1.Name, "fetch")
	}
	wantRead := "\x60config.md\x60 (for threshold)"
	if len(s1.Reads) != 1 || s1.Reads[0] != wantRead {
		t.Errorf("stage 1 reads = %v", s1.Reads)
	}
	if len(s1.Dos) != 3 {
		t.Errorf("stage 1 dos = %d, want 3", len(s1.Dos))
	}
	if want := []string{"read_file", "write_file"}; !reflect.DeepEqual(s1.Tools, want) {
		t.Errorf("stage 1 tools = %v, want %v", s1.Tools, want)
	}
	if s1.Write != "output/01-data.md" {
		t.Errorf("stage 1 write = %q, want %q", s1.Write, "output/01-data.md")
	}

	// Stage 2
	s2 := pb.Stages[1]
	if s2.Number != 2 {
		t.Errorf("stage 2 number = %d, want 2", s2.Number)
	}
	if s2.Name != "report" {
		t.Errorf("stage 2 name = %q, want %q", s2.Name, "report")
	}
	if len(s2.Reads) != 1 {
		t.Errorf("stage 2 reads = %d, want 1", len(s2.Reads))
	}
	if s2.Write != "output/02-report.md" {
		t.Errorf("stage 2 write = %q, want %q", s2.Write, "output/02-report.md")
	}

	// output path
	out := outputPath(pb, s1)
	expectedOut := filepath.Join(dir, "output", "01-data.md")
	if out != expectedOut {
		t.Errorf("outputPath = %q, want %q", out, expectedOut)
	}
}

func TestLoadPlaybookStagesSubdir(t *testing.T) {
	// Real-world authoring puts stages under stages/ with README.md at top
	// level; the loader must ignore README and pick up stages/ files in the
	// order their declared "# Stage N" headings say.
	tmp := t.TempDir()
	playbooksDir := filepath.Join(tmp, "playbooks")
	dir := filepath.Join(playbooksDir, "news-daily")
	os.MkdirAll(filepath.Join(dir, "stages"), 0700)
	os.MkdirAll(filepath.Join(dir, "output"), 0700)

	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# News Daily\n\nNot a stage.\n"), 0644)
	os.WriteFile(filepath.Join(dir, "config.md"), []byte("description: test\n"), 0644)
	os.WriteFile(filepath.Join(dir, "stages", "search.md"), []byte("# Stage 1: Search\n\n## Do\n\n1. Search the web.\n"), 0644)
	os.WriteFile(filepath.Join(dir, "stages", "summarize.md"), []byte("# Stage 2: Summarize\n\n## Do\n\n1. Summarize.\n"), 0644)
	os.WriteFile(filepath.Join(dir, "stages", "report.md"), []byte("# Stage 3: Report\n\n## Do\n\n1. Report.\n"), 0644)

	pb, err := LoadPlaybook(playbooksDir, "news-daily")
	if err != nil {
		t.Fatalf("LoadPlaybook: %v", err)
	}
	if len(pb.Stages) != 3 {
		t.Fatalf("stages = %d, want 3 (README.md must not count)", len(pb.Stages))
	}
	want := []struct {
		number int
		name   string
	}{
		{1, "search"},
		{2, "summarize"},
		{3, "report"},
	}
	for i, w := range want {
		if pb.Stages[i].Number != w.number || pb.Stages[i].Name != w.name {
			t.Errorf("stage %d = %d/%q, want %d/%q", i, pb.Stages[i].Number, pb.Stages[i].Name, w.number, w.name)
		}
	}
}

func TestParseStageToolLinesWithComments(t *testing.T) {
	// "- bash (to check existence)" must parse to "bash", and "None" bullets
	// must be dropped (leaving no restriction → full toolset at runtime).
	tmp := t.TempDir()
	f := filepath.Join(tmp, "01-do.md")
	os.WriteFile(f, []byte(`# Do it

## Tools

- bash (to check file existence)
- read_file (to read the content)
- None (text crafting only)

## Write

`+"`output/01-do.md`"+`
`), 0644)

	stage, err := parseStage(tmp, "01-do.md")
	if err != nil {
		t.Fatalf("parseStage: %v", err)
	}
	if want := []string{"bash", "read_file"}; !reflect.DeepEqual(stage.Tools, want) {
		t.Errorf("tools = %v, want %v", stage.Tools, want)
	}

	// a stage whose only bullet is "None" gets no restriction at all
	os.WriteFile(f, []byte(`# Craft only

## Tools

- None (LLM reasoning only)

## Write

`+"`output/01-do.md`"+`
`), 0644)
	stage, err = parseStage(tmp, "01-do.md")
	if err != nil {
		t.Fatalf("parseStage: %v", err)
	}
	if len(stage.Tools) != 0 {
		t.Errorf("tools = %v, want none", stage.Tools)
	}
}

func TestParseStageMissingSections(t *testing.T) {
	// stage with only ## Do should work
	tmp := t.TempDir()
	f := filepath.Join(tmp, "01-minimal.md")
	os.WriteFile(f, []byte(`# Minimal stage

## Do

1. Do one thing
`), 0644)

	stage, err := parseStage(tmp, "01-minimal.md")
	if err != nil {
		t.Fatalf("parseStage: %v", err)
	}
	if len(stage.Dos) != 1 {
		t.Errorf("dos = %d, want 1", len(stage.Dos))
	}
	if stage.Write != "" {
		t.Errorf("write = %q, want empty", stage.Write)
	}
}

func TestBuildStagePromptIncludesUserRequest(t *testing.T) {
	pb := &Playbook{Name: "procurement", Dir: t.TempDir()}
	stage := StageFile{Number: 1, Name: "fetch"}

	got := buildStagePrompt(pb, stage, "send me last week's purchase data")
	if !strings.Contains(got, "## User Request") ||
		!strings.Contains(got, "send me last week's purchase data") {
		t.Fatalf("prompt did not include user request: %q", got)
	}
}

func TestExecuteStageAcceptsFreshOutputAtIterationLimit(t *testing.T) {
	pb := &Playbook{Name: "generic", Dir: t.TempDir()}
	stage := StageFile{Number: 1, Name: "write", Write: "output/result.md"}
	outPath := outputPath(pb, stage)
	registry := NewRegistry()
	registry.Register(makeWriteTool(pb.Dir, pb.Dir))
	registry.Register(makeReadTool())

	script := []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("write_file", map[string]any{"path": outPath, "content": "result"})}, "tool_use"),
	}
	for range maxStageIterations - 1 {
		script = append(script, scriptedResp([]ContentBlock{toolBlock("read_file", map[string]any{"path": outPath})}, "tool_use"))
	}

	reply, calls, _, _, err := executeStage(
		context.Background(), &fakeClient{script: script}, "fresh-output", "",
		pb, stage, "do the work", nil, registry, 100, nil, pb.Dir,
	)
	if err != nil {
		t.Fatalf("executeStage: %v", err)
	}
	if reply != "" {
		t.Fatalf("iteration-limit receipt should rely on output, got %q", reply)
	}
	if len(calls) != maxStageIterations {
		t.Fatalf("calls = %d, want %d", len(calls), maxStageIterations)
	}
}

func TestExecuteStageRejectsUnchangedOutput(t *testing.T) {
	pb := &Playbook{Name: "generic", Dir: t.TempDir()}
	stage := StageFile{Number: 1, Name: "write", Write: "output/result.md"}
	outPath := outputPath(pb, stage)
	if err := os.WriteFile(outPath, []byte("old result"), 0600); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{textBlock("DONE")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("DONE")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("DONE")}, "stop"),
	}}

	_, _, _, _, err := executeStage(
		context.Background(), client, "stale-output", "", pb, stage,
		"do the work", nil, NewRegistry(), 100, nil, pb.Dir,
	)
	if err == nil || !strings.Contains(err.Error(), "not created or updated") {
		t.Fatalf("error = %v", err)
	}
	if client.pos != maxStageRetries {
		t.Fatalf("attempts = %d, want %d", client.pos, maxStageRetries)
	}
}

func TestExecuteStageRejectsMissingOutputAtIterationLimit(t *testing.T) {
	pb := &Playbook{Name: "generic", Dir: t.TempDir()}
	stage := StageFile{Number: 1, Name: "write", Write: "output/result.md"}
	registry := NewRegistry()
	registry.Register(makeReadTool())
	script := make([]*LLMResponse, 0, maxStageIterations)
	for range maxStageIterations {
		script = append(script, scriptedResp([]ContentBlock{toolBlock("read_file", map[string]any{"path": filepath.Join(pb.Dir, "missing")})}, "tool_use"))
	}

	_, _, _, _, err := executeStage(
		context.Background(), &fakeClient{script: script}, "missing-output", "",
		pb, stage, "do the work", nil, registry, 100, nil, pb.Dir,
	)
	if err == nil || !strings.Contains(err.Error(), "iteration_limit") {
		t.Fatalf("error = %v", err)
	}
}

func TestExecuteStagePreservesCancellationWithOldOutput(t *testing.T) {
	pb := &Playbook{Name: "generic", Dir: t.TempDir()}
	stage := StageFile{Number: 1, Name: "write", Write: "output/result.md"}
	if err := os.WriteFile(outputPath(pb, stage), []byte("old result"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, _, _, err := executeStage(
		ctx, &fakeClient{}, "cancelled-output", "", pb, stage,
		"do the work", nil, NewRegistry(), 100, nil, pb.Dir,
	)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildPlaybookSystemOmitsNestedPlaybookRouting(t *testing.T) {
	settings := &Settings{Home: t.TempDir(), Workspace: t.TempDir(), ContextChars: 10000}
	session := NewSession(settings, nil)
	got := session.BuildPlaybookSystem("run the procurement audit", "")
	if strings.Contains(got, "RELEVANT PLAYBOOK") {
		t.Fatalf("playbook system recursively advertises routing: %q", got)
	}
}

func TestFormatPlaybookResultReportsGenericOutputs(t *testing.T) {
	outputs := []string{
		filepath.Join(t.TempDir(), "output", "first.md"),
		filepath.Join(t.TempDir(), "output", "second.md"),
	}
	got := formatPlaybookResult(&PlaybookResult{
		Name: "example", Status: "complete", StagesRun: 2,
		Outputs: outputs, Reply: "DONE",
	})
	for _, output := range outputs {
		if !strings.Contains(got, "- "+output) {
			t.Fatalf("receipt missing output %q: %q", output, got)
		}
	}
	if strings.Contains(strings.ToLower(got), "telegram") ||
		strings.Contains(strings.ToLower(got), "delivered") {
		t.Fatalf("generic receipt invented delivery: %q", got)
	}
}

func TestRespondForLetsModelChooseMatchedPlaybook(t *testing.T) {
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "procurement-audit", []string{"01-fetch"})
	dir := filepath.Join(home, "playbooks", "procurement-audit")
	if err := os.WriteFile(filepath.Join(dir, "config.md"), []byte("description: Weekly procurement audit\n"), 0600); err != nil {
		t.Fatal(err)
	}

	calls, sawCandidate := 0, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		sawCandidate = strings.Contains(string(body), "POSSIBLY RELEVANT PLAYBOOK")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Handled through normal reasoning."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	defer server.Close()

	settings := &Settings{
		Home: home, Workspace: home, ContextChars: 100000,
		MaxIter: 5, MaxTokens: 1000, Timezone: "Asia/Kuala_Lumpur",
	}
	db := Connect(home)
	defer db.Close()
	client := fakePM(server.URL)
	mem := NewMemory(db, client, settings)
	core := &Core{
		Settings: settings, DB: db, Client: client, Memory: mem,
		Tools: NewRegistry(), Sessions: NewSessionManager(settings, mem),
	}
	core.Tools.Register(makeRunPlaybookTool(core))

	result := core.RespondFor("routing-test", "send me the procurement data", "dashboard", nil, false)
	if result.Reply != "Handled through normal reasoning." {
		t.Fatalf("reply = %q", result.Reply)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want one normal reasoning turn", calls)
	}
	if !sawCandidate {
		t.Fatal("matched playbook was not offered to the model")
	}
}

func TestMatchPlaybookKeyword(t *testing.T) {
	tmp := t.TempDir()
	writeWorkspacePlaybook(t, tmp, "procurement-audit", []string{"01-fetch"})
	os.WriteFile(filepath.Join(tmp, "playbooks", "procurement-audit", "config.md"), []byte("description: Weekly procurement audit fetch POs and analyze\n"), 0644)

	name, desc, score := MatchPlaybook(tmp, "send me the procurement data", nil)
	if name != "procurement-audit" {
		t.Errorf("match name = %q, want procurement-audit", name)
	}
	if desc == "" {
		t.Error("match desc empty")
	}
	if score <= 0 {
		t.Error("match score zero")
	}
}

func TestListPlaybooks(t *testing.T) {
	tmp := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		writeWorkspacePlaybook(t, tmp, name, []string{"01-test"})
	}

	names := ListPlaybooks(tmp)
	if len(names) != 3 {
		t.Fatalf("list = %d, want 3", len(names))
	}
	if names[0] != "alpha" || names[1] != "beta" || names[2] != "gamma" {
		t.Errorf("list = %v, want [alpha beta gamma]", names)
	}
}

func TestPlaybookCatalogUsesParsedFilesystemState(t *testing.T) {
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "procurement-audit", []string{"01-fetch"})
	dir := filepath.Join(home, "playbooks", "procurement-audit")
	if err := os.WriteFile(filepath.Join(dir, "config.md"), []byte("description: Weekly audit\nschedule: Mon 09:00 Asia/Kuala_Lumpur\nstatus: active\nnotify: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pb, err := loadPlaybookWorkspace(home, "procurement-audit")
	if err != nil {
		t.Fatal(err)
	}
	run, err := loadOrCreatePlaybookRun(pb, "test", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	path := playbookRunOutputPath(pb, run, pb.Stages[0], pb.Stages[0].Outputs[0])
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("done"), 0600); err != nil {
		t.Fatal(err)
	}
	run.Status, run.Stages[0].Status, run.Stages[0].Outputs = "complete", "complete", []string{path}
	if err := savePlaybookRun(pb, run); err != nil {
		t.Fatal(err)
	}

	catalog := playbookCatalog(home)
	if len(catalog) != 1 {
		t.Fatalf("catalog = %#v, want one playbook", catalog)
	}
	item := catalog[0]
	if item["name"] != "procurement-audit" || item["description"] != "Weekly audit" || item["notify"] != true {
		t.Fatalf("catalog metadata = %#v", item)
	}
	stages, ok := item["stages"].([]map[string]any)
	if !ok || len(stages) != 1 {
		t.Fatalf("catalog stages = %#v", item["stages"])
	}
	outputs, ok := item["outputs"].([]string)
	if !ok || len(outputs) != 1 || outputs[0] != filepath.Join("playbooks", "procurement-audit", "runs", run.ID, "stages", "01-fetch", "output", "result.md") {
		t.Fatalf("catalog outputs = %#v", item["outputs"])
	}
}

func TestSchedulePlaybook(t *testing.T) {
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "news", []string{"01-news"})
	dir := filepath.Join(home, "playbooks", "news")
	if err := os.WriteFile(filepath.Join(dir, "config.md"), []byte("description: News\nstatus: active\n"), 0600); err != nil {
		t.Fatal(err)
	}

	got := makeSchedulePlaybookTool(home, "Asia/Kuala_Lumpur").ContextFn(nil, map[string]any{"name": "news", "time": "20:00"})
	if !strings.Contains(got, "Scheduled news daily at 20:00") {
		t.Fatalf("schedule result = %q", got)
	}

	// verify schedules.json was written
	scheds, err := loadSchedules(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(scheds) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(scheds))
	}
	if scheds[0].Name != "news" || scheds[0].Time != "20:00" || scheds[0].Timezone != "Asia/Kuala_Lumpur" {
		t.Fatalf("schedule = %+v", scheds[0])
	}

	// cancel
	cancelGot := makeCancelScheduleTool(home).ContextFn(nil, map[string]any{"name": "news"})
	if !strings.Contains(cancelGot, "Cancelled schedule for news") || !strings.Contains(cancelGot, "0 schedule(s) remain") {
		t.Fatalf("cancel result = %q", cancelGot)
	}
	scheds, _ = loadSchedules(home)
	if len(scheds) != 0 {
		t.Fatalf("expected 0 schedules after cancel, got %d", len(scheds))
	}
}

func TestDueRoutineRecordsVerifiedResultWithEvidence(t *testing.T) {
	home := t.TempDir()
	now := time.Now().UTC()
	location, err := time.LoadLocation("Asia/Kuala_Lumpur")
	if err != nil {
		t.Fatal(err)
	}
	schedule := PlaybookSchedule{
		Name: "ai-news-daily", Time: now.In(location).Format("15:04"), Timezone: location.String(),
	}
	if err := saveSchedules(home, []PlaybookSchedule{schedule}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(home, "playbooks", schedule.Name, "output", "01-ai-news.md")
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("# AI news\n\nVerified report."), 0600); err != nil {
		t.Fatal(err)
	}
	db := Connect(home)
	defer db.Close()
	store := NewResponsibilityStore(db)
	if _, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "routine:" + schedule.Name, Type: "imported", Kind: "routine",
		Title: "Daily AI news", Owner: "mino", Status: "waiting",
		Summary: "Imported from the existing schedule.", SourceKind: "schedule",
		SourceRef: schedule.Name, Schedule: schedule.Time + " " + schedule.Timezone, At: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	core := &Core{
		Settings:         &Settings{Home: home, Timezone: "Asia/Kuala_Lumpur"},
		Responsibilities: store,
	}
	sawWorking := false
	run := func(context.Context, *Core, string, string, string, Observer) (*PlaybookResult, error) {
		items, err := store.List(ResponsibilityFilter{Kind: "routine"})
		if err != nil || len(items) != 1 {
			t.Fatalf("working projection: items=%+v err=%v", items, err)
		}
		sawWorking = items[0].Status == "working"
		return &PlaybookResult{
			Name: schedule.Name, Status: "complete", StagesRun: 1,
			Outputs: []string{output}, Reply: "Report written.",
		}, nil
	}

	dispatchDueSchedulesAt(core, now, run)

	items, err := store.List(ResponsibilityFilter{Kind: "routine"})
	if err != nil || len(items) != 1 {
		t.Fatalf("Routine projection: items=%+v err=%v", items, err)
	}
	got := items[0]
	if got.LastRunAt == nil {
		t.Fatalf("Routine projection has no last run: %+v", got)
	}
	wantDue, err := nextRoutineRun(schedule, *got.LastRunAt)
	if err != nil {
		t.Fatal(err)
	}
	if !sawWorking || got.ID != "routine:ai-news-daily" || got.Status != "verified" ||
		got.DueAt == nil || !got.DueAt.Equal(wantDue) {
		t.Fatalf("Routine projection = %+v, saw working=%v", got, sawWorking)
	}
	history, err := store.History(got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || history[1].Status != "working" || history[2].Status != "verified" {
		t.Fatalf("Routine History = %+v", history)
	}
	if !strings.Contains(history[2].Evidence, "playbooks/ai-news-daily/output/01-ai-news.md") ||
		strings.Contains(strings.ToLower(history[2].Evidence), "telegram delivered") {
		t.Fatalf("completion Evidence = %q", history[2].Evidence)
	}
	schedules, err := loadSchedules(home)
	if err != nil || len(schedules) != 1 || schedules[0].LastRun != history[2].At.Format(time.RFC3339) {
		t.Fatalf("saved schedules = %+v, err=%v", schedules, err)
	}
}

func TestOneOffPlaybookRecordsVerifiedResponsibility(t *testing.T) {
	home := t.TempDir()
	output := filepath.Join(home, "playbooks", "audit", "output", "report.md")
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("verified report"), 0600); err != nil {
		t.Fatal(err)
	}
	db := Connect(home)
	defer db.Close()
	store := NewResponsibilityStore(db)
	core := &Core{
		Settings:         &Settings{Home: home},
		Responsibilities: store,
	}
	at := time.Date(2026, 7, 31, 4, 0, 0, 0, time.UTC)
	run := func(context.Context, *Core, string, string, string, Observer) (*PlaybookResult, error) {
		return &PlaybookResult{Name: "audit", Status: "complete", StagesRun: 1, Outputs: []string{output}, Reply: "Report written."}, nil
	}

	got, err := runPlaybookWithResponsibility(context.Background(), core, "audit", "Run the audit", "dashboard-1", run, at)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "audit — complete") {
		t.Fatalf("tool result = %q", got)
	}
	items, err := store.List(ResponsibilityFilter{Kind: "one_off"})
	if err != nil || len(items) != 1 {
		t.Fatalf("one-off responsibilities = %+v, err=%v", items, err)
	}
	item := items[0]
	if item.Status != "verified" || item.SourceKind != "playbook" || !strings.HasPrefix(item.SourceRef, "one-off:audit:dashboard-1:") {
		t.Fatalf("one-off projection = %+v", item)
	}
	if item.Title != "Run audit once" || item.Outcome != "Run the audit" {
		t.Fatalf("one-off language = title %q outcome %q", item.Title, item.Outcome)
	}
	history, err := store.History(item.ID)
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %+v, err=%v", history, err)
	}
	if history[0].Type != "accepted" || history[0].Status != "working" || history[1].Type != "completed" || history[1].Status != "verified" {
		t.Fatalf("history = %+v", history)
	}
	if !strings.Contains(history[1].Evidence, "artifact:playbooks/audit/output/report.md") {
		t.Fatalf("evidence = %q", history[1].Evidence)
	}
}

func TestOneOffPlaybookBlocksWithoutReadableEvidence(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	store := NewResponsibilityStore(db)
	core := &Core{Settings: &Settings{Home: home}, Responsibilities: store}
	at := time.Date(2026, 7, 31, 4, 0, 0, 0, time.UTC)
	run := func(context.Context, *Core, string, string, string, Observer) (*PlaybookResult, error) {
		return &PlaybookResult{Name: "audit", Status: "complete", Reply: "Nothing written."}, nil
	}
	if _, err := runPlaybookWithResponsibility(context.Background(), core, "audit", "Run the audit", "dashboard-2", run, at); err != nil {
		t.Fatal(err)
	}
	items, err := store.List(ResponsibilityFilter{Kind: "one_off", Status: "blocked"})
	if err != nil || len(items) != 1 {
		t.Fatalf("blocked one-off responsibilities = %+v, err=%v", items, err)
	}
	history, err := store.History(items[0].ID)
	if err != nil || len(history) != 2 || history[1].Status != "blocked" {
		t.Fatalf("history = %+v, err=%v", history, err)
	}
}

func TestVerifiedRoutineRunsAgainOnTheNextScheduledDay(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	schedule := PlaybookSchedule{
		Name: "ai-news-daily", Time: "20:00", Timezone: "Asia/Kuala_Lumpur",
		LastRun: now.AddDate(0, 0, -1).Format(time.RFC3339),
	}
	if err := saveSchedules(home, []PlaybookSchedule{schedule}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(home, "playbooks", schedule.Name, "output", "01-ai-news.md")
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("# AI news\n\nSecond report."), 0600); err != nil {
		t.Fatal(err)
	}
	db := Connect(home)
	defer db.Close()
	store := NewResponsibilityStore(db)
	if _, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "routine:" + schedule.Name, Type: "completed", Kind: "routine",
		Title: "Daily AI news", Owner: "mino", Status: "verified",
		Summary: "Yesterday's run completed.", Evidence: "artifact:yesterday.md",
		SourceKind: "schedule", SourceRef: schedule.Name, At: now.AddDate(0, 0, -1),
	}); err != nil {
		t.Fatal(err)
	}
	core := &Core{
		Settings:         &Settings{Home: home, Timezone: "Asia/Kuala_Lumpur"},
		Responsibilities: store,
	}
	run := func(context.Context, *Core, string, string, string, Observer) (*PlaybookResult, error) {
		return &PlaybookResult{Name: schedule.Name, Status: "complete", StagesRun: 1, Outputs: []string{output}}, nil
	}

	dispatchDueSchedulesAt(core, now, run)

	history, err := store.History("routine:" + schedule.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || history[1].Status != "working" || history[2].Status != "verified" {
		t.Fatalf("recurring Routine History = %+v", history)
	}
}

func TestDueRoutineUsesTheScheduleTimezoneDate(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 1, 16, 0, 0, 0, time.UTC) // midnight on 2 August in Malaysia
	schedule := PlaybookSchedule{Name: "midnight-brief", Time: "00:00", Timezone: "Asia/Kuala_Lumpur"}
	if err := saveSchedules(home, []PlaybookSchedule{schedule}); err != nil {
		t.Fatal(err)
	}
	db := Connect(home)
	defer db.Close()
	store := NewResponsibilityStore(db)
	if _, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "routine:" + schedule.Name, Type: "imported", Kind: "routine",
		Title: "Midnight brief", Owner: "mino", Status: "waiting", Summary: "Imported.",
		SourceKind: "schedule", SourceRef: schedule.Name, At: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	core := &Core{Settings: &Settings{Home: home}, Responsibilities: store}
	ran := false
	run := func(context.Context, *Core, string, string, string, Observer) (*PlaybookResult, error) {
		ran = true
		return &PlaybookResult{Name: schedule.Name, Status: "failed", Reply: "Provider unavailable."}, nil
	}

	dispatchDueSchedulesAt(core, now, run)

	history, err := store.History("routine:" + schedule.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !ran || len(history) != 3 || history[1].Status != "working" || history[2].Status != "blocked" {
		t.Fatalf("midnight Routine ran=%v History=%+v", ran, history)
	}
}

func TestDueRoutineBlocksCompletionWithoutReadableEvidence(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	schedule := PlaybookSchedule{Name: "ai-news-daily", Time: "20:00", Timezone: "Asia/Kuala_Lumpur"}
	if err := saveSchedules(home, []PlaybookSchedule{schedule}); err != nil {
		t.Fatal(err)
	}
	db := Connect(home)
	defer db.Close()
	store := NewResponsibilityStore(db)
	if _, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "routine:" + schedule.Name, Type: "imported", Kind: "routine",
		Title: "Daily AI news", Owner: "mino", Status: "waiting", Summary: "Imported.",
		SourceKind: "schedule", SourceRef: schedule.Name, At: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	core := &Core{Settings: &Settings{Home: home}, Responsibilities: store}
	run := func(context.Context, *Core, string, string, string, Observer) (*PlaybookResult, error) {
		return &PlaybookResult{
			Name: schedule.Name, Status: "complete",
			Outputs: []string{filepath.Join(home, "playbooks", schedule.Name, "output", "missing.md")},
		}, nil
	}

	dispatchDueSchedulesAt(core, now, run)

	detail, err := store.Detail("routine:" + schedule.Name)
	if err != nil {
		t.Fatal(err)
	}
	latest := detail.History[len(detail.History)-1]
	if detail.Status != "blocked" || latest.Type != "blocked" ||
		latest.Summary != "Scheduled run completed without a readable output." {
		t.Fatalf("unverified Routine = %+v", detail)
	}
}

func TestDueRoutineRecordsActualCompletionTime(t *testing.T) {
	home := t.TempDir()
	location, err := time.LoadLocation("Asia/Kuala_Lumpur")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-time.Second)
	schedule := PlaybookSchedule{
		Name: "timed-run", Time: started.In(location).Format("15:04"), Timezone: location.String(),
	}
	if err := saveSchedules(home, []PlaybookSchedule{schedule}); err != nil {
		t.Fatal(err)
	}
	db := Connect(home)
	defer db.Close()
	store := NewResponsibilityStore(db)
	if _, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "routine:" + schedule.Name, Type: "imported", Kind: "routine",
		Title: "Timed run", Owner: "mino", Status: "waiting", Summary: "Imported.",
		SourceKind: "schedule", SourceRef: schedule.Name, At: started.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	core := &Core{Settings: &Settings{Home: home}, Responsibilities: store}
	run := func(context.Context, *Core, string, string, string, Observer) (*PlaybookResult, error) {
		time.Sleep(time.Millisecond)
		return &PlaybookResult{Name: schedule.Name, Status: "failed", Reply: "Provider unavailable."}, nil
	}

	dispatchDueSchedulesAt(core, started, run)

	history, err := store.History("routine:" + schedule.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || !history[2].At.After(history[1].At) {
		t.Fatalf("Routine History timestamps = %+v", history)
	}
	schedules, err := loadSchedules(home)
	if err != nil || len(schedules) != 1 || schedules[0].LastRun != history[2].At.Format(time.RFC3339) {
		t.Fatalf("saved schedules = %+v, err=%v", schedules, err)
	}
}

func TestSystemCheckReportsState(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	tool := makeSystemCheckTool(db, home)
	got := tool.Fn(nil)
	if !strings.Contains(got, "schedules: 0") || !strings.Contains(got, "pending_reminders: 0") || !strings.Contains(got, "playbooks: 0") {
		t.Fatalf("system check = %q", got)
	}
}

func TestParseStageTakesFirstBacktickPair(t *testing.T) {
	// A second Write bullet is commentary: it must never change the verified
	// output path (the 2026-07-31 incident: an LLM appended a bullet mid-run
	// to move the verification goalpost).
	tmp := t.TempDir()
	f := filepath.Join(tmp, "03-save.md")
	os.WriteFile(f, []byte("# Save\n\n## Do\n\n1. Save the file.\n\n## Write\n\n- `/home/mino/knowledge/YYYY-MM-DD.md`\n- `output/03-save.md` — confirmation that knowledge was saved\n"), 0644)

	stage, err := parseStage(tmp, "03-save.md")
	if err != nil {
		t.Fatalf("parseStage: %v", err)
	}
	if stage.Write != "/home/mino/knowledge/YYYY-MM-DD.md" {
		t.Fatalf("stage.Write = %q, want first bullet only", stage.Write)
	}
}

func TestOutputPathHonorsAbsoluteAndExpandsDates(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Kuala_Lumpur")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(loc).Format("2006-01-02")

	tests := []struct {
		name  string
		write string
		loc   *time.Location
		want  string
	}{
		{
			name:  "relative rebased into output dir",
			write: "output/01-data.md",
			want:  filepath.Join("pbdir", "output", "01-data.md"),
		},
		{
			name:  "absolute path kept as-is",
			write: "/home/mino/knowledge/ai-daily/2026-07-31.md",
			want:  "/home/mino/knowledge/ai-daily/2026-07-31.md",
		},
		{
			name:  "date template expanded in loc",
			write: "/home/mino/knowledge/ai-daily/YYYY-MM-DD.md",
			loc:   loc,
			want:  "/home/mino/knowledge/ai-daily/" + today + ".md",
		},
		{
			name:  "relative date template",
			write: "output/YYYY-MM-DD.md",
			loc:   loc,
			want:  filepath.Join("pbdir", "output", today+".md"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pb := &Playbook{Name: "x", Dir: "pbdir"}
			stage := StageFile{Number: 1, Name: "data", Write: tt.write}
			got := outputPath(pb, stage, tt.loc)
			if got != tt.want {
				t.Fatalf("outputPath = %q, want %q", got, tt.want)
			}
		})
	}

	// nil loc must not panic (variadic default)
	pb := &Playbook{Name: "x", Dir: "pbdir"}
	got := outputPath(pb, StageFile{Number: 2, Name: "n", Write: "output/YYYY-MM-DD.md"})
	if got != filepath.Join("pbdir", "output", time.Now().Format("2006-01-02")+".md") {
		t.Fatalf("nil-loc outputPath = %q", got)
	}
}

func TestLoadPlaybookRejectsOutputOutsideHome(t *testing.T) {
	home := t.TempDir()
	playbooksDir := filepath.Join(home, "playbooks")
	dir := filepath.Join(playbooksDir, "evil")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "01-steal.md"), []byte("# Steal\n\n## Write\n\n- `"+`/etc/passwd`+"`\n"), 0644)

	if _, err := LoadPlaybook(playbooksDir, "evil"); err == nil || !strings.Contains(err.Error(), "outside the allowed root") {
		t.Fatalf("LoadPlaybook error = %v, want outside-the-root rejection", err)
	}

	// absolute path under home stays valid
	os.WriteFile(filepath.Join(dir, "01-steal.md"), []byte("# Ok\n\n## Write\n\n- `"+filepath.Join(home, "knowledge", "x.md")+"`\n"), 0644)
	if _, err := LoadPlaybook(playbooksDir, "evil"); err != nil {
		t.Fatalf("LoadPlaybook with in-home absolute path: %v", err)
	}
}

func TestStartRoutineCreatesFreshResponsibility(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	store := NewResponsibilityStore(db)

	now := time.Now().UTC()
	if err := store.startRoutine(PlaybookSchedule{Name: "ai-daily-learn"}, now); err != nil {
		t.Fatalf("startRoutine on a fresh schedule: %v", err)
	}
	items, err := store.List(ResponsibilityFilter{Kind: "routine"})
	if err != nil || len(items) != 1 {
		t.Fatalf("routine projection: items=%+v err=%v", items, err)
	}
	if items[0].ID != "routine:ai-daily-learn" || items[0].Status != "working" || items[0].Kind != "routine" {
		t.Fatalf("routine = %+v", items[0])
	}
}

func TestDispatchDueSchedulesRecordsFireFailure(t *testing.T) {
	home := t.TempDir()
	now := time.Now().UTC()
	location, err := time.LoadLocation("Asia/Kuala_Lumpur")
	if err != nil {
		t.Fatal(err)
	}
	schedule := PlaybookSchedule{
		Name: "ai-daily-learn", Time: now.In(location).Format("15:04"), Timezone: location.String(),
	}
	if err := saveSchedules(home, []PlaybookSchedule{schedule}); err != nil {
		t.Fatal(err)
	}

	// closed DB forces startRoutine's Record to fail, as it did for every
	// schedule before kind/title/owner were provided.
	db := Connect(home)
	store := NewResponsibilityStore(db)
	db.Close()
	core := &Core{
		Settings:         &Settings{Home: home, Timezone: "Asia/Kuala_Lumpur"},
		Responsibilities: store,
	}
	ran := false
	run := func(context.Context, *Core, string, string, string, Observer) (*PlaybookResult, error) {
		ran = true
		return &PlaybookResult{Name: schedule.Name, Status: "complete"}, nil
	}

	dispatchDueSchedulesAt(core, now, run)

	if ran {
		t.Fatal("runner called despite startRoutine failure")
	}
	scheds, err := loadSchedules(home)
	if err != nil || len(scheds) != 1 {
		t.Fatalf("schedules = %+v, err=%v", scheds, err)
	}
	if scheds[0].LastError == "" {
		t.Fatalf("last_error not recorded: %+v", scheds[0])
	}
	if scheds[0].LastRun != "" {
		t.Fatalf("last_run set despite failed fire: %+v", scheds[0])
	}
	// the failure must be visible in the trace, not just journald
	trace, err := os.ReadFile(filepath.Join(home, "traces", time.Now().Format("2006-01-02")+".jsonl"))
	if err != nil || !strings.Contains(string(trace), "schedule_fire_failed") {
		t.Fatalf("trace missing schedule_fire_failed entry (err=%v)", err)
	}
}

func TestLoadPlaybookRequiresWriteFileWhenToolsDeclared(t *testing.T) {
	tests := []struct {
		name    string
		tools   string // "## Tools" section body, "" for none
		wantErr bool
	}{
		{name: "tools without write_file and an output", tools: "- read_file\n- bash\n", wantErr: true},
		{name: "tools including write_file", tools: "- write_file\n- read_file\n", wantErr: false},
		{name: "no tools section is unrestricted", tools: "", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, "playbooks", "pbtest")
			os.MkdirAll(dir, 0700)
			content := "# Stage 1: Do\n\n## Do\n\n1. Work.\n"
			if tt.tools != "" {
				content += "\n## Tools\n" + tt.tools + "\n"
			}
			content += "\n## Write\n\n- `output/01-result.md`\n"
			os.WriteFile(filepath.Join(dir, "01-do.md"), []byte(content), 0644)

			_, err := LoadPlaybook(filepath.Join(home, "playbooks"), "pbtest")
			if tt.wantErr && (err == nil || !strings.Contains(err.Error(), "without write_file")) {
				t.Fatalf("error = %v, want write_file contract error", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRunPlaybookRejectsUnknownDeclaredTool(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "playbooks", "phantom", "stages", "01-do")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(home, "playbooks", "phantom", "CONTEXT.md"), []byte("# Phantom\n"), 0644)
	os.WriteFile(filepath.Join(dir, "CONTEXT.md"), []byte("# Do\n\n## Tools\n- write_file\n- invoke_llm\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Result | `output/result.md` | Markdown |\n"), 0644)

	core := &Core{
		Settings: &Settings{Home: home},
		Tools:    NewRegistry(),
	}
	core.Tools.Register(&Tool{Name: "write_file", Schema: map[string]any{"type": "object"}})

	_, err := RunPlaybook(context.Background(), core, "phantom", "", "sess", nil)
	if err == nil || !strings.Contains(err.Error(), "declares unknown tool \"invoke_llm\"") {
		t.Fatalf("error = %v, want unknown-tool rejection", err)
	}
}

func TestParseStageExtractsReferences(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "01-make.md"), []byte("# Stage 1\n\n## Do\n\n1. Work.\n\n## References\n\n- `references/voice.md`\n- references/conventions.md\n"), 0644)
	stage, err := parseStage(tmp, "01-make.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(stage.Refs) != 2 || stage.Refs[0] != "references/voice.md" || stage.Refs[1] != "references/conventions.md" {
		t.Fatalf("refs = %v", stage.Refs)
	}
}

func TestBuildStagePromptIncludesReferences(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "references"), 0700)
	os.WriteFile(filepath.Join(dir, "references", "voice.md"), []byte("Write in a warm, concise tone. Always address Abah."), 0644)

	pb := &Playbook{Name: "news", Dir: dir}
	stage := StageFile{Number: 1, Name: "make", Path: filepath.Join(dir, "01-make.md"), Refs: []string{"references/voice.md"}}

	got := buildStagePrompt(pb, stage, "")
	if !strings.Contains(got, "## Stage References") || !strings.Contains(got, "warm, concise tone") {
		t.Fatalf("prompt missing references: %.300s", got)
	}

	// missing reference must not break the prompt
	stage.Refs = []string{"references/ghost.md", "references/voice.md"}
	got = buildStagePrompt(pb, stage, "")
	if !strings.Contains(got, "warm, concise tone") {
		t.Fatalf("prompt missing surviving reference: %.300s", got)
	}
}

func TestBuildStagePromptCapsReferences(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "refs"), 0700)
	big := strings.Repeat("x", 10000)
	os.WriteFile(filepath.Join(dir, "refs", "big.md"), []byte(big), 0644)

	pb := &Playbook{Name: "news", Dir: dir}
	stage := StageFile{Number: 1, Name: "make", Path: filepath.Join(dir, "01-make.md"), Refs: []string{"refs/big.md"}}
	got := buildStagePrompt(pb, stage, "")
	refSection := ""
	if i := strings.Index(got, "## Stage References"); i >= 0 {
		end := strings.Index(got[i:], "## Rules")
		if end >= 0 {
			refSection = got[i : i+end]
		} else {
			refSection = got[i:]
		}
	}
	if refSection == "" {
		t.Fatalf("references section missing")
	}
	if len(refSection) > 4300 {
		t.Fatalf("references section too large: %d chars", len(refSection))
	}
}

func TestExecuteStageRetryPromptIncludesPreviousAttempt(t *testing.T) {
	// The retry prompt must tell the model what the failed attempt did, so
	// "do not repeat steps that already succeeded" is enforceable — otherwise
	// retries redo side effects (double posts, duplicate sends).
	pb := &Playbook{Name: "generic", Dir: t.TempDir()}
	stage := StageFile{Number: 1, Name: "write", Write: "output/result.md"}
	outPath := outputPath(pb, stage)
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("write_file", map[string]any{"path": "/tmp/nowhere.md", "content": "x"}), textBlock("DONE")}, "stop"),
		scriptedResp([]ContentBlock{textBlock("DONE")}, "stop"),
	}}

	_, _, _, _, err := executeStage(
		context.Background(), client, "retry-trail", "", pb, stage,
		"do the work", nil, NewRegistry(), 100, nil, pb.Dir,
	)
	if err == nil || !strings.Contains(err.Error(), "not created or updated") {
		t.Fatalf("error = %v", err)
	}
	// the first retry user message must contain the previous attempt's trail
	retry := ""
scan:
	for _, msgs := range client.messages {
		for _, m := range msgs {
			if m.Role == "user" && strings.Contains(m.Content, "## Retry") {
				retry = m.Content
				break scan
			}
		}
	}
	if !strings.Contains(retry, "previous attempt's actions") || !strings.Contains(retry, "write_file") {
		t.Fatalf("retry prompt missing attempt trail: %.300s", retry)
	}
	if !strings.Contains(retry, outPath) {
		t.Fatalf("retry prompt missing output path: %.300s", retry)
	}
}

func TestWorkspaceRunResumesFirstIncompleteStage(t *testing.T) {
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{
		"01-collect", "02-report",
	})
	settings := &Settings{Home: home, Workspace: home, MaxTokens: 100}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}

	pb, err := loadPlaybookWorkspace(home, "brief")
	if err != nil {
		t.Fatal(err)
	}
	run, err := loadOrCreatePlaybookRun(pb, "make the briefing", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	stage1, _ := workspaceStage(pb, 1)
	stage2, _ := workspaceStage(pb, 2)
	path1 := playbookRunOutputPath(pb, run, stage1, stage1.Outputs[0])
	path2 := playbookRunOutputPath(pb, run, stage2, stage2.Outputs[0])
	// Remove the provisional run so RunPlaybook exercises creation itself with a
	// predictable timestamp-independent state.
	if err := os.RemoveAll(filepath.Join(playbookRunsDir(pb), run.ID)); err != nil {
		t.Fatal(err)
	}

	oldLoop := runPlaybookStageLoop
	defer func() { runPlaybookStageLoop = oldLoop }()
	calls := 0
	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		calls++
		if calls == 1 {
			if err := os.MkdirAll(filepath.Dir(path1), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path1, []byte("collected"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return &LoopResult{Status: "complete", Reply: "done"}
	}
	// The generated run ID is time-based, so point the first execution at the
	// pre-created state by restoring it before invoking the runner.
	if err := savePlaybookRun(pb, run); err != nil {
		t.Fatal(err)
	}
	result, err := RunPlaybook(context.Background(), core, "brief", "make the briefing", "test", nil)
	if err != nil || result.Status != "failed" {
		t.Fatalf("first run = %+v, err=%v", result, err)
	}

	runPlaybookStageLoop = func(_ context.Context, _ LLMClient, _ string, _ string, _ []Message, _ *Registry, _ int, _ int, _ Observer, _ string) *LoopResult {
		if err := os.MkdirAll(filepath.Dir(path2), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path2, []byte("reported"), 0600); err != nil {
			t.Fatal(err)
		}
		return &LoopResult{Status: "complete", Reply: "done"}
	}
	result, err = RunPlaybook(context.Background(), core, "brief", "ignored on resume", "test", nil)
	if err != nil || result.Status != "complete" || result.StagesRun != 1 {
		t.Fatalf("resume = %+v, err=%v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(playbookRunsDir(pb), run.ID, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved PlaybookRun
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Stages[0].Status != "complete" || saved.Stages[0].Attempts != 1 || saved.Stages[1].Status != "complete" || saved.Stages[1].Attempts != 2 {
		t.Fatalf("saved stage state = %+v", saved.Stages)
	}
}

func writeWorkspacePlaybook(t *testing.T, home, name string, stages []string) {
	t.Helper()
	root := filepath.Join(home, "playbooks", name)
	if err := os.MkdirAll(filepath.Join(root, "stages"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CONTEXT.md"), []byte("# Test playbook\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, stage := range stages {
		dir := filepath.Join(root, "stages", stage)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		content := "# " + stage + "\n\n## Inputs\n\n| Source | File/Location | Section/Scope | Why |\n| --- | --- | --- | --- |\n"
		if stage != stages[0] {
			content += "| Previous stage | `../" + stages[0] + "/output/result.md` | Full file | Handoff |\n"
		}
		content += "\n## Process\n\n1. Produce the result.\n\n## Tools\n\n- write_file\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Result | `output/result.md` | Markdown |\n"
		if err := os.WriteFile(filepath.Join(dir, "CONTEXT.md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
