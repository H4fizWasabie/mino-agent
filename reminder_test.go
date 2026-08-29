package main

import (
	"fmt"
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
	if err := db.QueryRow("SELECT value FROM _meta WHERE key = 'schema_version'").Scan(&version); err != nil || version != "8" {
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

func TestSchemasForContextKeepsCoreAndRetrievesSpecialist(t *testing.T) {
	db := Connect(t.TempDir())
	defer db.Close()
	r := NewRegistry()
	r.SetSearchDB(db)
	for _, name := range []string{"remember", "read_file", "write_file", "save_note", "search_web", "list_playbooks", "run_playbook", "bash", "create_reminder"} {
		r.Register(&Tool{Name: name, Description: name + " everyday capability", Schema: map[string]any{"type": "object"}})
	}
	r.Register(&Tool{Name: "procurement_report", Description: "Analyze supplier purchase orders and procurement audit data", Schema: map[string]any{"type": "object"}})
	r.Register(&Tool{Name: "image_transform", Description: "Transform and generate raster images", Schema: map[string]any{"type": "object"}})

	ctx := "The procurement skill says to analyze supplier purchase orders and produce the weekly audit."
	got := r.SchemasForContext("", ctx, ctx)
	names := make(map[string]bool, len(got))
	for _, schema := range got {
		names[schema.Name] = true
	}
	if !names["procurement_report"] {
		t.Fatalf("specialist tool was not retrieved: %v", names)
	}
	if names["image_transform"] {
		t.Fatalf("unrelated specialist tool was retrieved: %v", names)
	}
	if !names["remember"] || !names["run_playbook"] || !names["bash"] {
		t.Fatalf("essential tools missing: %v", names)
	}
}

func TestSchemasForContextUserWordsSurviveSystemPromptWordBudget(t *testing.T) {
	// Live 2026-08-17: "remind me ..." never surfaced create_reminder — the
	// FTS word budget (80 unique words, document order) was consumed by the
	// system prompt before the user message was reached, so the purpose-built
	// tool never entered the schema and the model fell back to bash+sqlite.
	// The user's own words must enter the query first, like the MCP gate
	// already does with oneTurnText.
	db := Connect(t.TempDir())
	defer db.Close()
	r := NewRegistry()
	r.SetSearchDB(db)
	for _, name := range []string{"remember", "read_file", "write_file", "save_note", "search_web", "bash", "list_playbooks", "run_playbook", "send_document", "note_session"} {
		r.Register(&Tool{Name: name, Description: name + " everyday capability", Schema: map[string]any{"type": "object"}})
	}
	// Real descriptions (the model's actual schema text) — reminder tools
	// match only on reminder-vocabulary tokens, never on the system prompt's
	// playbook/schedule vocabulary.
	r.Register(&Tool{Name: "create_reminder", Description: "Create a one-time reminder that Mino will send to the owner's Telegram chat. Resolve relative dates using the configured timezone and provide an ISO 8601 time.", Schema: map[string]any{"type": "object"}})
	r.Register(&Tool{Name: "list_reminders", Description: "List pending one-time reminders in the configured timezone. Use when asked about a meeting, appointment, or deadline that Mino was asked to remind about.", Schema: map[string]any{"type": "object"}})
	r.Register(&Tool{Name: "cancel_reminder", Description: "Cancel a pending reminder by its numeric ID.", Schema: map[string]any{"type": "object"}})

	// Filler tools whose descriptions repeat the system prompt vocabulary —
	// they match dozens of query terms and crowd the top-16 bm25 rank, the
	// live shape (72-tool catalog vs 23 here).
	fillers := []string{"schedule_playbook", "manage_playbook", "audit_playbook", "fetch_url", "list_schedules", "cancel_schedule", "edit_file", "threads_post", "graphify_query", "graphify_path", "graphify_explain", "codegraph_query", "system_check", "send_message", "view_image", "list_files", "grep_files", "git_status", "git_diff", "capture_playbook", "query_audit", "request_approval", "write_unit", "install_package", "screenshot"}
	for i, name := range fillers {
		r.Register(&Tool{Name: name, Description: fmt.Sprintf("playbook schedule stage contract state file workspace skill install update journal audit approval privilege sudoers config reload provider model graph query path explain code read write edit grep glob status diff render capture page url server dashboard api universe endpoint auth token clock timezone iteration loop guard gate bounded cap budget verify discipline snapshot #%d", i), Schema: map[string]any{"type": "object"}})
	}

	// A system prompt with more than 80 unique words, followed by the user
	// message — the live toolSelectionContext shape (system first, truncated
	// to 24000 chars). Vocabulary deliberately avoids the reminder tool
	// descriptions: in production the truncation window lands mid-prompt and
	// the state map (the only "reminder" tokens) is sliced off.
	// 102 unique words — none overlap the reminder tool descriptions — so the
	// 80-word budget fills from system vocabulary and the user message's
	// "remind"/"reminder" never enter the FTS query (the live shape).
	sysVocab := strings.Split("playbook schedule stage contract state file workspace skill install update journal audit approval privilege sudoers config reload provider model graph query path explain code write edit grep glob status diff render capture page url server dashboard api universe endpoint auth token clock iteration loop guard gate bounded cap budget verify discipline snapshot route fact stale live truth workflow directory home package rollback health binary swap migrate schema table index keyword vector embed openrouter fallback vision image pixel canvas draw zoom density depth galaxy orbital sphere landscape portrait mobile desktop zoom lens search inspect node edge community branch trunk overview renderer", " ")
	var sys strings.Builder
	for len(sys.String()) < 13400 {
		for _, w := range sysVocab {
			sys.WriteString(w + " ")
		}
		sys.WriteString(". ")
	}

	fullCtx := toolSelectionContext(sys.String(), []Message{{Role: "user", Content: "Mino, remind me of this later.. Around 2.30pm. This vetplus sales reps want to have meeting with me."}})
	oneTurn := "Mino, remind me of this later.. Around 2.30pm. This vetplus sales reps want to have meeting with me."
	got := r.SchemasForContext("", fullCtx, oneTurn)
	names := make(map[string]bool, len(got))
	for _, schema := range got {
		names[schema.Name] = true
	}
	if !names["create_reminder"] {
		t.Fatalf("create_reminder was starved by the system prompt word budget: %v", names)
	}
}

func TestSchemasForContextIncludesExplicitExtensionCapabilityAcrossChannels(t *testing.T) {
	db := Connect(t.TempDir())
	defer db.Close()
	r := NewRegistry()
	r.SetSearchDB(db)
	for _, name := range []string{"remember", "read_file", "write_file", "save_note", "search_web", "list_playbooks", "run_playbook", "bash"} {
		r.Register(&Tool{Name: name, Description: name + " everyday capability", Schema: map[string]any{"type": "object"}})
	}
	r.Register(&Tool{Name: "threads_post", Description: "Publish a text post to Threads.", Schema: map[string]any{"type": "object"}})
	r.Register(&Tool{Name: "threads_get_replies", Description: "Get recent replies to a Threads post.", Schema: map[string]any{"type": "object"}})

	for _, prompt := range []string{
		"Telegram request: use the Threads post extension to publish today's update.",
		"Dashboard request: use the Threads post extension to publish today's update.",
	} {
		got := r.SchemasForContext("", "generic runtime context with unrelated history", prompt)
		names := make(map[string]bool, len(got))
		for _, schema := range got {
			names[schema.Name] = true
		}
		if !names["threads_post"] {
			t.Fatalf("explicit Threads capability missing for %q: %v", prompt, names)
		}
	}
}

func TestMCPGateCapsAtFiveInRelevanceOrder(t *testing.T) {
	// The MCP gate: keyword FTS5 is the final gate, capped at 5, in bm25
	// relevance order (never alphabetical). The model must see the most
	// relevant schemas first, not the alphabetically-first wrappers.
	db := Connect(t.TempDir())
	defer db.Close()
	r := NewRegistry()
	r.SetSearchDB(db)
	for _, name := range []string{"read_file", "write_file", "bash", "remember", "search_web"} {
		r.Register(&Tool{Name: name, Description: name + " everyday capability", Schema: map[string]any{"type": "object"}})
	}
	// MCP tools with descriptions matching "gmail" to varying degrees.
	r.Register(&Tool{Name: "MCP_composio_GMAIL_FETCH_EMAILS", Description: "Fetch Gmail emails matching a query", Schema: map[string]any{"type": "object"}})
	r.Register(&Tool{Name: "MCP_composio_GMAIL_BATCH_MODIFY_MESSAGES", Description: "Batch modify Gmail messages labels", Schema: map[string]any{"type": "object"}})
	r.Register(&Tool{Name: "MCP_composio_GMAIL_CREATE_LABEL", Description: "Create a Gmail label", Schema: map[string]any{"type": "object"}})
	r.Register(&Tool{Name: "MCP_composio_GMAIL_SEND_EMAIL", Description: "Send email through Gmail", Schema: map[string]any{"type": "object"}})
	r.Register(&Tool{Name: "MCP_composio_GMAIL_GET_MESSAGE", Description: "Get one Gmail message", Schema: map[string]any{"type": "object"}})
	r.Register(&Tool{Name: "MCP_composio_INSTAGRAM_POST", Description: "Post to Instagram", Schema: map[string]any{"type": "object"}})
	r.Register(&Tool{Name: "MCP_composio_SLACK_SEND", Description: "Send a Slack message", Schema: map[string]any{"type": "object"}})

	ctx := "clean up promotional emails in my gmail inbox, fetch and trash them"
	got := r.SchemasForContext("", ctx, ctx)
	mcpSelected := make([]string, 0)
	for _, s := range got {
		if strings.HasPrefix(s.Name, "MCP_") {
			mcpSelected = append(mcpSelected, s.Name)
		}
	}
	if len(mcpSelected) == 0 {
		t.Fatal("no MCP tools selected for gmail request")
	}
	if len(mcpSelected) > 5 {
		t.Fatalf("MCP cap exceeded: %d tools (%v)", len(mcpSelected), mcpSelected)
	}
	// The most relevant tool (fetch emails) must be present; unrelated MCP
	// toolkits (instagram, slack) must NOT leak in.
	found := false
	for _, name := range mcpSelected {
		if name == "MCP_composio_GMAIL_FETCH_EMAILS" {
			found = true
		}
		if strings.Contains(name, "INSTAGRAM") || strings.Contains(name, "SLACK") {
			t.Fatalf("unrelated MCP toolkit leaked: %v", mcpSelected)
		}
	}
	if !found {
		t.Fatalf("most relevant GMAIL_FETCH_EMAILS not selected: %v", mcpSelected)
	}
}
