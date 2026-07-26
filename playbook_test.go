package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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

func TestBuildPlaybookSystemOmitsNestedPlaybookRouting(t *testing.T) {
	settings := &Settings{Home: t.TempDir(), Workspace: t.TempDir(), ContextChars: 10000}
	session := NewSession(settings, nil)
	got := session.BuildPlaybookSystem("run the procurement audit", "")
	if strings.Contains(got, "PLAYBOOK AVAILABLE") {
		t.Fatalf("playbook system recursively advertises routing: %q", got)
	}
}

func TestPlaybookCircuitBreakerStopsRepeatedIdenticalCall(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "input.txt")
	if err := os.WriteFile(path, []byte("evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	registry.Register(makeReadTool())
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock("read_file", map[string]any{"path": path})}, "tool_use"),
		scriptedResp([]ContentBlock{toolBlock("read_file", map[string]any{"path": path})}, "tool_use"),
		scriptedResp([]ContentBlock{toolBlock("read_file", map[string]any{"path": path})}, "tool_use"),
	}}

	calls, _, _, done := runToolLoop(context.Background(), client, "test-session", nil, registry, 100, nil, home, 1)
	if !strings.HasPrefix(done, "BLOCKED: repeated identical call") {
		t.Fatalf("done = %q, want circuit breaker", done)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want first execution plus one cached result", len(calls))
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
