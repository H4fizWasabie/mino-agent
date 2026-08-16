package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	kinds := map[string]int{}
	for _, node := range payload.Nodes {
		kinds[node.Kind]++
		if node.ID == "memory:fact-a" && (node.At != at.Format(time.RFC3339) || node.CommunityLabel != "Test memory") {
			t.Fatalf("fact-a node = %+v", node)
		}
	}
	if kinds["memory"] != 2 || kinds["responsibility"] != 1 || kinds["schedule"] != 1 ||
		kinds["reminder"] != 1 || kinds["artifact"] != 1 {
		t.Fatalf("node kinds = %+v", kinds)
	}
	if len(payload.Edges) != 2 || payload.Edges[0].Relation == "" {
		t.Fatalf("edges = %+v", payload.Edges)
	}
	if len(payload.History) != 2 || payload.History[1].At != at.Add(2*time.Hour).Format(time.RFC3339) {
		t.Fatalf("history = %+v", payload.History)
	}
}

func TestUniverseProjectionBoundsOverviewAndNeighborhood(t *testing.T) {
	snapshot := UniverseSnapshot{Nodes: make([]UniverseNode, 100_000), Edges: make([]UniverseEdge, 1_000_000)}
	for index := range snapshot.Nodes {
		snapshot.Nodes[index] = UniverseNode{
			ID: fmt.Sprintf("memory:n-%06d", index), Kind: "memory", Label: fmt.Sprintf("Node %d", index),
			Region: "memory", Community: index % 400, Connections: index % 17,
		}
	}
	for index := range snapshot.Edges {
		snapshot.Edges[index] = UniverseEdge{
			Source: snapshot.Nodes[index%100_000].ID, Target: snapshot.Nodes[(index+1)%100_000].ID, Relation: "related",
		}
	}

	overview, ok := projectUniverseSnapshot(snapshot, "overview", "")
	if !ok || len(overview.Nodes) != 120 || !overview.HasMore || len(overview.Communities) != 256 {
		t.Fatalf("overview projection = nodes:%d communities:%d more:%v ok:%v", len(overview.Nodes), len(overview.Communities), overview.HasMore, ok)
	}
	ids := map[string]bool{}
	for _, node := range overview.Nodes {
		ids[node.ID] = true
	}
	for _, edge := range overview.Edges {
		if !ids[edge.Source] || !ids[edge.Target] {
			t.Fatalf("overview returned dangling edge %+v", edge)
		}
	}

	entity, ok := projectUniverseSnapshot(snapshot, "entity", "memory:n-000500")
	if !ok || len(entity.Nodes) == 0 || len(entity.Nodes) > 240 || len(entity.Edges) > 1_200 {
		t.Fatalf("entity projection = nodes:%d edges:%d ok:%v", len(entity.Nodes), len(entity.Edges), ok)
	}
	if entity.Nodes[0].ID != "memory:n-000500" {
		t.Fatalf("entity projection starts at %q", entity.Nodes[0].ID)
	}

	dangling := universeNeighborhood(snapshot.Nodes[:2], []UniverseEdge{{Source: snapshot.Nodes[0].ID, Target: "memory:missing"}}, snapshot.Nodes[0].ID, 10)
	if len(dangling) != 1 || dangling[0].ID != snapshot.Nodes[0].ID {
		t.Fatalf("dangling neighborhood = %+v", dangling)
	}
	lateIncident := universeProjectionEdges([]UniverseEdge{
		{Source: snapshot.Nodes[1].ID, Target: snapshot.Nodes[2].ID},
		{Source: snapshot.Nodes[0].ID, Target: snapshot.Nodes[1].ID},
	}, snapshot.Nodes[:3], snapshot.Nodes[0].ID, 1)
	if len(lateIncident) != 1 || lateIncident[0].Source != snapshot.Nodes[0].ID {
		t.Fatalf("focused edge was not preserved: %+v", lateIncident)
	}
	exactNodes := snapshot.Nodes[:240]
	exactEdges := snapshot.Edges[:239]
	exact, ok := projectUniverseSnapshot(UniverseSnapshot{Nodes: exactNodes, Edges: exactEdges}, "entity", exactNodes[0].ID)
	if !ok || exact.HasMore {
		t.Fatalf("exact-budget neighborhood has_more = %v, ok = %v", exact.HasMore, ok)
	}

	revision := universeRevision(UniverseSnapshot{Nodes: []UniverseNode{{ID: "memory:a", Label: "Before"}}})
	changed := universeRevision(UniverseSnapshot{Nodes: []UniverseNode{{ID: "memory:a", Label: "After"}}})
	if revision == changed {
		t.Fatal("projection revision ignored a visible node change")
	}
}

func TestUniverseOverviewBudgetScalesWithoutOpeningTheFullGraph(t *testing.T) {
	for _, test := range []struct {
		total, level, want int
	}{
		{1_390, 0, 420}, {1_390, 1, 840}, {1_390, 2, 1_200},
		{10_000, 2, 1_080}, {50_001, 2, 360}, {100_000, 2, 360},
	} {
		if got := universeOverviewBudget(test.total, test.level); got != test.want {
			t.Fatalf("universeOverviewBudget(%d, %d) = %d, want %d", test.total, test.level, got, test.want)
		}
	}
}

func TestDashboardEventsUseNonDestructiveCursors(t *testing.T) {
	dashEventMu.Lock()
	oldQueue, oldCursor := dashEventQ, dashCursor
	dashEventQ, dashCursor = nil, 0
	dashEventMu.Unlock()
	defer func() {
		dashEventMu.Lock()
		dashEventQ, dashCursor = oldQueue, oldCursor
		dashEventMu.Unlock()
	}()

	pushDashEvent(map[string]any{"type": "turn_start"})
	pushDashEvent(map[string]any{"type": "tool", "tool": "remember"})

	read := func(cursor string) struct {
		Events []map[string]any `json:"events"`
		Cursor int64            `json:"cursor"`
	} {
		t.Helper()
		recorder := httptest.NewRecorder()
		handleEventsAPI(recorder, httptest.NewRequest(http.MethodGet, "/api/events?cursor="+cursor, nil))
		var payload struct {
			Events []map[string]any `json:"events"`
			Cursor int64            `json:"cursor"`
		}
		if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}

	first, second := read("0"), read("0")
	if len(first.Events) != 2 || len(second.Events) != 2 || first.Cursor != 2 || second.Cursor != 2 {
		t.Fatalf("cursor reads consumed shared events: first=%+v second=%+v", first, second)
	}
	latest := read("2")
	if len(latest.Events) != 0 || latest.Cursor != 2 {
		t.Fatalf("latest cursor = %+v", latest)
	}
}
