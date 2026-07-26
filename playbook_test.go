package main

import (
	"context"
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
	dir := filepath.Join(home, "playbooks", "procurement-audit")
	if err := os.MkdirAll(filepath.Join(dir, "output"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.md"), []byte("description: Weekly procurement audit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "01-fetch.md"), []byte("## Do\n1. Fetch data\n## Write\n`output/data.md`\n"), 0600); err != nil {
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
	playbooksDir := filepath.Join(tmp, "playbooks")

	dir := filepath.Join(playbooksDir, "procurement-audit")
	os.MkdirAll(filepath.Join(dir, "output"), 0700)
	os.WriteFile(filepath.Join(dir, "config.md"), []byte(`description: Weekly procurement audit — fetch POs and analyze
`), 0644)
	os.WriteFile(filepath.Join(dir, "01-fetch.md"), []byte(`# Fetch
## Do
1. Query
## Write
`+"`output/01-data.md`"+`
`), 0644)

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
	playbooksDir := filepath.Join(tmp, "playbooks")

	for _, name := range []string{"alpha", "beta", "gamma"} {
		dir := filepath.Join(playbooksDir, name)
		os.MkdirAll(filepath.Join(dir, "output"), 0700)
		os.WriteFile(filepath.Join(dir, "01-test.md"), []byte("## Do\n1. thing\n"), 0644)
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
	dir := filepath.Join(home, "playbooks", "procurement-audit")
	if err := os.MkdirAll(filepath.Join(dir, "output"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.md"), []byte("description: Weekly audit\nschedule: Mon 09:00 Asia/Kuala_Lumpur\nstatus: active\nnotify: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "01-fetch.md"), []byte("## Tools\n- read_file\n## Write\n`output/audit.md`\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "output", "audit.md"), []byte("done"), 0600); err != nil {
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
	if !ok || len(stages) != 1 || stages[0]["write"] != "output/audit.md" {
		t.Fatalf("catalog stages = %#v", item["stages"])
	}
	outputs, ok := item["outputs"].([]string)
	if !ok || len(outputs) != 1 || outputs[0] != filepath.Join("playbooks", "procurement-audit", "output", "audit.md") {
		t.Fatalf("catalog outputs = %#v", item["outputs"])
	}
}

func TestSchedulePlaybook(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "playbooks", "news")
	if err := os.MkdirAll(filepath.Join(dir, "output"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.md"), []byte("description: News\nstatus: active\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "01-news.md"), []byte("## Do\n1. Search\n## Write\n`output/news.md`\n"), 0600); err != nil {
		t.Fatal(err)
	}

	got := makeSchedulePlaybookTool(home, "Asia/Kuala_Lumpur").ContextFn(nil, map[string]any{"name": "news", "time": "20:00"})
	if !strings.Contains(got, "Scheduled news daily at 20:00") {
		t.Fatalf("schedule result = %q", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.md"))
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	for _, want := range []string{"schedule: 20:00 Asia/Kuala_Lumpur", "notify: true", "status: active"} {
		if !strings.Contains(config, want) {
			t.Errorf("config missing %q: %s", want, config)
		}
	}
}
