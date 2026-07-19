package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
		ActiveTasks []TaskSnapshot `json:"active_tasks"`
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

func TestToolOutputStatus(t *testing.T) {
	tests := []struct {
		name, output, want string
	}{
		{"success", "3 results found", "ok"},
		{"builtin error", "Error reading /tmp/missing: not found", "error"},
		{"extension error", "Extension error: context deadline exceeded", "error"},
		{"cached error", "[already executed] Extension error: unavailable", "error"},
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

func TestTraceTelemetryUsesRecordedDecisionsAndStatuses(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "traces"), 0700); err != nil {
		t.Fatal(err)
	}
	trace := strings.Join([]string{
		`{"type":"turn_start","ts":"2026-07-18T01:00:00Z","user_message":"remember me"}`,
		`{"type":"tool","ts":"2026-07-18T01:00:01Z","tool":"recall","status":"ok"}`,
		`{"type":"gate","ts":"2026-07-18T01:00:02Z","decision":"retrieve","reason":"recall tool invoked"}`,
		`{"type":"turn_end","ts":"2026-07-18T01:00:03Z","reply":"done","iterations":2}`,
		`{"type":"turn_start","ts":"2026-07-18T02:00:00Z","user_message":"search"}`,
		`{"type":"tool","ts":"2026-07-18T02:00:01Z","tool":"web_search","status":"error","output":"Extension error: timeout"}`,
		`{"type":"gate","ts":"2026-07-18T02:00:02Z","decision":"skip","reason":"recall tool not invoked"}`,
		`{"type":"turn_end","ts":"2026-07-18T02:00:03Z","reply":"failed","iterations":1}`,
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
			setup: func(t *testing.T, _ string, core *Core) string {
				result, err := core.DB.Exec("INSERT INTO episodes (happened_at, summary) VALUES (?, ?)", "2026-07-18", "A useful day")
				if err != nil {
					t.Fatal(err)
				}
				id, _ := result.LastInsertId()
				return fmt.Sprintf(`{"action":"delete_episode","id":%d}`, id)
			},
			verify: func(t *testing.T, _ string, core *Core) {
				var count int
				core.DB.QueryRow("SELECT COUNT(*) FROM episodes").Scan(&count)
				if count != 0 {
					t.Fatalf("episode was not deleted: %d remain", count)
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
		{"select", "SELECT subject FROM facts", http.StatusOK},
		{"delete", "DELETE FROM facts", http.StatusBadRequest},
		{"multiple statements", "SELECT subject FROM facts; DELETE FROM facts", http.StatusBadRequest},
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

func TestFTSTermsDropsConversationalStopWords(t *testing.T) {
	if got := ftsTerms("how should you speak to me"); got != "speak" {
		t.Fatalf("unexpected FTS terms: %q", got)
	}
}

func TestOpaqueIdentifierQueriesSkipSemanticFallback(t *testing.T) {
	if !opaqueIdentifierQuery("RANKSYNC-20260717") {
		t.Fatal("identifier query was not recognized")
	}
	if opaqueIdentifierQuery("how should you speak to me") {
		t.Fatal("natural-language query was mistaken for an identifier")
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

func TestScoreFactUsesImportanceAndExplicitFeedback(t *testing.T) {
	now := "2026-07-17 00:00:00"
	low := scoreFact(factHit{keyword: 1, importance: 1, feedback: -5, createdAt: now})
	high := scoreFact(factHit{keyword: 1, importance: 5, feedback: 5, createdAt: now})
	if high <= low {
		t.Fatalf("importance and feedback did not affect ranking: high=%f low=%f", high, low)
	}
}

func TestConnectBuildsFTSIndices(t *testing.T) {
	db := Connect(t.TempDir())
	defer db.Close()
	if _, err := db.Exec("INSERT INTO facts (subject, content) VALUES (?, ?)", "Language", "Hafiz prefers English"); err != nil {
		t.Fatal(err)
	}
	var matches int
	if err := db.QueryRow("SELECT COUNT(*) FROM facts_fts WHERE facts_fts MATCH 'English'").Scan(&matches); err != nil {
		t.Fatal(err)
	}
	if matches != 1 {
		t.Fatalf("FTS5 did not index fact: %d matches", matches)
	}
}

func TestHybridRecallDeduplicatesFactCandidates(t *testing.T) {
	db := Connect(t.TempDir())
	defer db.Close()
	if _, err := db.Exec("INSERT INTO facts (subject, content, importance) VALUES (?, ?, ?)", "Language", "Hafiz prefers English", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO facts (subject, content, importance) VALUES (?, ?, ?)", "Preferences", "Hafiz prefers English", 2); err != nil {
		t.Fatal(err)
	}
	mem := NewMemory(db, nil, &Settings{TopK: 4})
	got := mem.SemanticSearch("English", nil)
	if strings.Count(got, "Hafiz prefers English") != 1 {
		t.Fatalf("unexpected hybrid recall: %q", got)
	}
}

type rewriteTransport struct{ host string }

func (t rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme, r.URL.Host = "http", t.host
	return http.DefaultTransport.RoundTrip(r)
}

func TestRecallFillerFiltersLowSimilarity(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"embedding":[1,0]}]}`) // every query embeds to [1,0]
	}))
	defer ts.Close()
	old := httpClient
	httpClient = &http.Client{Transport: rewriteTransport{strings.TrimPrefix(ts.URL, "http://")}}
	defer func() { httpClient = old }()

	db := Connect(t.TempDir())
	defer db.Close()
	mem := NewMemory(db, nil, &Settings{TopK: 4, MinSimilarity: 0.45})
	es := &EmbeddingStore{apiKey: "test", docs: []embeddedDoc{
		{Source: "episode", Content: "relevant episode", Embedding: []float32{1, 0}},      // cos=1.0
		{Source: "working_memory", Content: "unrelated note", Embedding: []float32{0, 1}}, // cos=0.0
	}}
	got := mem.SemanticSearch("favorite color", es)
	if !strings.Contains(got, "relevant episode") || strings.Contains(got, "unrelated note") {
		t.Fatalf("similarity floor not applied: %q", got)
	}
	es.docs = es.docs[1:] // fact forgotten, only junk neighbors remain
	if got := mem.SemanticSearch("favorite color", es); got != "No matches found." {
		t.Fatalf("post-forget recall should admit no matches, got %q", got)
	}
}

func TestBackfillPrunesOrphanedEmbeddings(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	if _, err := db.Exec("INSERT INTO facts (subject, content) VALUES ('Keep', 'current fact')"); err != nil {
		t.Fatal(err)
	}
	mem := NewMemory(db, nil, &Settings{Home: home, TopK: 4})
	mem.embedder = &EmbeddingStore{db: db, docs: []embeddedDoc{
		{Source: "fact", Content: "Keep: current fact", Embedding: []float32{1}},
		{Source: "fact", Content: "Orphan: deleted fact", Embedding: []float32{1}},
		{Source: "episode", Content: "orphan episode", Embedding: []float32{1}},
	}}
	mem.embedder.saveCache()
	mem.BackfillEmbeddings() // no HTTP: the only valid doc is already embedded
	if len(mem.embedder.docs) != 1 || mem.embedder.docs[0].Content != "Keep: current fact" {
		t.Fatalf("RAM cache not reconciled: %#v", mem.embedder.docs)
	}
	var n int
	db.QueryRow("SELECT COUNT(*) FROM memory_embeddings").Scan(&n)
	if n != 1 {
		t.Fatalf("DB cache not reconciled: %d rows", n)
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

	// 2. Success: fact + episode for session a only; b untouched.
	response = `{"facts":[{"subject":"Hafiz","content":"Works at a veterinary hospital"},{"subject":"","content":"dropped"}],"episode":"Chatted about work"}`
	if got := mem.ConsolidateDue(); got != 1 {
		t.Fatalf("written = %d, want 1", got)
	}
	var facts, episodes int
	db.QueryRow("SELECT COUNT(*) FROM facts WHERE source = 'consolidation'").Scan(&facts)
	db.QueryRow("SELECT COUNT(*) FROM episodes WHERE session_id = 'a'").Scan(&episodes)
	db.QueryRow("SELECT COUNT(*) FROM chat_log WHERE consolidated = 0").Scan(&pending)
	if facts != 1 || episodes != 1 || pending != 2 {
		t.Fatalf("facts=%d episodes=%d pending=%d, want 1/1/2", facts, episodes, pending)
	}

	// 3. Nothing due: no LLM call at all.
	before := calls
	mem.ConsolidateDue()
	if calls != before {
		t.Fatal("consolidation called the LLM with nothing due")
	}

	// 4. Echoed template placeholders: rejected, not saved as facts.
	response = `{"facts":[{"subject":"<who/what>","content":"<one sentence>"}],"episode":"<one sentence>"}`
	seed("c", 2)
	if got := mem.ConsolidateDue(); got != 0 {
		t.Fatalf("placeholder echo was written: %d", got)
	}
	db.QueryRow("SELECT COUNT(*) FROM episodes WHERE session_id = 'c'").Scan(&episodes)
	if episodes != 0 {
		t.Fatal("placeholder episode was written")
	}

	response = `{"facts":[{"subject":"Hafiz","content":"Works at a veterinary hospital"},{"subject":"","content":"dropped"}],"episode":"Chatted about work"}`
	// 5. Same fact distilled again: dup-skipped.
	seed("a", 2)
	if got := mem.ConsolidateDue(); got != 0 {
		t.Fatalf("duplicate fact was written: %d", got)
	}
	db.QueryRow("SELECT COUNT(*) FROM facts").Scan(&facts)
	if facts != 1 {
		t.Fatalf("facts = %d after dup pass, want 1", facts)
	}
}
