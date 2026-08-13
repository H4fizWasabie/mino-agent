package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillLoaderMatch(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "skills", "test-skill"), 0700)
	os.WriteFile(filepath.Join(home, "skills", "test-skill", "SKILL.md"), []byte(`---
name: test-skill
description: Run procurement reports and stock analysis
triggers:
  - report
  - stock
  - procurement
  - analysis
---
1. Call planning_summary
2. Generate report
3. Save to reports/`), 0600)

	sl := NewSkillLoader(home)
	hits := sl.Match("generate a procurement report")
	if len(hits) != 1 || hits[0].Name != "test-skill" {
		t.Fatalf("trigger match failed: %d hits", len(hits))
	}
	hits = sl.Match("run stock analysis")
	if len(hits) != 1 {
		t.Fatalf("keyword match failed: %d hits", len(hits))
	}
	hits = sl.Match("what time is it")
	if len(hits) != 0 {
		t.Fatalf("got %d hits for non-matching message", len(hits))
	}
	if sl.skills["test-skill"].UseCount != 2 {
		t.Fatalf("use count = %d, want 2", sl.skills["test-skill"].UseCount)
	}
}

func TestSkillCreateAndLifecycle(t *testing.T) {
	home := t.TempDir()
	sl := NewSkillLoader(home)
	if err := sl.Create("my-report", "Generate weekly reports", []string{"report", "weekly"}, "# Steps\n1. Do it"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "skills", "my-report", "SKILL.md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("SKILL.md not created")
	}
	hits := sl.Match("create the weekly report")
	if len(hits) != 1 || hits[0].Name != "my-report" {
		t.Fatalf("created skill not matched: %d hits", len(hits))
	}
	sl.MarkStale("my-report")
	hits = sl.Match("create the weekly report")
	if len(hits) != 0 {
		t.Fatalf("stale skill still matched: %d hits", len(hits))
	}
}

func TestCodingSkillUsesWorkspaceAndChunkedWrites(t *testing.T) {
	for _, want := range []string{"LOCAL WORKSPACE", "mode=overwrite", "mode=append", "sync back once", "automatically rewrites supported noisy commands through RTK"} {
		if !strings.Contains(codingSkill, want) {
			t.Fatalf("coding skill missing %q", want)
		}
	}
	if strings.Contains(codingSkill, "/home/mino/") {
		t.Fatal("coding skill hardcodes the VPS workspace")
	}
}

func TestPlaybookAuthoringSkillIsEmbedded(t *testing.T) {
	for _, want := range []string{"name: playbook-authoring", "create playbook", "## Structure", "manage_playbook", "Telegram"} {
		if !strings.Contains(playbookAuthoringSkill, want) {
			t.Errorf("playbook authoring skill missing %q", want)
		}
	}
}

func TestSkillLoaderAutoGate(t *testing.T) {
	home := t.TempDir()
	// Invoke-on-demand skill: no `auto` (defaults false).
	os.MkdirAll(filepath.Join(home, "skills", "procura"), 0700)
	os.WriteFile(filepath.Join(home, "skills", "procura", "SKILL.md"), []byte(`---
name: procura
description: Purchase history and stock reports
triggers:
  - purchase history
  - stock report
---
Query the Procura DB.`), 0600)
	// Auto-ambient skill: `auto: true`.
	os.MkdirAll(filepath.Join(home, "skills", "image-gen"), 0700)
	os.WriteFile(filepath.Join(home, "skills", "image-gen", "SKILL.md"), []byte(`---
name: image-gen
description: Generate images for posts
auto: true
triggers:
  - image
---
Generate the image.`), 0600)

	// Explicit trigger loads an on-demand skill.
	sl := NewSkillLoader(home)
	hits := sl.Match("run a purchase history for March")
	if len(hits) != 1 || hits[0].Name != "procura" {
		t.Fatalf("explicit trigger should load on-demand skill, got %d hits", len(hits))
	}

	// Fuzzy word-overlap (no trigger/description) must NOT auto-load an on-demand skill.
	sl2 := NewSkillLoader(home)
	hits = sl2.Match("check the purchase stock levels please")
	if len(hits) != 0 {
		t.Fatalf("on-demand skill auto-loaded on fuzzy overlap (issues #170): %d hits", len(hits))
	}

	// An auto:true skill IS loaded by fuzzy word-overlap.
	sl3 := NewSkillLoader(home)
	hits = sl3.Match("create an image for the post")
	if len(hits) != 1 || hits[0].Name != "image-gen" {
		t.Fatalf("auto skill should fuzzy-load, got %d hits", len(hits))
	}
}
