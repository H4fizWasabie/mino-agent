package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePersonaFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	body := "Stance: verify everything.\nMission: ship verified output.\n"

	cases := []struct {
		name    string
		content string
		want    *Agent
		wantErr string
	}{
		{"valid persona",
			"---\nname: trend-researcher\ndescription: daily AI news\n---\n\n" + body,
			&Agent{Name: "trend-researcher", Description: "daily AI news", Body: strings.TrimSpace(body)}, ""},
		{"no frontmatter", "just a body", nil, "no frontmatter"},
		{"missing name", "---\ndescription: x\n---\n\nbody", nil, "invalid frontmatter"},
		{"tools field refused",
			"---\nname: x\ntools:\n  - write_file\n---\n\nbody", nil, "must not declare tools"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePersonaFile(write(tc.name+".md", tc.content))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("parsePersonaFile error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePersonaFile: %v", err)
			}
			if got.Name != tc.want.Name || got.Description != tc.want.Description || got.Body != tc.want.Body {
				t.Fatalf("parsePersonaFile = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestValidatePlaybookPersona(t *testing.T) {
	home := t.TempDir()
	agents := filepath.Join(home, "agents")
	if err := os.MkdirAll(agents, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, "trend-researcher.md"),
		[]byte("---\nname: trend-researcher\n---\n\nStance: verify.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, "mismatch.md"),
		[]byte("---\nname: other-name\n---\n\nStance: verify.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, "oversized.md"),
		[]byte("---\nname: oversized\n---\n\n"+strings.Repeat("x", maxPersonaBodyBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		agent string
		want  string // empty = valid
	}{
		{"no binding refuses nothing", "", ""},
		{"bound persona passes", "trend-researcher", ""},
		{"missing agent refused like unknown tool", "ghost", "persona \"ghost\" unavailable"},
		{"frontmatter name must match binding exactly", "mismatch", "must match the binding"},
		{"oversized body refused with explicit error", "oversized", "over the 2048-byte cap"},
		{"traversal name refused", "../escape", "invalid persona name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePlaybookPersona(home, &PlaybookWorkspace{Name: "news", Agent: tc.agent})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validatePlaybookPersona: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validatePlaybookPersona error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestPlaybookPersonaResolution(t *testing.T) {
	cases := []struct {
		name          string
		workspaceName string
		workspaceBody string
		sharedName    string
		sharedBody    string
		want          string
	}{
		{"workspace persona wins", "instagram-curator", "workspace persona", "instagram-curator", "shared persona", "workspace persona"},
		{"shared roster is the fallback", "", "", "trend-researcher", "legacy persona", "legacy persona"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			shared := filepath.Join(home, "agents")
			playbook := filepath.Join(home, "playbooks", "news")
			if err := os.MkdirAll(shared, 0700); err != nil {
				t.Fatal(err)
			}
			write := func(path, name, body string) {
				t.Helper()
				content := "---\nname: " + name + "\n---\n\n" + body
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.workspaceName != "" {
				personaDir := filepath.Join(playbook, "persona")
				if err := os.MkdirAll(personaDir, 0700); err != nil {
					t.Fatal(err)
				}
				write(filepath.Join(personaDir, tc.workspaceName+".md"), tc.workspaceName, tc.workspaceBody)
			}
			write(filepath.Join(shared, tc.sharedName+".md"), tc.sharedName, tc.sharedBody)

			pb := &PlaybookWorkspace{Name: "news", Dir: playbook, Agent: tc.sharedName}
			if tc.workspaceName != "" {
				pb.Name = "instagram"
				pb.Agent = tc.workspaceName
			}
			got, err := loadPlaybookPersona(home, pb)
			if err != nil {
				t.Fatal(err)
			}
			if got.Body != tc.want {
				t.Fatalf("persona body = %q, want %q", got.Body, tc.want)
			}
		})
	}
}

func TestWorkspacePersonaValidation(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"malformed frontmatter", "not a persona", "no frontmatter"},
		{"binding mismatch", "---\nname: other\n---\n\nbody", "must match the binding"},
		{"tools are stage-owned", "---\nname: curator\ntools:\n  - write_file\n---\n\nbody", "must not declare tools"},
		{"body cap applies", "---\nname: curator\n---\n\n" + strings.Repeat("x", maxPersonaBodyBytes+1), "over the 2048-byte cap"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			playbook := filepath.Join(home, "playbooks", "curation")
			personaDir := filepath.Join(playbook, "persona")
			if err := os.MkdirAll(personaDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(personaDir, "curator.md"), []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			err := validatePlaybookPersona(home, &PlaybookWorkspace{Name: "curation", Dir: playbook, Agent: "curator", agentsPresent: true})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validatePlaybookPersona error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestMigratedWorkspaceRequiresLocalPersona(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "agents"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agents", "curator.md"), []byte("---\nname: curator\n---\n\nshared persona\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err := validatePlaybookPersona(home, &PlaybookWorkspace{Name: "curation", Dir: filepath.Join(home, "playbooks", "curation"), Agent: "curator", agentsPresent: true})
	if err == nil || !strings.Contains(err.Error(), "workspace persona/curator.md is required") {
		t.Fatalf("validatePlaybookPersona error = %v, want missing workspace persona", err)
	}
}

func TestManagedPlaybookRefusesUnknownAgent(t *testing.T) {
	// PSN-001 acceptance: the config.md agent: reference is validated at edit
	// time — a missing persona refuses the playbook like a missing tool.
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	if err := os.WriteFile(filepath.Join(home, "playbooks", "brief", "config.md"),
		[]byte("status: active\nagent: ghost\n"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry}
	if err := validateManagedPlaybook(core, "brief"); err == nil || !strings.Contains(err.Error(), "persona \"ghost\" unavailable") {
		t.Fatalf("validateManagedPlaybook error = %v, want persona refusal", err)
	}
}
