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

// Tests for #477: a turn already known to be navigating a playbook gets the
// narrow, ICM-scoped system prompt (BuildPlaybookSystem) instead of the full
// general-chat system prompt (buildSystem) and its skill/owner-fact-matching
// overhead — restoring the context isolation the dedicated stage loop always
// had, which #450/#451/#452's unified-loop navigation dropped.

func capturingProviderCore(t *testing.T, home string, bodies *[]string) *Core {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*bodies = append(*bodies, string(body))
		w.Write([]byte(`{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	t.Cleanup(srv.Close)
	keyEnv := "MINO_TEST_KEY_" + t.Name()
	t.Setenv(keyEnv, "k")
	os.WriteFile(filepath.Join(home, "providers.json"), []byte(`{"providers":[{"name":"t","priority":1,"base_url":"`+srv.URL+`","api_key_env":"`+keyEnv+`","model":"test-model"}]}`), 0600)
	settings := &Settings{Home: home, Workspace: home, ContextChars: 20000, MaxTokens: 100, MaxIter: 5}
	pm, err := NewProviderManager(home, settings, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Core{Settings: settings, Client: pm, Sessions: NewSessionManager(settings, nil), Tools: NewRegistry()}
}

func lastRequestSystem(t *testing.T, bodies []string) string {
	t.Helper()
	if len(bodies) == 0 {
		t.Fatal("no provider request captured")
	}
	var p struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(bodies[len(bodies)-1]), &p); err != nil {
		t.Fatalf("parse request: %v", err)
	}
	for _, m := range p.Messages {
		if m.Role == "system" {
			return m.Content
		}
	}
	t.Fatal("no system message in request")
	return ""
}

func TestScheduledFireUsesICMScopedSystemPrompt(t *testing.T) {
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	var bodies []string
	core := capturingProviderCore(t, home, &bodies)

	core.RespondForContext(context.Background(), "scheduled-brief", "go", "schedule", nil, false)
	system := lastRequestSystem(t, bodies)

	if !strings.Contains(system, "Operating Rules (absolute") {
		t.Fatalf("expected playbookRails in the scheduled fire's system prompt, got %q", system)
	}
	if strings.Contains(system, "Tool preference: prefer the purpose-built tool") {
		t.Fatalf("scheduled fire must not carry the general-chat system prompt, got %q", system)
	}
	if strings.Contains(system, "Iteration discipline (issues #171)") {
		t.Fatalf("scheduled fire must not carry general-chat iteration-discipline text, got %q", system)
	}
}

func TestOrdinaryChatUsesGeneralSystemPrompt(t *testing.T) {
	home := t.TempDir()
	var bodies []string
	core := capturingProviderCore(t, home, &bodies)

	core.RespondForContext(context.Background(), "tg:1", "hello", "telegram", nil, false)
	system := lastRequestSystem(t, bodies)

	if !strings.Contains(system, "Tool preference: prefer the purpose-built tool") {
		t.Fatalf("ordinary chat must still get the general-chat system prompt, got %q", system)
	}
	if strings.Contains(system, "Operating Rules (absolute") {
		t.Fatalf("ordinary chat with no active navigation must not carry playbookRails, got %q", system)
	}
}

func TestChatContinuationOfNavigationUsesICMScopedSystemPrompt(t *testing.T) {
	home := t.TempDir()
	writeWorkspacePlaybook(t, home, "brief", []string{"01-collect"})
	var bodies []string
	core := capturingProviderCore(t, home, &bodies)

	// Simulate an earlier message in this session already having started
	// navigating "brief" (sessionNav set, as navigatePlaybookRun does).
	setSessionNav("tg:2", "brief", "run-1")
	defer clearSessionNav("tg:2")

	core.RespondForContext(context.Background(), "tg:2", "continue", "telegram", nil, false)
	system := lastRequestSystem(t, bodies)

	if !strings.Contains(system, "Operating Rules (absolute") {
		t.Fatalf("a chat turn continuing an active navigation must get playbookRails, got %q", system)
	}
	if strings.Contains(system, "Tool preference: prefer the purpose-built tool") {
		t.Fatalf("a chat turn continuing an active navigation must not carry the general-chat prompt, got %q", system)
	}
}
