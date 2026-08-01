package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAwaitInterruptWaitsForReplyBeforeReturning(t *testing.T) {
	got, ok := awaitInterrupt(context.Background(), func(reply func(string)) {
		time.Sleep(time.Millisecond)
		reply("status ready")
	})
	if !ok || got != "status ready" {
		t.Fatalf("awaitInterrupt() = %q, %v", got, ok)
	}
}

func TestDashboardDataIncludesSoul(t *testing.T) {
	home := t.TempDir()
	want := "# Mino\n\nBe curious, calm, and direct.\n"
	if err := os.WriteFile(filepath.Join(home, "SOUL.md"), []byte(want), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "mcp.d"), 0700); err != nil {
		t.Fatal(err)
	}
	db := Connect(home)
	defer db.Close()
	if _, err := db.Exec("INSERT INTO memory_embeddings (source, content, embedding) VALUES (?, ?, ?)", "fact", "demo", strings.Repeat("x", 900)); err != nil {
		t.Fatal(err)
	}

	previous := dashCore
	dashCore = &Core{
		Settings: &Settings{Home: home, Provider: "test", Model: "test", ConsolidateEvery: 6},
		DB:       db,
		Tools:    NewRegistry(),
	}
	defer func() { dashCore = previous }()

	recorder := httptest.NewRecorder()
	handleDataAPI(recorder, httptest.NewRequest(http.MethodGet, "/api/data", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
	var response struct {
		Soul string `json:"soul"`
		DB   struct {
			Path   string `json:"path"`
			Tables []struct {
				Name    string           `json:"name"`
				Columns []string         `json:"columns"`
				Sample  []map[string]any `json:"sample"`
			} `json:"tables"`
		} `json:"db"`
		ActiveTasks []map[string]any `json:"active_tasks"`
		Tools       struct {
			MCP struct {
				Servers []string `json:"servers"`
			} `json:"mcp"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Soul != want {
		t.Fatalf("SOUL.md mismatch: got %q want %q", response.Soul, want)
	}
	if response.DB.Path != filepath.Join(home, "state.db") || len(response.DB.Tables) == 0 {
		t.Fatalf("database metadata missing: %#v", response.DB)
	}
	for _, table := range response.DB.Tables {
		if table.Sample == nil {
			t.Fatalf("database sample for %s must be an empty array, not null", table.Name)
		}
		if table.Name == "memory_embeddings" && len(table.Sample) == 1 && len(table.Sample[0]["embedding"].(string)) > 503 {
			t.Fatal("database sample returned an unbounded embedding")
		}
	}
	if response.ActiveTasks == nil {
		t.Fatal("active_tasks must be an empty array, not null")
	}
	if response.Tools.MCP.Servers == nil {
		t.Fatal("MCP servers must be an empty array, not null")
	}
}

func TestConsolidationEdgesRequireCandidatesConfidenceAndSpecificRelations(t *testing.T) {
	m := &Memory{}
	edges := m.validInferredEdges([]Edge{
		{Target: "keep", Rel: "depends_on", Confidence: 0.9},
		{Target: "weak", Rel: "depends_on", Confidence: 0.84},
		{Target: "generic", Rel: "related_to", Confidence: 0.99},
		{Target: "missing", Rel: "requires", Confidence: 0.99},
		{Target: "keep", Rel: "depends_on", Confidence: 0.95},
		{Target: "conflict", Rel: "depends_on", Confidence: 0.9},
		{Target: "conflict", Rel: "supersedes", Confidence: 0.9},
	}, map[string]bool{"keep": true, "conflict": true}, "consolidation")
	if len(edges) != 1 || edges[0].Target != "keep" || edges[0].Kind != "inferred" || edges[0].Source != "consolidation" {
		t.Fatalf("validated edges = %+v", edges)
	}
}

func TestConsolidationResponseRejectsMalformedOutput(t *testing.T) {
	if _, err := parseConsolidationResponse("not json"); err == nil {
		t.Fatal("malformed response was accepted")
	}
	got, err := parseConsolidationResponse(`{"facts":[{"id":"claim","subject":"A claim","edges":[]}],"episode":"An episode"}`)
	if err != nil || len(got.Facts) != 1 || got.Episode != "An episode" {
		t.Fatalf("parsed response = %+v, err=%v", got, err)
	}
	for _, response := range []string{
		"Here is the result:\n```json\n{\"facts\":[],\"episode\":\"An episode\"}\n```",
		"reasoning {not JSON} final: {\"facts\":[],\"episode\":\"An episode\"}",
	} {
		if _, err := parseConsolidationResponse(response); err != nil {
			t.Fatalf("wrapped response rejected: %q: %v", response, err)
		}
	}
}

func TestConsolidationUsesFakeProviderResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"facts\":[{\"id\":\"fake_claim\",\"subject\":\"A fake-provider claim\",\"content\":\"Tested\",\"edges\":[]}],\"episode\":\"A fake-provider episode\"}"}}]}`)
	}))
	defer server.Close()
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	if _, err := db.Exec("INSERT INTO chat_log (role, content, session_id) VALUES ('user', 'remember this', 'fake-session'), ('assistant', 'okay', 'fake-session')"); err != nil {
		t.Fatal(err)
	}
	pm := &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, BaseURL: server.URL, Model: "main", Small: "small"}},
		clients:   map[string]*Client{"fake": NewClient("test-key", server.URL)},
		state:     map[string]*providerState{"fake": {}}, sticky: map[string]string{}, preferred: map[string]providerPreference{},
		sleep: func(time.Duration) {}, now: time.Now,
	}
	mem := &Memory{db: db, client: pm, cfg: &Settings{Home: home, MemoriesDir: filepath.Join(home, "memories"), ConsolidateEvery: 1}, graph: NewGraphMemory(filepath.Join(home, "memories"), nil)}
	if got := mem.ConsolidateDue(); got != 1 {
		t.Fatalf("consolidated facts = %d", got)
	}
	if fact, ok := mem.graph.FindFact("fake_claim"); !ok || fact.Body != "Tested" {
		t.Fatalf("fake-provider fact = %+v, found=%v", fact, ok)
	}
}

func TestGraphRebuildDoesNotEraseEdgesOnEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"edges\":[]}"}}]}`)
	}))
	defer server.Close()
	pm := &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, BaseURL: server.URL, Model: "main", Small: "small"}},
		clients:   map[string]*Client{"fake": NewClient("test-key", server.URL)},
		state:     map[string]*providerState{"fake": {}}, sticky: map[string]string{}, preferred: map[string]providerPreference{},
		sleep: func(time.Duration) {}, now: time.Now,
	}
	dir := t.TempDir()
	gm := NewGraphMemory(filepath.Join(dir, "memories"), nil)
	if err := gm.RecordFact(Fact{ID: "target", Type: "semantic", Subject: "Target"}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "source", Type: "semantic", Subject: "Source", Body: "Source", Edges: []Edge{{Target: "target", Rel: "depends_on", Kind: "inferred", Confidence: 0.95, Source: "consolidation"}}}); err != nil {
		t.Fatal(err)
	}
	m := &Memory{
		client: pm,
		graph:  gm,
		embedder: &EmbeddingStore{docs: []embeddedDoc{
			{Source: "fact:source", Embedding: []float32{1, 0}},
			{Source: "fact:target", Embedding: []float32{0.9, 0.1}},
		}},
	}
	if _, err := m.RebuildGraphEdges(); err == nil {
		t.Fatal("empty rebuild response was accepted")
	}
	fact, ok := gm.FindFact("source")
	if !ok || len(fact.Edges) != 1 || fact.Edges[0].Target != "target" {
		t.Fatalf("existing edge was erased: %+v", fact)
	}
}

func TestToolOutputStatus(t *testing.T) {
	tests := []struct {
		name, output, want string
	}{
		{"success", "3 results found", "ok"},
		{"builtin error", "Error reading /tmp/missing: not found", "error"},
		{"extension error", "Extension error: context deadline exceeded", "error"},
		{"failed operation", "Failed to create skill: permission denied", "error"},
		{"search failure", "Search failed: timeout", "error"},
		{"mcp failure", "MCP call files_read failed: EOF", "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolOutputStatus(tt.output); got != tt.want {
				t.Fatalf("toolOutputStatus(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

func TestDashboardToolStatusUsesRuntimeOutput(t *testing.T) {
	tools := dashboardTools([]ToolCall{
		{Name: "read_file", Output: "contents"},
		{Name: "edit_file", Output: "Error: old text not found"},
	})
	if tools[0]["status"] != "ok" || tools[1]["status"] != "error" {
		t.Fatalf("dashboard statuses = %#v", tools)
	}
}

func TestStreamedToolCallsKeepProviderOrder(t *testing.T) {
	openAI := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"second","arguments":"{}"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"first","arguments":"{}"}}]}}]}`,
		`data: [DONE]`,
	}, "\n")
	anthropic := strings.Join([]string{
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"b","name":"second"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","text":"{}"}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"a","name":"first"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","text":"{}"}}`,
	}, "\n")
	tests := []struct {
		name  string
		parse func() (*LLMResponse, error)
	}{
		{"openai", func() (*LLMResponse, error) { return parseSSEStream(strings.NewReader(openAI), nil) }},
		{"anthropic", func() (*LLMResponse, error) { return parseAnthropicStream(strings.NewReader(anthropic), nil) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := tt.parse()
			if err != nil {
				t.Fatal(err)
			}
			uses := extractToolUses(response.Content)
			if len(uses) != 2 || uses[0].Name != "first" || uses[1].Name != "second" {
				t.Fatalf("tool order = %#v", uses)
			}
		})
	}
}

func TestTraceTelemetryUsesRecordedDecisionsAndStatuses(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "traces"), 0700); err != nil {
		t.Fatal(err)
	}
	trace := strings.Join([]string{
		`{"type":"turn_start","ts":"2026-07-18T01:00:00Z","user_message":"remember me"}`,
		`{"type":"tool","ts":"2026-07-18T01:00:01Z","tool":"remember","status":"ok"}`,
		`{"type":"gate","ts":"2026-07-18T01:00:02Z","decision":"retrieve","reason":"memory tool invoked"}`,
		`{"type":"turn_end","ts":"2026-07-18T01:00:03Z","reply":"done","status":"complete","iterations":2}`,
		`{"type":"turn_start","ts":"2026-07-18T02:00:00Z","user_message":"search"}`,
		`{"type":"llm","ts":"2026-07-18T02:00:01Z","iteration":1,"in":120,"out":30,"selected_tools":8}`,
		`{"type":"tool","ts":"2026-07-18T02:00:01Z","tool":"web_search","status":"error","output":"Extension error: timeout"}`,
		`{"type":"gate","ts":"2026-07-18T02:00:02Z","decision":"skip","reason":"memory tool not invoked"}`,
		`{"type":"turn_end","ts":"2026-07-18T02:00:03Z","reply":"failed","status":"blocked","iterations":1}`,
	}, "\n") + "\n"
	path := filepath.Join(home, "traces", time.Now().Format("2006-01-02")+".jsonl")
	if err := os.WriteFile(path, []byte(trace), 0600); err != nil {
		t.Fatal(err)
	}

	skips, retrieves, errors := traceTelemetry(home)
	if skips != 1 || retrieves != 1 || errors != 1 {
		t.Fatalf("trace telemetry = %d skips, %d retrieves, %d errors", skips, retrieves, errors)
	}
	turns := traceTurns(home)
	if len(turns) != 2 || turns[0]["gate"].(map[string]any)["decision"] != "skip" || turns[1]["gate"].(map[string]any)["decision"] != "retrieve" {
		t.Fatalf("turn gates were not reconstructed: %#v", turns)
	}
	if turns[0]["status"] != "blocked" || turns[0]["llm_calls"].([]map[string]any)[0]["selected_tools"] != float64(8) {
		t.Fatalf("turn verification telemetry was not reconstructed: %#v", turns[0])
	}
}

func TestUsageStatsIgnoresErrorTextInChatHistory(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	if _, err := db.Exec("INSERT INTO chat_log (role, content) VALUES ('assistant', 'Extension error: old timeout')"); err != nil {
		t.Fatal(err)
	}
	previous := dashCore
	dashCore = &Core{DB: db}
	defer func() { dashCore = previous }()

	if got := usageStats(home)["tool_errors"]; got != 0 {
		t.Fatalf("historical chat text counted as a current tool error: %v", got)
	}
}

func TestDashboardMemoryActions(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*testing.T, string, *Core) string
		verify func(*testing.T, string, *Core)
	}{
		{
			name: "save skill",
			setup: func(t *testing.T, home string, _ *Core) string {
				path := filepath.Join(home, "skills", "demo", "SKILL.md")
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				return `{"action":"save_skill","path":"skills/demo/SKILL.md","content":"# Updated skill"}`
			},
			verify: func(t *testing.T, home string, _ *Core) {
				got, err := os.ReadFile(filepath.Join(home, "skills", "demo", "SKILL.md"))
				if err != nil || string(got) != "# Updated skill" {
					t.Fatalf("skill was not saved: %q %v", got, err)
				}
			},
		},
		{
			name: "delete episode",
			setup: func(t *testing.T, home string, core *Core) string {
				memories := filepath.Join(home, "memories")
				core.Memory = &Memory{db: core.DB, cfg: &Settings{Home: home, MemoriesDir: memories}, graph: NewGraphMemory(memories, nil)}
				if err := core.Memory.graph.RecordFact(Fact{ID: "ep_test_day", Type: "episodic", Subject: "A useful day", At: time.Now()}); err != nil {
					t.Fatal(err)
				}
				return `{"action":"delete_episode","id":"ep_test_day"}`
			},
			verify: func(t *testing.T, _ string, core *Core) {
				if _, ok := core.Memory.graph.FindFact("ep_test_day"); ok {
					t.Fatalf("episode still in graph")
				}
				if _, err := os.Stat(filepath.Join(core.Memory.graph.dir, "ep_test_day.md")); err == nil {
					t.Fatalf("episode file still exists")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			core := &Core{Settings: &Settings{Home: home}, DB: Connect(home), Tools: NewRegistry()}
			defer core.DB.Close()
			previous := dashCore
			dashCore = core
			defer func() { dashCore = previous }()

			body := tt.setup(t, home, core)
			recorder := httptest.NewRecorder()
			handleMemoryAPI(recorder, httptest.NewRequest(http.MethodPost, "/api/memory", strings.NewReader(body)))
			if recorder.Code != http.StatusOK {
				t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
			}
			tt.verify(t, home, core)
		})
	}
}

func TestDashboardQueryAPIIsReadOnly(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	previous := dashCore
	dashCore = &Core{Settings: &Settings{Home: home}, DB: db, Tools: NewRegistry()}
	defer func() { dashCore = previous }()

	for _, tt := range []struct {
		name string
		sql  string
		want int
	}{
		{"select", "SELECT content FROM chat_log", http.StatusOK},
		{"delete", "DELETE FROM chat_log", http.StatusBadRequest},
		{"multiple statements", "SELECT content FROM chat_log; DELETE FROM chat_log", http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			body := fmt.Sprintf(`{"sql":%q}`, tt.sql)
			handleQueryAPI(recorder, httptest.NewRequest(http.MethodPost, "/api/query", strings.NewReader(body)))
			if recorder.Code != tt.want {
				t.Fatalf("status %d, want %d: %s", recorder.Code, tt.want, recorder.Body.String())
			}
		})
	}
}

func TestDashboardChatRequiresProvider(t *testing.T) {
	previous := dashCore
	dashCore = &Core{}
	defer func() { dashCore = previous }()
	for _, handler := range []http.HandlerFunc{handleChat, handleChatStream} {
		recorder := httptest.NewRecorder()
		handler(recorder, httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"hello"}`)))
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("status %d, want %d", recorder.Code, http.StatusServiceUnavailable)
		}
	}
}

func TestToolCatalogIsStable(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"zeta", "alpha", "web_search"} {
		registry.Register(&Tool{Name: name})
	}
	got := registry.Catalog()
	if got[0].Name != "alpha" || got[1].Name != "web_search" || got[2].Name != "zeta" {
		t.Fatalf("catalog order is unstable: %#v", got)
	}
}

func TestWorkingMemoryPrunesRecentFixesAndPatternsDeduplicate(t *testing.T) {
	home := t.TempDir()
	old := time.Now().UTC().Add(-8 * 24 * time.Hour).Format("2006-01-02 15:04")
	path := filepath.Join(home, "working_memory.md")
	content := "## Recent Fixes\n- " + old + " | obsolete fix\n\n## System Status\n- keep this\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	removed := PruneRecentFixes(home, 7*24*time.Hour)
	if len(removed) != 1 || removed[0] != "obsolete fix" || strings.Contains(LoadWorkingMemory(home), "obsolete fix") {
		t.Fatalf("recent fixes were not pruned: %#v", removed)
	}
	if !AppendWorkingMemory(home, "Recent Fixes", "new fix") || !strings.Contains(LoadWorkingMemory(home), " | new fix") {
		t.Fatal("working-memory entry was not timestamped")
	}
	if !AddPattern(home, "When tests fail, inspect isolation first") {
		t.Fatal("new pattern was not saved")
	}
	if AddPattern(home, "When tests fail, inspect isolation first") {
		t.Fatal("duplicate pattern was saved")
	}
}

func TestConnectBuildsFTSIndices(t *testing.T) {
	db := Connect(t.TempDir())
	defer db.Close()
	if _, err := db.Exec("INSERT INTO tool_catalog_fts (name, description, keywords) VALUES ('ls_files', 'List files in a directory', 'files list dir')"); err != nil {
		t.Fatal(err)
	}
	var matches int
	if err := db.QueryRow("SELECT COUNT(*) FROM tool_catalog_fts WHERE tool_catalog_fts MATCH 'directory'").Scan(&matches); err != nil {
		t.Fatal(err)
	}
	if matches != 1 {
		t.Fatalf("FTS5 did not index catalog row: %d matches", matches)
	}
}

func TestConsolidateDue(t *testing.T) {
	response := "not json at all"
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}],"usage":{}}`, response)
	}))
	defer ts.Close()

	db := Connect(t.TempDir())
	defer db.Close()
	cfg := &Settings{Home: t.TempDir(), ConsolidateEvery: 2, TopK: 4}
	mem := NewMemory(db, &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, Model: "m"}},
		clients:   map[string]*Client{"fake": NewClient("k", ts.URL)},
		state:     map[string]*providerState{"fake": {}},
		sticky:    map[string]string{}, now: time.Now, sleep: func(time.Duration) {},
	}, cfg)
	seed := func(sid string, n int) {
		for range n {
			mem.LogChat("user", "hello", sid, "test")
			mem.LogChat("assistant", "hi", sid, "test")
		}
	}
	seed("a", 2) // 2 exchanges = due
	seed("b", 1) // below threshold

	// 1. Summarizer failure: nothing written, nothing marked, retried later.
	if got := mem.ConsolidateDue(); got != 0 {
		t.Fatalf("garbage response wrote %d facts", got)
	}
	var pending int
	db.QueryRow("SELECT COUNT(*) FROM chat_log WHERE consolidated = 0").Scan(&pending)
	if pending != 6 {
		t.Fatalf("failure must leave rows unconsolidated: pending = %d", pending)
	}

	// 2. A valid but empty result is still a consolidation failure and must retry.
	response = `{"facts":[],"episode":""}`
	if got := mem.ConsolidateDue(); got != 0 {
		t.Fatalf("empty result wrote %d facts", got)
	}
	db.QueryRow("SELECT COUNT(*) FROM chat_log WHERE consolidated = 0").Scan(&pending)
	if pending != 6 {
		t.Fatalf("empty result must leave rows unconsolidated: pending = %d", pending)
	}

	// 3. Success: fact + episode saved to .md files; session a only; b untouched.
	response = `{"facts":[{"id":"hafiz_works_vet","subject":"Hafiz works at a veterinary hospital","content":"Works at a veterinary hospital","edges":[]},{"id":"","subject":"","content":"dropped"}],"episode":"Chatted about work"}`
	if got := mem.ConsolidateDue(); got != 1 {
		t.Fatalf("written = %d, want 1", got)
	}
	if mem.graph.Stat() == 0 {
		t.Fatalf("no facts written to graph memory")
	}
	db.QueryRow("SELECT COUNT(*) FROM chat_log WHERE consolidated = 0").Scan(&pending)
	if pending != 2 {
		t.Fatalf("pending=%d, want 2 (session b untouched)", pending)
	}

	// 4. Nothing due: no LLM call at all.
	before := calls
	mem.ConsolidateDue()
	if calls != before {
		t.Fatal("consolidation called the LLM with nothing due")
	}

	// 5. Echoed template placeholders: rejected, not saved.
	response = `{"facts":[{"id":"<snake_case_id>","subject":"<one sentence>","content":"<optional body>","edges":[]}],"episode":"<one sentence>"}`
	seed("c", 2)
	before = mem.graph.Stat()
	if got := mem.ConsolidateDue(); got != 0 {
		t.Fatalf("placeholder echo was written: %d", got)
	}
	if mem.graph.Stat() != before {
		t.Fatal("placeholder episode was written")
	}

	response = `{"facts":[{"id":"hafiz_works_vet","subject":"Hafiz works at a veterinary hospital","content":"Works at a veterinary hospital","edges":[]}],"episode":"Chatted about work"}`
	// 6. Same fact distilled again: merge (no new file, just edge merge).
	seed("a", 2)
	before = mem.graph.Stat()
	mem.ConsolidateDue() // returns 0 because merge skips, but that's fine
	if mem.graph.Stat() != before {
		t.Fatalf("duplicate fact created new file: %d -> %d", before, mem.graph.Stat())
	}
}

func TestConsolidateDueLimitsLLMCallsPerPass(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"facts\":[{\"id\":\"user_preference\",\"subject\":\"User has a durable preference\",\"content\":\"Has a durable preference\",\"edges\":[]}],\"episode\":\"A useful conversation\"}"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer ts.Close()

	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	cfg := &Settings{Home: home, ConsolidateEvery: 1, ConsolidateLimit: 1, TopK: 4}
	mem := NewMemory(db, fakePM(ts.URL), cfg)
	for _, session := range []string{"a", "b"} {
		mem.LogChat("user", "remember this", session, "test")
		mem.LogChat("assistant", "noted", session, "test")
	}
	mem.ConsolidateDue()
	if calls != 1 {
		t.Fatalf("LLM calls = %d, want 1", calls)
	}
	var pending int
	db.QueryRow("SELECT COUNT(*) FROM chat_log WHERE consolidated = 0").Scan(&pending)
	if pending != 2 {
		t.Fatalf("pending rows = %d, want one session left for the next pass", pending)
	}
}

func TestFilterMergedEdges(t *testing.T) {
	existing := map[string]*Fact{
		"keep":     {},
		"survivor": {},
	}
	tests := []struct {
		name  string
		edges []Edge
		keep  string
		want  []string
	}{
		{name: "drops self-edges", edges: []Edge{{Target: "keep"}, {Target: "survivor"}}, keep: "keep", want: []string{"survivor"}},
		{name: "drops dangling", edges: []Edge{{Target: "deleted"}, {Target: "survivor"}}, keep: "keep", want: []string{"survivor"}},
		{name: "keeps valid cross-fact edges", edges: []Edge{{Target: "survivor"}}, keep: "keep", want: []string{"survivor"}},
		{name: "empty stays empty", edges: nil, keep: "keep", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterMergedEdges(tt.edges, existing, tt.keep)
			if len(got) != len(tt.want) {
				t.Fatalf("filtered = %+v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i].Target != tt.want[i] {
					t.Fatalf("filtered = %+v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestManageMemorySelfMaintenanceActions(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	memories := filepath.Join(home, "memories")
	mem := &Memory{db: db, cfg: &Settings{Home: home, MemoriesDir: memories}, graph: NewGraphMemory(memories, nil)}
	tool := makeManageMemoryTool(mem)

	// status: counts without error, no provider needed
	got := tool.Fn(map[string]any{"action": "status"})
	if !strings.Contains(got, "0 facts") {
		t.Fatalf("status = %q", got)
	}

	// maintenance actions without provider/embedder report unavailability clearly
	for _, action := range []string{"consolidate", "dedup", "rebuild_edges"} {
		got := tool.Fn(map[string]any{"action": action})
		if !strings.Contains(got, "unavailable") && !strings.Contains(got, "requires") {
			t.Fatalf("%s = %q, want clear unavailability message", action, got)
		}
	}

	// clean_edges: pure graph, no provider needed
	a := Fact{ID: "a", Type: "semantic", Subject: "a", Edges: []Edge{{Target: "b", Kind: "inferred"}}}
	b := Fact{ID: "b", Type: "semantic", Subject: "b", Edges: []Edge{{Target: "a", Kind: "inferred"}}}
	if err := mem.graph.RecordFact(a); err != nil {
		t.Fatal(err)
	}
	if err := mem.graph.RecordFact(b); err != nil {
		t.Fatal(err)
	}
	got = tool.Fn(map[string]any{"action": "clean_edges"})
	if !strings.Contains(got, "removed") {
		t.Fatalf("clean_edges = %q", got)
	}

	// fact actions still require a subject
	if got := tool.Fn(map[string]any{"action": "forget"}); !strings.Contains(got, "requires a subject") {
		t.Fatalf("forget without subject = %q", got)
	}
}

func TestGraphRebuildBackfillsMissingEmbeddings(t *testing.T) {
	// RebuildGraphEdges must embed facts lacking vectors BEFORE GraphCandidates,
	// else migrated facts are invisible to edge inference.
	// The embedder calls the package-level httpClient (hardcoded OpenRouter
	// URL), so swap it to route embedding requests to the local test server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "text-embedding") {
			fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2,0.3]}]}`)
			return
		}
		// Non-empty edges payload: an empty one counts as a failed batch and
		// makes RebuildGraphEdges return an error. This edge targets nobody,
		// so it is rejected by the allowed-candidates filter and writes nothing.
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"edges\":[{\"source\":\"orphan\",\"target\":\"nobody\",\"rel\":\"depends_on\",\"confidence\":0.9}]}"}}]}`)
	}))
	defer server.Close()
	old := httpClient
	httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		// Rewrite the hardcoded OpenRouter host to the local test server.
		r2 := r.Clone(r.Context())
		r2.URL.Scheme = "http"
		r2.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return server.Client().Transport.RoundTrip(r2)
	})}
	defer func() { httpClient = old }()
	pm := &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, BaseURL: server.URL, Model: "main", Small: "small"}},
		clients:   map[string]*Client{"fake": NewClient("test-key", server.URL)},
		state:     map[string]*providerState{"fake": {}}, sticky: map[string]string{}, preferred: map[string]providerPreference{},
		sleep: func(time.Duration) {}, now: time.Now,
	}
	dir := t.TempDir()
	gm := NewGraphMemory(filepath.Join(dir, "memories"), nil)
	gm.RecordFact(Fact{ID: "orphan", Type: "semantic", Subject: "Orphan fact", Body: "Body"})
	m := &Memory{client: pm, graph: gm, embedder: NewEmbeddingStore(Connect(dir), "test-key", "openai/text-embedding-3-large")}
	if _, err := m.RebuildGraphEdges(); err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
	if !m.embedder.HasFactEmbedding("orphan") {
		t.Fatal("fact was not embedded during rebuild")
	}
}

func TestRemoveMutualInferredEdgesAnyRelation(t *testing.T) {
	dir := t.TempDir()
	gm := NewGraphMemory(dir, nil)
	// RecordFact filters edges to existing targets at write time, so record
	// bare facts first and attach edges via ReplaceFact once both exist.
	gm.RecordFact(Fact{ID: "a", Type: "semantic", Subject: "A"})
	gm.RecordFact(Fact{ID: "b", Type: "semantic", Subject: "B"})
	gm.ReplaceFact(Fact{ID: "a", Type: "semantic", Subject: "A", Edges: []Edge{{Target: "b", Rel: "used_in", Kind: "inferred", Confidence: 0.9}}})
	gm.ReplaceFact(Fact{ID: "b", Type: "semantic", Subject: "B", Edges: []Edge{
		{Target: "a", Rel: "contains", Kind: "inferred", Confidence: 0.95},
		{Target: "a", Rel: "maintains", Kind: "explicit"},
	}})
	if n := gm.RemoveMutualInferredEdges(); n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	// Mutual rule: when A→B and B→A are BOTH inferred, drop the
	// lower-confidence edge; explicit edges always survive. A→B (0.9)
	// vs B→A (0.95): keep B→A, drop A→B. So after cleanup: a has 0
	// edges, b has contains (0.95) + maintains (explicit) = 2 edges.
	a, _ := gm.FindFact("a")
	if len(a.Edges) != 0 {
		t.Fatalf("a edges = %+v, want none (lower-confidence mirror dropped)", a.Edges)
	}
	b, _ := gm.FindFact("b")
	if len(b.Edges) != 2 {
		t.Fatalf("b edges = %+v, want contains(0.95) + maintains(explicit)", b.Edges)
	}
}

func TestJudgeChangedFacts(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"edges\":[{\"source\":\"b\",\"target\":\"a\",\"rel\":\"depends_on\",\"confidence\":0.9}]}"}}]}`)
	}))
	defer server.Close()
	pm := &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, BaseURL: server.URL, Model: "main", Small: "small"}},
		clients:   map[string]*Client{"fake": NewClient("test-key", server.URL)},
		state:     map[string]*providerState{"fake": {}}, sticky: map[string]string{}, preferred: map[string]providerPreference{},
		sleep: func(time.Duration) {}, now: time.Now,
	}
	dir := t.TempDir()
	gm := NewGraphMemory(filepath.Join(dir, "memories"), nil)
	gm.RecordFact(Fact{ID: "a", Type: "semantic", Subject: "Fact A"})
	gm.RecordFact(Fact{ID: "b", Type: "semantic", Subject: "Fact B"})
	m := &Memory{client: pm, graph: gm, embedder: &EmbeddingStore{docs: []embeddedDoc{
		{Source: "fact:a", Embedding: []float32{1, 0}},
		{Source: "fact:b", Embedding: []float32{0.9, 0.1}},
	}}}
	// Pass 1: b gets judged, edge written, marked.
	if n := m.JudgeChangedFacts(); n != 1 {
		t.Fatalf("judged %d facts, want 1", n)
	}
	b, _ := gm.FindFact("b")
	if len(b.Edges) != 1 || b.Edges[0].Target != "a" || b.Edges[0].Rel != "depends_on" || b.Edges[0].Kind != "inferred" {
		t.Fatalf("b edges = %+v", b.Edges)
	}
	// Pass 2: nothing left to judge, no LLM call.
	before := calls
	if n := m.JudgeChangedFacts(); n != 0 || calls != before {
		t.Fatalf("second pass made %d calls (n=%d), want 0", calls-before, n)
	}
	// Failed pass retries: force empty response.
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `not json`)
	}))
	defer server2.Close()
	pm2 := &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, BaseURL: server2.URL, Model: "main", Small: "small"}},
		clients:   map[string]*Client{"fake": NewClient("test-key", server2.URL)},
		state:     map[string]*providerState{"fake": {}}, sticky: map[string]string{}, preferred: map[string]providerPreference{},
		sleep: func(time.Duration) {}, now: time.Now,
	}
	gm2 := NewGraphMemory(filepath.Join(dir, "memories2"), nil)
	gm2.RecordFact(Fact{ID: "c", Type: "semantic", Subject: "Fact C"})
	m2 := &Memory{client: pm2, graph: gm2, embedder: &EmbeddingStore{docs: []embeddedDoc{}}}
	if n := m2.JudgeChangedFacts(); n != 0 {
		t.Fatalf("failed pass returned %d, want 0 (retry next pass)", n)
	}
	if got := gm2.UnjudgedFacts(); len(got) != 1 || got[0].ID != "c" {
		t.Fatalf("failed pass must leave fact unjudged: %+v", got)
	}
}

func TestDistillOutputsDue(t *testing.T) {
	// Artifact row + output file → one episodic run node, row marked distilled.
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "results", "scheduled-threads-ai-learning", "1", "post.md")
	os.MkdirAll(filepath.Dir(outputPath), 0755)
	os.WriteFile(outputPath, []byte("# Threads post\nTakeaways on open-weight models. ID: 987654321"), 0644)
	db := Connect(dir)
	defer db.Close()
	db.Exec("INSERT INTO session_artifacts (path, session_id, label, size) VALUES (?, 'scheduled-threads-ai-learning', 'threads-ai-learning output', 78)", outputPath)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"run\":{\"id\":\"ep_threads_ai_learning_2026_08_02\",\"subject\":\"Posted Threads takeaways 2026-08-02 (ID 987654321)\",\"body\":\"Daily AI learning takeaways posted; ID 987654321; published OK\",\"edges\":[{\"target\":\"threads_ai_learning_playbook\",\"rel\":\"instance_of\",\"kind\":\"explicit\"}]},\"facts\":[{\"id\":\"open_weight_models_letter\",\"subject\":\"25 tech companies warn against premature open-weight restrictions\",\"edges\":[]}]}"}}]}`)
	}))
	defer server.Close()
	pm := &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, BaseURL: server.URL, Model: "main", Small: "small"}},
		clients:   map[string]*Client{"fake": NewClient("test-key", server.URL)},
		state:     map[string]*providerState{"fake": {}}, sticky: map[string]string{}, preferred: map[string]providerPreference{},
		sleep: func(time.Duration) {}, now: time.Now,
	}
	gm := NewGraphMemory(filepath.Join(dir, "memories"), nil)
	gm.RecordFact(Fact{ID: "threads_ai_learning_playbook", Type: "semantic", Subject: "Playbook publishes daily Threads takeaways"})
	m := &Memory{db: db, client: pm, cfg: &Settings{Home: dir, MemoriesDir: filepath.Join(dir, "memories")}, graph: gm}
	if n := m.DistillOutputsDue(); n != 1 {
		t.Fatalf("distilled %d, want 1", n)
	}
	if fact, ok := gm.FindFact("ep_threads_ai_learning_2026_08_02"); !ok || fact.Subject == "" || len(fact.Edges) != 1 || fact.Edges[0].Target != "threads_ai_learning_playbook" {
		t.Fatalf("run node = %+v, ok=%v", fact, ok)
	}
	if _, ok := gm.FindFact("open_weight_models_letter"); !ok {
		t.Fatal("semantic fact from run content missing")
	}
	var distilled int
	db.QueryRow("SELECT distilled FROM session_artifacts WHERE path = ?", outputPath).Scan(&distilled)
	if distilled != 1 {
		t.Fatal("artifact not marked distilled")
	}
	// Second pass: nothing left.
	if n := m.DistillOutputsDue(); n != 0 {
		t.Fatalf("second pass distilled %d, want 0", n)
	}
	// Failure path: garbage response leaves row undistilled.
	outputPath2 := filepath.Join(dir, "results", "scheduled-gmail-daily-cleanup", "1", "log.md")
	os.MkdirAll(filepath.Dir(outputPath2), 0755)
	os.WriteFile(outputPath2, []byte("moved 3 emails"), 0644)
	db.Exec("INSERT INTO session_artifacts (path, session_id, label, size) VALUES (?, 'scheduled-gmail-daily-cleanup', 'gmail-daily-cleanup output', 15)", outputPath2)
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `not json`)
	}))
	defer server2.Close()
	pm2 := &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, BaseURL: server2.URL, Model: "main", Small: "small"}},
		clients:   map[string]*Client{"fake": NewClient("test-key", server2.URL)},
		state:     map[string]*providerState{"fake": {}}, sticky: map[string]string{}, preferred: map[string]providerPreference{},
		sleep: func(time.Duration) {}, now: time.Now,
	}
	m2 := &Memory{db: db, client: pm2, cfg: &Settings{Home: dir, MemoriesDir: filepath.Join(dir, "memories")}, graph: gm}
	if n := m2.DistillOutputsDue(); n != 0 {
		t.Fatalf("failed pass distilled %d, want 0", n)
	}
	db.QueryRow("SELECT distilled FROM session_artifacts WHERE path = ?", outputPath2).Scan(&distilled)
	if distilled != 0 {
		t.Fatal("failed artifact must stay undistilled for retry")
	}
}

func TestDashboardGraphPayloadIncludesCommunities(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	cfg := &Settings{Home: home, MemoriesDir: filepath.Join(home, "memories")}
	gm := NewGraphMemory(cfg.MemoriesDir, cfg)
	gm.RecordFact(Fact{ID: "a", Type: "semantic", Subject: "Fact A"})
	gm.SetCommunities(map[string]int{"a": 0}, []string{"a"}, map[string]string{"0": "Test"})

	previous := dashCore
	dashCore = &Core{DB: db, Settings: cfg, Memory: &Memory{db: db, graph: gm}, Tools: NewRegistry()}
	defer func() { dashCore = previous }()

	rec := httptest.NewRecorder()
	handleDataAPI(rec, httptest.NewRequest(http.MethodGet, "/api/data", nil))
	var payload struct {
		Graph struct {
			Communities map[string]int    `json:"communities"`
			Gods        []string          `json:"gods"`
			Labels      map[string]string `json:"labels"`
		} `json:"graph"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Graph.Communities["a"] != 0 || len(payload.Graph.Gods) != 1 || payload.Graph.Labels["0"] != "Test" {
		t.Fatalf("graph payload missing community fields: %+v", payload.Graph)
	}
}
