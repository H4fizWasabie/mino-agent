package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReminderToolsCreateListAndCancel(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	var version string
	if err := db.QueryRow("SELECT value FROM _meta WHERE key = 'schema_version'").Scan(&version); err != nil || version != "10" {
		t.Fatalf("schema version = %q, err=%v", version, err)
	}
	location := time.FixedZone("MYT", 8*60*60)
	tools := makeReminderTools(db, location)

	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	created := tools[0].Fn(map[string]any{"message": "check procurement", "remind_at": future})
	if !strings.Contains(created, "Created reminder #1") {
		t.Fatalf("create = %q", created)
	}
	// OSV-01: the result must answer "where did it go" — system reminders
	// (SQLite), explicitly NOT the user's calendar.
	for _, want := range []string{"system reminders", "SQLite", "NOT your calendar"} {
		if !strings.Contains(created, want) {
			t.Fatalf("create result missing %q: %q", want, created)
		}
	}
	listed := tools[1].Fn(nil)
	if !strings.Contains(listed, "#1") || !strings.Contains(listed, "check procurement") {
		t.Fatalf("list = %q", listed)
	}
	cancelled := tools[2].Fn(map[string]any{"id": float64(1)})
	if !strings.Contains(cancelled, "Cancelled reminder #1") || !strings.Contains(cancelled, "cancelled") {
		t.Fatalf("cancel = %q", cancelled)
	}
	if got := tools[1].Fn(nil); got != "No pending reminders." {
		t.Fatalf("list after cancel = %q", got)
	}
}

func TestDispatchDueReminders(t *testing.T) {
	// Fake Telegram bot API — telegramAPIBase is overridable (same as telegram_test.go).
	var mu sync.Mutex
	var sent []string
	var reject bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		sent = append(sent, string(body))
		rej := reject
		mu.Unlock()
		if rej {
			w.Write([]byte(`{"ok": false}`))
			return
		}
		w.Write([]byte(`{"ok": true}`))
	}))
	defer server.Close()
	telegramAPIBase = server.URL
	defer func() { telegramAPIBase = "https://api.telegram.org" }()

	now := time.Now().UTC()
	cases := []struct {
		name        string
		token       string
		chatID      int64
		reminders   [][2]string // message, remind_at
		reject      bool
		wantDeliver int // reminders marked delivered
		wantSent    int // HTTP calls to the fake API
	}{
		{"due reminder is delivered", "tok", 123, [][2]string{{"alpha", now.Add(-time.Minute).Format(time.RFC3339)}}, false, 1, 1},
		{"future reminder stays pending", "tok", 123, [][2]string{{"beta", now.Add(time.Hour).Format(time.RFC3339)}}, false, 0, 0},
		{"no telegram config sends nothing", "", 0, [][2]string{{"gamma", now.Add(-time.Minute).Format(time.RFC3339)}}, false, 0, 0},
		{"telegram rejection keeps reminder pending", "tok", 123, [][2]string{{"delta", now.Add(-time.Minute).Format(time.RFC3339)}}, true, 0, 2}, // markdown + plain retry
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			db := Connect(home)
			defer db.Close()
			settings := &Settings{Home: home, Telegram: tc.token, TelegramChatID: tc.chatID}
			core := &Core{Settings: settings, DB: db, Sessions: NewSessionManager(settings, nil)}
			for _, r := range tc.reminders {
				if _, err := db.Exec("INSERT INTO reminders (message, remind_at) VALUES (?, ?)", r[0], r[1]); err != nil {
					t.Fatal(err)
				}
			}
			mu.Lock()
			sent = nil
			reject = tc.reject
			mu.Unlock()

			dispatchDueReminders(core)

			var delivered int
			db.QueryRow("SELECT COUNT(*) FROM reminders WHERE status = 'delivered'").Scan(&delivered)
			if delivered != tc.wantDeliver {
				t.Errorf("delivered = %d, want %d", delivered, tc.wantDeliver)
			}
			mu.Lock()
			calls := len(sent)
			mu.Unlock()
			if calls != tc.wantSent {
				t.Errorf("telegram calls = %d, want %d", calls, tc.wantSent)
			}
		})
	}
}

// #483 replaced keyword/semantic auto-inclusion with a fixed tier-1 set
// (floor + frequency + dispatchers) plus a tier-2 deferred index the model
// searches on demand — the four tests below (previously exercising the FTS5
// keyword sliding window, the 80-word budget, explicit-mention inclusion,
// and the MCP relevance cap, all deleted with the mechanism) are replaced by
// TestSchemasForContextFixedTierOneSet, TestDeferredToolIndexListsUnselectedToolsOnly,
// TestToolSearchReturnsSchemaText, and TestToolCallDispatchesToRealHandler in
// tools_test.go, which exercise the same "a specialist tool the turn didn't
// obviously mention must still be reachable" property under the new
// mechanism: present in the deferred index, callable via tool_search +
// tool_call, regardless of turn wording.
