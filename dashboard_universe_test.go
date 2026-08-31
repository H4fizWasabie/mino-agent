package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDashboardUniverseIncludesDurableStateAndHistory(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	cfg := &Settings{Home: home, MemoriesDir: filepath.Join(home, "memories"), Timezone: "Asia/Kuala_Lumpur"}
	graph := NewGraphMemory(cfg.MemoriesDir, cfg)
	at := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	graph.RecordFact(Fact{ID: "fact-b", Type: "episodic", Subject: "Verified outcome", At: at.Add(time.Hour)})
	graph.RecordFact(Fact{
		ID: "fact-a", Type: "semantic", Subject: "Durable preference", At: at,
		Body: "The complete memory body.", Source: "owner",
		Edges: []Edge{{Target: "fact-b", Rel: "supports", Kind: "explicit"}},
	})
	graph.SetCommunities(map[string]int{"fact-a": 0, "fact-b": 0}, nil, map[string]string{"0": "Test memory"})

	responsibilities := NewResponsibilityStore(db)
	if _, err := responsibilities.Record(ResponsibilityEvent{
		ResponsibilityID: "routine:test", Type: "imported", Kind: "routine",
		Title: "Test routine", Owner: "mino", Status: "waiting",
		Summary: "Imported from schedule.", SourceKind: "schedule", SourceRef: "test",
		At: at,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := responsibilities.Record(ResponsibilityEvent{
		ResponsibilityID: "routine:test", Type: "started", Status: "working",
		Summary: "Work started.", At: at.Add(2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := saveSchedules(home, []PlaybookSchedule{{Name: "test", Time: "09:00", Timezone: "Asia/Kuala_Lumpur"}}); err != nil {
		t.Fatal(err)
	}
	db.Exec("INSERT INTO reminders(message,remind_at,status) VALUES('Check outcome','2026-08-02T09:00:00Z','pending')")
	db.Exec("INSERT INTO session_artifacts(path,session_id,label,size,created_at) VALUES('results/report.md','session-a','Report',42,'2026-08-01T12:00:00Z')")
	for _, dir := range []string{filepath.Join(home, "results"), filepath.Join(home, "traces")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		filepath.Join(home, "results", "report.md"):     "registered artifact",
		filepath.Join(home, "results", "untracked.txt"): "durable output",
		filepath.Join(home, "traces", "turn.jsonl"):     "{}\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	previous := dashCore
	dashCore = &Core{
		DB: db, Settings: cfg, Memory: &Memory{db: db, graph: graph},
		Responsibilities: responsibilities, Tools: NewRegistry(),
	}
	defer func() { dashCore = previous }()

	recorder := httptest.NewRecorder()
	handleUniverseAPI(recorder, httptest.NewRequest(http.MethodGet, "/api/universe", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var payload UniverseSnapshot
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Counts.Memories != 2 || payload.Counts.Relationships != 1 {
		t.Fatalf("memory counts = %+v", payload.Counts)
	}
	if payload.Counts.Responsibilities != 1 || payload.Counts.Schedules != 1 || payload.Counts.Reminders != 1 {
		t.Fatalf("operational counts = %+v", payload.Counts)
	}
	if payload.Counts.Artifacts != 1 || payload.Counts.Files != 2 {
		t.Fatalf("output counts = %+v", payload.Counts)
	}
	kinds := map[string]int{}
	for _, node := range payload.Nodes {
		kinds[node.Kind]++
		if node.ID == "memory:fact-a" && (node.At != at.Format(time.RFC3339) || node.CommunityLabel != "Test memory") {
			t.Fatalf("fact-a node = %+v", node)
		}
	}
	if kinds["memory"] != 2 || kinds["responsibility"] != 1 || kinds["schedule"] != 1 ||
		kinds["reminder"] != 1 || kinds["artifact"] != 1 || kinds["file"] != 2 {
		t.Fatalf("node kinds = %+v", kinds)
	}
	if len(payload.Edges) != 2 || payload.Edges[0].Relation == "" {
		t.Fatalf("edges = %+v", payload.Edges)
	}
	if len(payload.History) != 2 || payload.History[1].At != at.Add(2*time.Hour).Format(time.RFC3339) {
		t.Fatalf("history = %+v", payload.History)
	}
}

