package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		{"universe", "/api/universe", "/api/universe"},
		{"universe projection", "/api/universe/projection", "/api/universe/projection"},
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
	knownMissing := map[string]string{}
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

func TestDashboardArtifactActionsHaveRecoveryUI(t *testing.T) {
	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"/api/reveal?action=inspect",
		"artifactNotice(`${label} opened in Files`)",
		"copyArtifactPath",
		`document.execCommand("copy")`,
		"action=download",
		`reveal("memories","memories/")`,
		"allow pop-ups to open it",
	} {
		if !strings.Contains(string(script), marker) {
			t.Errorf("artifact action contract missing %q", marker)
		}
	}
	for _, forbidden := range []string{`reveal("state.db"`, `reveal("providers.json"`, `reveal("","open folder")`} {
		if strings.Contains(string(script), forbidden) {
			t.Errorf("sensitive artifact affordance still present: %q", forbidden)
		}
	}
}

func TestDashboardVoiceAffordanceRemoved(t *testing.T) {
	script, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	index, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"/api/voice", "getUserMedia", "wireMic", `id="mic"`} {
		if strings.Contains(string(script), marker) || strings.Contains(string(index), marker) {
			t.Errorf("unsupported voice affordance remains: %q", marker)
		}
	}
}

func TestDashboardArtifactActionContract(t *testing.T) {
	home := t.TempDir()
	playbooks := filepath.Join(home, "playbooks")
	memories := filepath.Join(home, "semantic-memory")
	if err := os.MkdirAll(playbooks, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(memories, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "SOUL.md"), []byte("owner preferences"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "providers.json"), []byte(`{"api_key":"secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}

	previous := dashCore
	dashCore = &Core{Settings: &Settings{Home: home, MemoriesDir: memories}}
	defer func() { dashCore = previous }()

	tests := []struct {
		name, path, action string
		status             int
		ok                 bool
		kind               string
	}{
		{name: "relative file", path: "SOUL.md", action: "inspect", status: http.StatusOK, ok: true, kind: "file"},
		{name: "relative directory", path: "playbooks", action: "inspect", status: http.StatusOK, ok: true, kind: "directory"},
		{name: "download directory", path: "playbooks", action: "download", status: http.StatusBadRequest},
		{name: "missing target", path: "playbooks/missing.md", action: "inspect", status: http.StatusNotFound},
		{name: "private config", path: "providers.json", action: "inspect", status: http.StatusForbidden},
		{name: "outside home", path: outside, action: "inspect", status: http.StatusForbidden},
		{name: "unsupported action", path: "SOUL.md", action: "launch", status: http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/reveal?path="+url.QueryEscape(tc.path)+"&action="+url.QueryEscape(tc.action), nil)
			rr := httptest.NewRecorder()
			handleRevealAPI(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.status, rr.Body.String())
			}
			if tc.status != http.StatusOK {
				return
			}
			var got struct {
				OK   bool   `json:"ok"`
				Kind string `json:"kind"`
			}
			if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got.OK != tc.ok || got.Kind != tc.kind {
				t.Fatalf("response = %#v, want ok=%t kind=%q", got, tc.ok, tc.kind)
			}
		})
	}
}

func TestDashboardFilesAuthorizesMinoRoots(t *testing.T) {
	home := t.TempDir()
	results := filepath.Join(home, "playbooks", "results")
	if err := os.MkdirAll(results, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(results, "report.txt"), []byte("verified output"), 0600); err != nil {
		t.Fatal(err)
	}
	unsafe := filepath.Join(results, "preview.html")
	if err := os.WriteFile(unsafe, []byte(`<svg><script>window.pwned=true</script></svg>`), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}

	previous := dashCore
	dashCore = &Core{Settings: &Settings{Home: home}}
	defer func() { dashCore = previous }()

	tests := []struct {
		name, path string
		status     int
		body       string
	}{
		{name: "valid file", path: filepath.Join(results, "report.txt"), status: http.StatusOK, body: "verified output"},
		{name: "valid directory", path: results, status: http.StatusOK},
		{name: "missing target", path: filepath.Join(results, "missing.txt"), status: http.StatusNotFound},
		{name: "disallowed target", path: outside, status: http.StatusForbidden},
		{name: "unsupported action", path: filepath.Join(results, "report.txt"), status: http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action := ""
			if tc.name == "unsupported action" {
				action = "&action=launch"
			}
			req := httptest.NewRequest(http.MethodGet, "/api/files?path="+url.QueryEscape(tc.path)+action, nil)
			rr := httptest.NewRecorder()
			handleFilesAPI(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.status, rr.Body.String())
			}
			if tc.body != "" && rr.Body.String() != tc.body {
				t.Fatalf("body = %q, want %q", rr.Body.String(), tc.body)
			}
		})
	}
	request := httptest.NewRequest(http.MethodGet, "/api/files?path="+url.QueryEscape(unsafe), nil)
	response := httptest.NewRecorder()
	handleFilesAPI(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe artifact response = status %d, content-type %q, nosniff %q", response.Code, response.Header().Get("Content-Type"), response.Header().Get("X-Content-Type-Options"))
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

func TestMedianP90(t *testing.T) {
	for _, tc := range []struct {
		name       string
		vals       []int
		wantMedian int
		wantP90    int
	}{
		{"empty", nil, 0, 0},
		{"single", []int{7}, 7, 7},
		{"even upper median", []int{1, 2, 3, 4}, 3, 4},
		{"ten upper median", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 6, 10},
		{"skewed clamps", []int{5, 5, 5, 5}, 5, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			median, p90 := medianP90(tc.vals)
			if median != tc.wantMedian || p90 != tc.wantP90 {
				t.Fatalf("medianP90(%v) = %d,%d want %d,%d", tc.vals, median, p90, tc.wantMedian, tc.wantP90)
			}
		})
	}
}

func TestTraceLLMInputsParsesIterationTokens(t *testing.T) {
	home := t.TempDir()
	traceDir := filepath.Join(home, "traces")
	if err := os.MkdirAll(traceDir, 0700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"turn_start","msg_chars":100}`,
		`{"type":"llm","iteration":1,"in":32346,"out":215}`,
		`{"type":"gate","decision":"skip"}`,
		`{"type":"llm","iteration":2,"in":33304,"out":637}`,
		`{"type":"llm","iteration":1,"in":0,"out":0}`,
	}
	if err := os.WriteFile(filepath.Join(traceDir, traceFileName(home)), []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	got := traceLLMInputs(home)
	if len(got) != 2 || got[0] != 32346 || got[1] != 33304 {
		t.Fatalf("traceLLMInputs = %v, want [32346 33304]", got)
	}
}

// issue #188: a bind conflict must surface as an error, not a silent exit —
// the wrapper returns ListenAndServe's error so RunDashboard can fail loud.
func TestServeDashboardReportsBindConflict(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := serveDashboard(l.Addr().String()); err == nil {
		t.Fatal("bind conflict on an occupied port must return an error")
	}
}

func TestQueryAPIZeroRowsReturnsArrayNotNull(t *testing.T) {
	home := t.TempDir()
	previous := dashCore
	dashCore = &Core{DB: Connect(home)}
	defer func() { dashCore = previous }()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/query", strings.NewReader(`{"sql":"SELECT * FROM chat_log WHERE 0"}`))
	handleQueryAPI(rr, req)

	var resp struct {
		Rows []any `json:"rows"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Rows == nil {
		t.Fatal("rows must be [] not null so the dashboard can read r.rows.length")
	}
}
