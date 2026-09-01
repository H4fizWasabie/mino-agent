package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptFileNameFor(t *testing.T) {
	if got := scriptFileNameFor("windows"); got != "script.ps1" {
		t.Fatalf("Windows script name = %q", got)
	}
	if got := scriptFileNameFor("darwin"); got != "script.sh" {
		t.Fatalf("macOS script name = %q", got)
	}
}

func TestAINewsFetchAcceptsMarkdownSourceLabel(t *testing.T) {
	home := t.TempDir()
	stage := filepath.Join(home, "stages", "02-fetch")
	if err := os.MkdirAll(filepath.Join(home, "stages", "01-judgment", "output"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stage, 0700); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("playbook_defaults/ai-news-daily/stages/02-fetch/script.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "script.sh"), script, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "stages", "01-judgment", "output", "topics.md"), []byte("## Story\n**Source:** https://example.com/story\nKey claim: verified\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(home, "story.html")
	if err := os.WriteFile(fixture, []byte("<title>Story</title><p>A verified article paragraph with enough text.</p>"), 0600); err != nil {
		t.Fatal(err)
	}
	curl := "#!/bin/bash\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"-o\" ]; then shift; cp \"$FIXTURE\" \"$1\"; fi\n  shift\ndone\n"
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(curl), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "./script.sh")
	cmd.Dir = stage
	cmd.Env = append(os.Environ(), "FIXTURE="+fixture, "PATH="+bin+":"+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fetch script failed: %v\n%s", err, output)
	}
	facts, err := os.ReadFile(filepath.Join(stage, "output", "facts.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(facts), "Status: fetched") {
		t.Fatalf("facts = %q", facts)
	}
}

// playbook_script_test.go — script-backed playbook stages (issue #304,
// PA-007): the harness executes script.sh directly, zero inference; a
// non-zero exit or a missing declared output fails the run loudly; writes
// stay in the playbookWriteGuard's run-scoped zone; every script-stage
// action lands in the audit log (OBS-002); the shared validation seam
// (bash -n + tool scan) runs at edit time.

// writeScriptPlaybook writes a playbook whose stages carry script.sh (and
// optionally CONTEXT.md) per spec.
func writeScriptPlaybook(t *testing.T, home, name string, stages []struct {
	folder, script, context string
}) {
	t.Helper()
	root := filepath.Join(home, "playbooks", name)
	if err := os.MkdirAll(filepath.Join(root, "stages"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CONTEXT.md"), []byte("# "+name+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, st := range stages {
		dir := filepath.Join(root, "stages", st.folder)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if st.script != "" {
			if err := os.WriteFile(filepath.Join(dir, "script.sh"), []byte(st.script), 0700); err != nil {
				t.Fatal(err)
			}
		}
		if st.context != "" {
			if err := os.WriteFile(filepath.Join(dir, "CONTEXT.md"), []byte(st.context), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestScriptStageValidationSeam(t *testing.T) {
	home := t.TempDir()
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry}

	dir := filepath.Join(home, "playbooks", "check", "stages", "01-x")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "script.sh")
	good := "#!/bin/bash\nset -euo pipefail\n# mino exec write_file is a comment, not an invocation\nmkdir -p output\n"
	write := func(content string) {
		if err := os.WriteFile(script, []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Good script passes the shared seam.
	write(good)
	if err := validateScriptFile(core, script, "01-x"); err != nil {
		t.Fatalf("good script rejected: %v", err)
	}
	// Bad bash is rejected.
	write("#!/bin/bash\nif then\n")
	if err := validateScriptFile(core, script, "01-x"); err == nil || !strings.Contains(err.Error(), "bash -n") {
		t.Fatalf("bad bash accepted: %v", err)
	}
	// Undeclared tool references are rejected.
	write("#!/bin/bash\nmino exec read_file output/x\n")
	if err := validateScriptFile(core, script, "01-x"); err == nil || !strings.Contains(err.Error(), "unknown tool(s)") {
		t.Fatalf("unknown tool accepted: %v", err)
	}
	// Edit time: manage_playbook's validator rejects a playbook whose stage
	// script is broken, and accepts it once fixed.
	writeScriptPlaybook(t, home, "editcheck", []struct{ folder, script, context string }{
		{"01-x", "#!/bin/bash\nif then\n", ""},
	})
	if err := validateManagedPlaybook(core, "editcheck"); err == nil || !strings.Contains(err.Error(), "bash -n") {
		t.Fatalf("edit-time validation accepted bad script: %v", err)
	}
	writeScriptPlaybook(t, home, "editcheck", []struct{ folder, script, context string }{
		{"01-x", good, ""},
	})
	if err := validateManagedPlaybook(core, "editcheck"); err != nil {
		t.Fatalf("edit-time validation rejected good script: %v", err)
	}
}
