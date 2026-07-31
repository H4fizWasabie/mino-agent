package main

import (
	"strings"
	"testing"
	"time"
)

func TestReminderToolsCreateListAndCancel(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	var version string
	if err := db.QueryRow("SELECT value FROM _meta WHERE key = 'schema_version'").Scan(&version); err != nil || version != "5" {
		t.Fatalf("schema version = %q, err=%v", version, err)
	}
	location := time.FixedZone("MYT", 8*60*60)
	tools := makeReminderTools(db, location)

	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	created := tools[0].Fn(map[string]any{"message": "check procurement", "remind_at": future})
	if !strings.Contains(created, "Created reminder #1") {
		t.Fatalf("create = %q", created)
	}
	listed := tools[1].Fn(nil)
	if !strings.Contains(listed, "#1") || !strings.Contains(listed, "check procurement") {
		t.Fatalf("list = %q", listed)
	}
	cancelled := tools[2].Fn(map[string]any{"id": float64(1)})
	if cancelled != "Cancelled reminder #1" {
		t.Fatalf("cancel = %q", cancelled)
	}
	if got := tools[1].Fn(nil); got != "No pending reminders." {
		t.Fatalf("list after cancel = %q", got)
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
	got := r.SchemasForContext(ctx, ctx, nil)
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
