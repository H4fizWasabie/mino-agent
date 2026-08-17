package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSeedDefaultPlaybooksIdempotent verifies task-232 seeding: a fresh home
// gets all embedded defaults, and a second seed never overwrites the owner's
// edits (idempotency is the ticket's hard constraint).
func TestSeedDefaultPlaybooksIdempotent(t *testing.T) {
	home := t.TempDir()
	if err := SeedDefaultPlaybooks(home); err != nil {
		t.Fatal(err)
	}
	for _, pb := range []string{"ai-news-daily", "morning-briefing", "weekly-cost", "weekly-audit", "shared"} {
		if _, err := os.Stat(filepath.Join(home, "playbooks", pb)); err != nil {
			t.Fatalf("default playbook %s not seeded: %v", pb, err)
		}
	}
	if got := ListPlaybooks(home); len(got) < 4 {
		t.Fatalf("ListPlaybooks after seed = %v, want >= 4 defaults", got)
	}

	// Owner edits a seeded playbook; a second seed must leave the edit alone.
	ownerFile := filepath.Join(home, "playbooks", "weekly-cost", "CONTEXT.md")
	if err := os.WriteFile(ownerFile, []byte("OWNER EDIT"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SeedDefaultPlaybooks(home); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ownerFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "OWNER EDIT" {
		t.Fatalf("second seed overwrote owner edit: %q", string(data))
	}
}

// TestSeedDefaultPlaybooksRefusesToOverwritePlaybookDir guards the other side
// of idempotency: a playbook directory created by the owner before seeding is
// left intact (files are seeded only when absent).
func TestSeedDefaultPlaybooksNoOverwriteExistingDir(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "playbooks", "weekly-cost")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.md"), []byte("description: owner-made\nstatus: active\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SeedDefaultPlaybooks(home); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.md"))
	if !strings.Contains(string(data), "owner-made") {
		t.Fatalf("existing playbook overwritten by seed: %q", string(data))
	}
}

// TestDefaultPlaybooksSanitized enforces the ticket's sanitization constraint:
// embedded defaults must never leak owner-specific data (recipient names,
// absolute home paths) — the same discipline TestChangelogPublicDiscipline
// enforces for public docs.
func TestDefaultPlaybooksSanitized(t *testing.T) {
	banned := []string{"Abah", "/home/mino", "to=Abah"}
	err := fs.WalkDir(defaultPlaybooks, "playbook_defaults", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := defaultPlaybooks.ReadFile(path)
		if err != nil {
			return err
		}
		for _, b := range banned {
			if strings.Contains(string(data), b) {
				t.Fatalf("default %s contains banned owner data %q", path, b)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestSeededDefaultsValidate ensures every seeded default playbook passes the
// same edit-time validation real playbooks must pass (declared tools known,
// write_file required, no human checkpoints).
func TestSeededDefaultsValidate(t *testing.T) {
	home := t.TempDir()
	if err := SeedDefaultPlaybooks(home); err != nil {
		t.Fatal(err)
	}
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	for _, name := range []string{"write_file", "send_message", "fetch_url", "bash", "search_web", "list_reminders", "manage_memory", "read_file"} {
		registry.Register(&Tool{Name: name})
	}
	core := &Core{Settings: settings, Tools: registry}
	for _, pb := range []string{"ai-news-daily", "morning-briefing", "weekly-cost", "weekly-audit"} {
		if err := validateManagedPlaybook(core, pb); err != nil {
			t.Fatalf("seeded default %s fails validation: %v", pb, err)
		}
	}
}
