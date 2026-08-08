package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDashboardHTTPRouteContract(t *testing.T) {
	mux := http.NewServeMux()
	registerDashboardRoutes(mux, t.TempDir())

	for _, tc := range []struct {
		name, path, pattern string
	}{
		{"root", "/", "/"},
		{"static asset", "/static/app.js", "/static/"},
		{"memory file", "/memories/fact.md", "/memories/"},
		{"streaming chat", "/api/chat/stream", "/api/chat/stream"},
		{"chat", "/api/chat", "/api/chat"},
		{"session", "/api/session", "/api/session"},
		{"memory", "/api/memory", "/api/memory"},
		{"query", "/api/query", "/api/query"},
		{"events", "/api/events", "/api/events"},
		{"nerves", "/api/nerves", "/api/nerves"},
		{"data", "/api/data", "/api/data"},
		{"responsibilities", "/api/responsibilities", "/api/responsibilities"},
		{"responsibility evidence", "/api/responsibility-evidence", "/api/responsibility-evidence"},
		{"reveal", "/api/reveal", "/api/reveal"},
		{"files", "/api/files", "/api/files"},
		{"active tasks", "/api/active-tasks", "/api/active-tasks"},
		{"settings", "/api/settings", "/api/settings"},
		{"oauth callback", "/callback", "/callback"},
		{"auth", "/api/auth", "/api/auth"},
		{"switch", "/api/switch", "/api/switch"},
		{"providers", "/api/providers", "/api/providers"},
		{"oauth providers", "/api/oauth/providers", "/api/oauth/providers"},
		{"oauth login", "/api/oauth/login/codex", "/api/oauth/login/"},
		{"oauth device", "/api/oauth/device/codex", "/api/oauth/device/"},
		{"health", "/health", "/health"},
		{"metrics", "/metrics", "/metrics"},
		{"evaluation", "/api/eval/thumbs-up", "/api/eval/thumbs-up"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tc.path, nil)
			_, pattern := mux.Handler(request)
			if pattern != tc.pattern {
				t.Fatalf("route %q matched %q, want %q", tc.path, pattern, tc.pattern)
			}
		})
	}
}

func TestDashboardFrontendEndpointsHaveRegisteredHandlers(t *testing.T) {
	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerDashboardRoutes(mux, t.TempDir())

	endpointPattern := regexp.MustCompile(`"/api/[A-Za-z0-9/_-]+`)
	knownMissing := map[string]string{
		"/api/voice": "tracked by #32",
	}
	seen := make(map[string]bool)
	for _, match := range endpointPattern.FindAllString(string(script), -1) {
		path := strings.TrimPrefix(match, `"`)
		if seen[path] {
			continue
		}
		seen[path] = true
		request := httptest.NewRequest(http.MethodGet, path, nil)
		_, pattern := mux.Handler(request)
		if pattern == "/" {
			if knownMissing[path] == "" {
				t.Errorf("frontend endpoint %q has no registered handler", path)
			}
			continue
		}
		if reason := knownMissing[path]; reason != "" {
			t.Errorf("frontend endpoint %q is now registered; remove known-missing allowance (%s)", path, reason)
		}
	}
	for path, reason := range knownMissing {
		if !seen[path] {
			t.Errorf("known-missing endpoint %q (%s) is no longer referenced; remove the allowance", path, reason)
		}
	}
}

func TestMergeEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mino.env")
	os.WriteFile(path, []byte("MINO_HOME=/home/mino\nCLOUDFLARE_API_TOKEN=keepme\nMINO_API_KEY=old\n"), 0600)
	// unrelated keys survive; empty values don't wipe existing keys
	err := mergeEnvFile(path, map[string]string{
		"MINO_API_KEY":       "new",
		"TELEGRAM_BOT_TOKEN": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	want := "CLOUDFLARE_API_TOKEN=keepme\nMINO_API_KEY=new\nMINO_HOME=/home/mino\n"
	if string(data) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", data, want)
	}
}
