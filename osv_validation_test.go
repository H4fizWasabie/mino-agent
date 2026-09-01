package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// OSV-04 — Validation cases (wayfinder map #66). Proves "right store, right
// answer" after OSV-01/02/03: (a) a reminder question is answered from the
// reminder store with zero calendar tool calls, (b) mutating tool results
// carry destination + state, (c) a claimed fix that didn't land is corrected
// by the harness. Table-driven, MEM-05 pattern.

// (a) The Arachem case: a pending system reminder is the ground truth for
// "when was my last meeting". The harness must offer the reminder store, the
// model must use it, and no calendar tool may be invoked.
func TestOSV04ReminderQuestionUsesReminderStoreNotCalendar(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	loc := time.FixedZone("MYT", 8*60*60)
	if _, err := db.Exec("INSERT INTO reminders (message, remind_at) VALUES (?, ?)",
		"Arachem meeting", "2026-08-07T04:30:00Z"); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	r.SetSearchDB(db)
	r.Register(makeToolSearchTool(r))
	r.Register(makeToolCallTool(r))
	for _, tool := range makeReminderTools(db, loc) {
		r.Register(tool)
	}
	// Calendar tools are available too — the model must NOT reach for them.
	for _, tool := range []*Tool{makeCalendarTool(db, home), makeListCalendarTool(db, loc)} {
		r.Register(tool)
	}

	// list_reminders is neither floor nor (on a fresh DB with no tool_calls
	// history) frequency-derived, so it's deferred (issue #483): the model
	// reaches it via tool_search then tool_call, not a direct offered slot.
	client := &fakeClient{script: []*LLMResponse{
		scriptedResp([]ContentBlock{toolBlock(toolSearchName, map[string]any{"name": "list_reminders"})}, "tool_use"),
		scriptedResp([]ContentBlock{toolBlock(toolCallName, map[string]any{"name": "list_reminders", "args": map[string]any{}})}, "tool_use"),
		scriptedResp([]ContentBlock{textBlock("Your last Arachem meeting reminder is Friday 7 Aug at 12:30 — a system reminder, never in your calendar.")}, "stop"),
	}}
	msgs := []Message{{Role: "user", Content: "When was my last Arachem meeting?"}}
	result := RunLoopContext(context.Background(), client, "osv04-reminder", "", msgs, r, 5, 100, nil, "")
	if result.Status != "complete" {
		t.Fatalf("status = %q, want complete", result.Status)
	}
	if !strings.Contains(result.Reply, "7 Aug") || !strings.Contains(result.Reply, "12:30") {
		t.Fatalf("reply does not answer from the reminder store: %q", result.Reply)
	}
	if len(result.ToolCalls) != 2 || result.ToolCalls[0].Name != toolSearchName || result.ToolCalls[1].Name != toolCallName {
		t.Fatalf("tool calls = %v, want exactly [%s %s]", result.ToolCalls, toolSearchName, toolCallName)
	}
	for _, tc := range result.ToolCalls {
		if name, _ := tc.Args["name"].(string); name == "list_events" || name == "create_event" {
			t.Fatalf("calendar tool invoked via dispatcher: %s", name)
		}
	}
	if !strings.Contains(result.ToolCalls[1].Output, "Arachem") || !strings.Contains(result.ToolCalls[1].Output, "12:30") {
		t.Fatalf("tool_call(list_reminders) output missing meeting: %q", result.ToolCalls[1].Output)
	}
	// The harness offered both dispatchers, the model's only path to a
	// deferred tool like list_reminders on a turn with no usage history yet.
	hasSearch, hasCall := false, false
	for _, s := range client.toolSets[0] {
		if s.Name == toolSearchName {
			hasSearch = true
		}
		if s.Name == toolCallName {
			hasCall = true
		}
	}
	if !hasSearch || !hasCall {
		t.Fatalf("tool_search/tool_call not offered to the model: %v", client.toolSets[0])
	}
}

// (b) Destination + state: every mutating tool result answers "what happened
// and where it went" (OSV-01 pattern).
func TestOSV04ToolResultsReportDestinationAndState(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	loc := time.FixedZone("MYT", 8*60*60)

	mem := &Memory{graph: NewGraphMemory(t.TempDir(), nil)}
	mem.skills = NewSkillLoader(home)

	// Minimal playbook so schedule_playbook can validate.
	if err := os.MkdirAll(filepath.Join(home, "playbooks", "daily", "stages", "01-gather"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "playbooks", "daily", "CONTEXT.md"), []byte("# Daily\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "playbooks", "daily", "config.md"), []byte("status: active\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "playbooks", "daily", "stages", "01-gather", "CONTEXT.md"), []byte("# Gather\n\n## Process\n\n1. Do the thing.\n\n## Outputs\n\n| Artifact | Location | Format |\n| --- | --- | --- |\n| Result | `output/result.md` | Markdown |\n"), 0644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	for _, tool := range append(makeReminderTools(db, loc),
		makeCalendarTool(db, home),
		makeNotesTool(db, mem),
		makeUpdateSoulTool(home),
		makeCreateSkillTool(home, mem),
		makeWorkingMemoryTool(home, mem),
		makePatternTool(home, mem),
		makeSchedulePlaybookTool(home, "Asia/Kuala_Lumpur"),
	) {
		r.Register(tool)
	}

	cases := []struct {
		name    string
		tool    string
		args    map[string]any
		wantIn  []string
		wantOut []string
	}{
		{"reminder names its store", "create_reminder",
			map[string]any{"message": "call supplier", "remind_at": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)},
			[]string{"system reminders", "SQLite", "NOT your calendar"}, []string{"Error"}},
		{"reminder cancel reports state", "cancel_reminder",
			map[string]any{"id": float64(1)},
			[]string{"status: cancelled"}, []string{"Error"}},
		{"event names both stores", "create_event",
			map[string]any{"title": "review", "start": "2026-08-10T09:00:00+08:00"},
			[]string{"calendar_events", "calendar.ics"}, []string{"Error"}},
		{"fact names its file", "save_note",
			map[string]any{"id": "arachem", "subject": "Meeting is a reminder", "content": "stored in memories"},
			[]string{"memories/arachem.md"}, []string{"Error"}},
		{"soul names its path", "update_soul",
			map[string]any{"content": "Always verify"},
			[]string{"SOUL.md updated at"}, []string{"Error"}},
		{"skill names its file", "create_skill",
			map[string]any{"name": "weekly report", "description": "Weekly report skill", "body": "steps"},
			[]string{"skills/weekly-report/SKILL.md"}, []string{"Error"}},
		{"working memory names its file", "add_working_memory",
			map[string]any{"section": "Recent Fixes", "content": "fixed the dispatcher"},
			[]string{"working_memory.md"}, []string{"Error"}},
		{"pattern names its file", "add_pattern",
			map[string]any{"rule": "When deploying, test first"},
			[]string{"patterns.md"}, []string{"Error"}},
		{"schedule names its store", "schedule_playbook",
			map[string]any{"name": "daily", "time": "09:00"},
			[]string{"schedules.json"}, []string{"Error"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.ExecuteContext(context.Background(), c.tool, c.args)
			for _, want := range c.wantIn {
				if !strings.Contains(got, want) {
					t.Errorf("result missing %q: %q", want, got)
				}
			}
			for _, out := range c.wantOut {
				if strings.Contains(got, out) {
					t.Errorf("result unexpectedly contains %q: %q", out, got)
				}
			}
		})
	}
}

// (c) A claimed fix that didn't land is corrected: the harness compares the
// reply's outcome claim against this turn's tool results (OSV-03). Table over
// the claim surface — push only on contradiction.
func TestOSV04OutcomeClaimsAreCorrected(t *testing.T) {
	ok := ToolCall{Name: "write_file", Args: map[string]any{"path": "/tmp/x.md"}, Output: "Wrote 123 bytes to /tmp/x.md"}
	fail := ToolCall{Name: "write_file", Args: map[string]any{"path": "/tmp/x.md"}, Output: "Error writing /tmp/x.md: permission denied"}
	cases := []struct {
		name     string
		reply    string
		calls    []ToolCall
		wantPush bool
		wantIn   []string
	}{
		{"failure claim vs success evidence", "the edit was rejected", []ToolCall{ok}, true, []string{"tool results show success", "write_file"}},
		{"success claim vs error evidence", "I fixed the file", []ToolCall{fail}, true, []string{"tool results show errors"}},
		{"reminder failure claim vs success evidence", "the reminder was rejected", []ToolCall{{Name: "create_reminder", Args: map[string]any{}, Output: "Created reminder #1 — stored in system reminders (SQLite), NOT your calendar"}}, true, []string{"tool results show success"}},
		{"search-style failure is not a claim", "the search failed to find anything", []ToolCall{ok}, false, nil},
		{"failure claim matches real error", "the edit was rejected", []ToolCall{fail}, false, nil},
		{"success claim matches real success", "I fixed the file", []ToolCall{ok}, false, nil},
		{"no claim, no push", "Here is the summary.", []ToolCall{ok}, false, nil},
		{"mixed tool results never push", "the file was rejected", []ToolCall{ok, fail}, false, nil},
		{"consistent completion claim", "The edit completed.", []ToolCall{ok}, false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := outcomeContradiction(c.reply, c.calls)
			if !c.wantPush && got != "" {
				t.Fatalf("unexpected push: %q", got)
			}
			if c.wantPush {
				for _, want := range c.wantIn {
					if !strings.Contains(got, want) {
						t.Errorf("push missing %q: %q", want, got)
					}
				}
			}
		})
	}
}
