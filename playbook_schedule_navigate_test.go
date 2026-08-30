package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests for #452: scheduled playbook fires drive Mino's normal loop via a
// synthetic instruction (NavigateScheduledPlaybook) instead of calling
// RunPlaybook's dedicated stage loop directly.

func TestBuildSystemInjectsPlaybookRailsForScheduleSource(t *testing.T) {
	// A scheduled fire has no owner present to notice a run that quietly
	// stopped early — it always needs the same "finish it, no narration"
	// discipline the dedicated loop got from BuildPlaybookSystem's rails.
	home := t.TempDir()
	sess := NewSession(&Settings{Home: home, Workspace: home}, nil)
	system, _ := sess.BuildContext("go", "schedule")
	if !strings.Contains(system, "## Operating Rules (absolute") {
		t.Fatalf("expected playbookRails in a schedule-source system prompt, got %q", system)
	}
}

func TestBuildSystemInjectsPlaybookRailsWhenNavigating(t *testing.T) {
	// A chat turn with an active navigation pointer (#450/#451) is mid
	// playbook work, even though the source is an ordinary channel — it
	// needs the same discipline as a schedule fire.
	home := t.TempDir()
	sess := NewSession(&Settings{Home: home, Workspace: home}, nil)

	system, _ := sess.BuildContext("hi", "telegram")
	if strings.Contains(system, "## Operating Rules (absolute") {
		t.Fatalf("no active navigation: rails must not be injected, got %q", system)
	}

	setSessionNav("default", "brief", "run-1")
	defer clearSessionNav("default")
	system, _ = sess.BuildContext("hi", "telegram")
	if !strings.Contains(system, "## Operating Rules (absolute") {
		t.Fatalf("active navigation: expected playbookRails, got %q", system)
	}
}

// testProviderCore builds a Core wired to a scripted HTTP provider, the same
// pattern app_test.go's TestContextBudgetBlockInTurnTail uses, so
// RespondForContext can run end to end without a live model.
func testProviderCore(t *testing.T, reply string) *Core {
	t.Helper()
	home := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"` + reply + `"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MINO_TEST_KEY_"+t.Name(), "k")
	keyEnv := "MINO_TEST_KEY_" + t.Name()
	os.WriteFile(filepath.Join(home, "providers.json"), []byte(`{"providers":[{"name":"t","priority":1,"base_url":"`+srv.URL+`","api_key_env":"`+keyEnv+`","model":"test-model"}]}`), 0600)
	settings := &Settings{Home: home, Workspace: home, ContextChars: 20000, MaxTokens: 100, MaxIter: 5}
	pm, err := NewProviderManager(home, settings, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Core{Settings: settings, Client: pm, Sessions: NewSessionManager(settings, nil), Tools: NewRegistry()}
}

func TestRespondForContextSkipsAddExchangeForSchedule(t *testing.T) {
	core := testProviderCore(t, "done")

	core.RespondForContext(context.Background(), "scheduled-brief", "go", "schedule", nil, false)
	sess := core.Sessions.Get("scheduled-brief")
	if len(sess.Session.history) != 0 {
		t.Fatalf("expected no history for a schedule-source turn, got %d entries", len(sess.Session.history))
	}

	core.RespondForContext(context.Background(), "tg:1", "go", "telegram", nil, false)
	sess2 := core.Sessions.Get("tg:1")
	if len(sess2.Session.history) == 0 {
		t.Fatal("expected a telegram-source turn to record history as before")
	}
}

func TestNavigateScheduledPlaybookMapsRunStatus(t *testing.T) {
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	settings := &Settings{Home: home, Workspace: home}
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	core := &Core{Settings: settings, Tools: registry, Sessions: NewSessionManager(settings, nil)}

	pb, err := loadPlaybookWorkspace(home, "brief")
	if err != nil {
		t.Fatal(err)
	}

	oldSeam := respondForScheduledPlaybook
	defer func() { respondForScheduledPlaybook = oldSeam }()
	respondForScheduledPlaybook = func(core *Core, ctx context.Context, sessionID, instruction string, obs Observer) *LoopResult {
		return &LoopResult{Status: "complete", Reply: "turn done"}
	}

	cases := []struct {
		runStatus  string
		wantStatus string
	}{
		{"complete", "complete"},
		{"failed", "failed"},
		{"interrupted", "interrupted"},
		{"running", "iteration_limit"},
	}
	for _, c := range cases {
		t.Run(c.runStatus, func(t *testing.T) {
			run, err := loadOrCreatePlaybookRun(pb, registry, "go", "scheduled-brief", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			run.Status = c.runStatus
			if err := savePlaybookRun(pb, run); err != nil {
				t.Fatal(err)
			}
			result, err := NavigateScheduledPlaybook(context.Background(), core, "brief", "Scheduled run", "scheduled-brief", nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != c.wantStatus {
				t.Fatalf("run.Status=%s: got PlaybookResult.Status=%s, want %s", c.runStatus, result.Status, c.wantStatus)
			}
		})
	}
}

func TestNavigateScheduledPlaybookNoRunIsFailure(t *testing.T) {
	// The model never called run_playbook this fire (or the workspace is
	// otherwise untouched) — there is no run record to report, so this
	// counts as a failure like a pre-run validation error would.
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	settings := &Settings{Home: home, Workspace: home}
	core := &Core{Settings: settings, Tools: NewRegistry(), Sessions: NewSessionManager(settings, nil)}

	oldSeam := respondForScheduledPlaybook
	defer func() { respondForScheduledPlaybook = oldSeam }()
	respondForScheduledPlaybook = func(core *Core, ctx context.Context, sessionID, instruction string, obs Observer) *LoopResult {
		return &LoopResult{Status: "complete", Reply: "did something unrelated"}
	}
	result, err := NavigateScheduledPlaybook(context.Background(), core, "brief", "Scheduled run", "scheduled-brief", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" {
		t.Fatalf("expected failed with no run on disk, got %+v", result)
	}
}
