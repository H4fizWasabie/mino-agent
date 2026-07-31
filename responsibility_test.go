package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResponsibilityStoreRecordsAcceptedRoutine(t *testing.T) {
	db := Connect(t.TempDir())
	defer db.Close()
	store := NewResponsibilityStore(db)
	at := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	due := time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC)

	got, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "routine:morning-briefing",
		Type:             "imported",
		Kind:             "routine",
		Title:            "Morning briefing",
		Outcome:          "Send a concise morning briefing",
		Owner:            "mino",
		Status:           "waiting",
		Summary:          "Imported from the existing schedule.",
		SourceKind:       "schedule",
		SourceRef:        "morning-briefing",
		Schedule:         "09:00 Asia/Kuala_Lumpur",
		DueAt:            &due,
		At:               at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "routine:morning-briefing" || got.Owner != "mino" || got.Status != "waiting" ||
		got.DueAt == nil || !got.DueAt.Equal(due) {
		t.Fatalf("recorded Responsibility = %+v", got)
	}

	list, err := store.List(ResponsibilityFilter{Kind: "routine"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "Morning briefing" {
		t.Fatalf("listed Responsibilities = %+v", list)
	}

	history, err := store.History(got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Type != "imported" || history[0].Summary != "Imported from the existing schedule." {
		t.Fatalf("Responsibility History = %+v", history)
	}
	if history[0].Owner != "mino" || history[0].Outcome != "Send a concise morning briefing" ||
		history[0].DueAt == nil || !history[0].DueAt.Equal(due) {
		t.Fatalf("Responsibility History snapshot = %+v", history[0])
	}
}

func TestResponsibilityStoreUpdatesProjectionWithoutRewritingHistory(t *testing.T) {
	db := Connect(t.TempDir())
	defer db.Close()
	store := NewResponsibilityStore(db)
	started := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	updated := started.Add(15 * time.Minute)
	if _, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "routine:news", Type: "imported", Kind: "routine",
		Title: "News brief", Status: "waiting", Summary: "Imported.", At: started,
		Owner: "mino",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "routine:news",
		Type:             "started",
		Status:           "working",
		Summary:          "Gathering current sources.",
		NextAction:       "Cross-check important claims",
		NextOwner:        "mino",
		Owner:            "mino",
		At:               updated,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "working" || got.NextAction != "Cross-check important claims" || !got.UpdatedAt.Equal(updated) {
		t.Fatalf("updated Responsibility = %+v", got)
	}
	if !got.CreatedAt.Equal(started) {
		t.Fatalf("created at changed from %s to %s", started, got.CreatedAt)
	}

	history, err := store.History(got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Type != "imported" || history[1].Type != "started" {
		t.Fatalf("Responsibility History = %+v", history)
	}
}

func TestResponsibilityStoreDoesNotReopenTerminalState(t *testing.T) {
	db := Connect(t.TempDir())
	defer db.Close()
	store := NewResponsibilityStore(db)
	at := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	if _, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "one-off:report", Type: "completed", Kind: "one_off",
		Title: "Deliver report", Owner: "mino", Status: "verified",
		Summary: "Delivered.", Evidence: "artifact report.pdf", At: at,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "one-off:report", Type: "started", Status: "working",
		Summary: "Reopened.", At: at.Add(time.Minute),
	}); err == nil {
		t.Fatal("terminal Responsibility reopened")
	}
	history, _ := store.History("one-off:report")
	if len(history) != 1 {
		t.Fatalf("invalid transition changed History = %+v", history)
	}
}

func TestResponsibilityStoreBootstrapsCurrentStateOnce(t *testing.T) {
	home := t.TempDir()
	playbookDir := filepath.Join(home, "playbooks", "morning-briefing")
	if err := os.MkdirAll(playbookDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(playbookDir, "config.md"), []byte(
		"description: Send a concise morning briefing\nstatus: active\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(playbookDir, "01-send.md"), []byte(
		"# Send\n\n## Write\n\n`output/brief.md`\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "schedules.json"), []byte(
		`[{"name":"morning-briefing","time":"09:00","timezone":"Asia/Kuala_Lumpur","last_run":"2026-07-30T01:00:00Z"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	db := Connect(home)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO reminders (message, remind_at)
		VALUES ('Review the dashboard decision', '2026-07-31T04:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	store := NewResponsibilityStore(db)
	now := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)

	result, err := store.Bootstrap(home, time.FixedZone("MYT", 8*60*60), now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Routines != 1 || result.Reminders != 1 || result.AlreadyDone {
		t.Fatalf("bootstrap result = %+v", result)
	}
	routines, _ := store.List(ResponsibilityFilter{Kind: "routine"})
	if len(routines) != 1 || routines[0].Title != "Send a concise morning briefing" ||
		routines[0].DueAt == nil || !routines[0].DueAt.Equal(time.Date(2026, 7, 31, 1, 0, 0, 0, time.UTC)) ||
		routines[0].LastRunAt == nil || !routines[0].LastRunAt.Equal(time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("imported Routines = %+v", routines)
	}
	reminders, _ := store.List(ResponsibilityFilter{Kind: "reminder"})
	if len(reminders) != 1 || reminders[0].Status != "waiting" ||
		reminders[0].DueAt == nil || !reminders[0].DueAt.Equal(time.Date(2026, 7, 31, 4, 0, 0, 0, time.UTC)) {
		t.Fatalf("imported reminders = %+v", reminders)
	}
	baseline, err := store.History("system:journal")
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline) != 1 || baseline[0].Summary != "1 routine and 1 pending reminder imported." {
		t.Fatalf("baseline History = %+v", baseline)
	}

	again, err := store.Bootstrap(home, time.FixedZone("MYT", 8*60*60), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !again.AlreadyDone {
		t.Fatalf("second bootstrap result = %+v", again)
	}
	routineHistory, _ := store.History("routine:morning-briefing")
	baseline, _ = store.History("system:journal")
	if len(routineHistory) != 1 || len(baseline) != 1 {
		t.Fatalf("bootstrap duplicated history: routine=%d baseline=%d", len(routineHistory), len(baseline))
	}
	views, err := store.Views(now, time.FixedZone("MYT", 8*60*60))
	if err != nil {
		t.Fatal(err)
	}
	if len(views.Today) != 1 || views.Today[0].ID != "system:journal" || len(views.Work) != 2 {
		t.Fatalf("migration views = %+v", views)
	}
}

func TestResponsibilityStoreRequiresEvidenceToVerify(t *testing.T) {
	db := Connect(t.TempDir())
	defer db.Close()
	store := NewResponsibilityStore(db)
	at := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	if _, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "reminder:7", Type: "accepted", Kind: "reminder",
		Title: "Review the report", Owner: "mino", Status: "waiting", Summary: "Accepted.", At: at,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "reminder:7", Type: "completed", Status: "verified",
		Summary: "Delivered.", At: at.Add(time.Hour),
	}); err == nil {
		t.Fatal("Verified Responsibility accepted without Evidence")
	}
	list, _ := store.List(ResponsibilityFilter{Kind: "reminder"})
	history, _ := store.History("reminder:7")
	if len(list) != 1 || list[0].Status != "waiting" || len(history) != 1 {
		t.Fatalf("failed verification changed state: list=%+v history=%+v", list, history)
	}

	got, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "reminder:7", Type: "completed", Status: "verified",
		Summary: "Delivered.", Evidence: "telegram message 481 accepted",
		At: at.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	history, _ = store.History(got.ID)
	if got.Status != "verified" || len(history) != 2 || history[1].Evidence != "telegram message 481 accepted" {
		t.Fatalf("verified Responsibility = %+v history=%+v", got, history)
	}
}

func TestResponsibilityStoreAllowsResponsibilitiesWithoutExternalSource(t *testing.T) {
	db := Connect(t.TempDir())
	defer db.Close()
	store := NewResponsibilityStore(db)
	at := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	for _, event := range []ResponsibilityEvent{
		{ResponsibilityID: "one-off:first", Type: "accepted", Kind: "one_off",
			Title: "First outcome", Owner: "mino", Status: "working", Summary: "Accepted.", At: at},
		{ResponsibilityID: "one-off:second", Type: "accepted", Kind: "one_off",
			Title: "Second outcome", Owner: "mino", Status: "working", Summary: "Accepted.", At: at.Add(time.Second)},
	} {
		if _, err := store.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.List(ResponsibilityFilter{Kind: "one_off"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("one-off Responsibilities = %+v", got)
	}
}

func TestDashboardReadsTodayWorkAndResponsibilityHistory(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	store := NewResponsibilityStore(db)
	now := time.Now().UTC()
	if _, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "routine:ai-news-daily", Type: "imported", Kind: "routine",
		Title: "Daily AI news", Owner: "mino", Status: "waiting",
		Summary: "Imported.", SourceKind: "schedule", SourceRef: "ai-news-daily",
		At: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Record(ResponsibilityEvent{
		ResponsibilityID: "routine:ai-news-daily", Type: "completed", Status: "verified",
		Summary:  "Scheduled run completed with 1 verified output.",
		Evidence: "session:scheduled-ai-news-daily\nartifact:playbooks/ai-news-daily/output/01-ai-news.md (42 bytes)",
		At:       now,
	}); err != nil {
		t.Fatal(err)
	}
	previous := dashCore
	dashCore = &Core{
		Settings:         &Settings{Home: home, Timezone: "Asia/Kuala_Lumpur"},
		DB:               db,
		Responsibilities: store,
		Tools:            NewRegistry(),
	}
	defer func() { dashCore = previous }()

	recorder := httptest.NewRecorder()
	handleDataAPI(recorder, httptest.NewRequest(http.MethodGet, "/api/data", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("data status = %d", recorder.Code)
	}
	var data struct {
		Responsibilities ResponsibilityViews `json:"responsibilities"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if len(data.Responsibilities.Today) != 1 || len(data.Responsibilities.Work) != 1 {
		t.Fatalf("Responsibility views = %+v", data.Responsibilities)
	}
	entry := data.Responsibilities.Today[0]
	if entry.ID != "routine:ai-news-daily" || entry.Latest.Status != "verified" ||
		!strings.Contains(entry.Latest.Evidence, "artifact:playbooks/ai-news-daily/output/01-ai-news.md") {
		t.Fatalf("Today Journal entry = %+v", entry)
	}

	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/responsibilities?id=routine%3Aai-news-daily", nil)
	handleResponsibilitiesAPI(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var detail ResponsibilityDetail
	if err := json.NewDecoder(recorder.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != "routine:ai-news-daily" || len(detail.History) != 2 ||
		detail.History[1].Summary != "Scheduled run completed with 1 verified output." {
		t.Fatalf("Responsibility detail = %+v", detail)
	}
}

func TestResponsibilityEvidenceServesOnlyPlaybookOutputs(t *testing.T) {
	home := t.TempDir()
	output := filepath.Join(home, "playbooks", "ai-news-daily", "output", "01-ai-news.md")
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("# Verified AI news"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "providers.json"), []byte(`{"secret":"no"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "providers.json"),
		filepath.Join(home, "playbooks", "ai-news-daily", "output", "leak.md")); err != nil {
		t.Fatal(err)
	}
	previous := dashCore
	dashCore = &Core{Settings: &Settings{Home: home}}
	defer func() { dashCore = previous }()

	for _, tc := range []struct {
		name, path string
		status     int
		body       string
	}{
		{"output", "playbooks/ai-news-daily/output/01-ai-news.md", http.StatusOK, "# Verified AI news"},
		{"configuration", "providers.json", http.StatusForbidden, ""},
		{"traversal", "playbooks/ai-news-daily/output/../../providers.json", http.StatusForbidden, ""},
		{"symlink", "playbooks/ai-news-daily/output/leak.md", http.StatusForbidden, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet,
				"/api/responsibility-evidence?path="+tc.path, nil)
			handleResponsibilityEvidence(recorder, request)
			if recorder.Code != tc.status || tc.body != "" && recorder.Body.String() != tc.body {
				t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
			}
		})
	}
}
