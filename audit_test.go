package main

import (
	"strings"
	"testing"
)

// TestQueryAuditToolWithToolErrors guards the single-connection pool:
// db.SetMaxOpenConns(1) means a second query while the first rows set is
// still open deadlocks (rows must be closed before the tool_calls query).
func TestQueryAuditToolWithToolErrors(t *testing.T) {
	db := Connect(t.TempDir())
	defer db.Close()
	if _, err := db.Exec("INSERT INTO audit_events (session_id, event_type, message) VALUES ('s1', 'test', 'hello audit')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO tool_calls (session_id, tool_name, output_summary, status) VALUES ('s1', 'bash', 'boom', 'error')"); err != nil {
		t.Fatal(err)
	}
	tool := makeQueryAuditTool(db)
	got := tool.Fn(map[string]any{"limit": 10})
	if !strings.Contains(got, "hello audit") || !strings.Contains(got, "boom") {
		t.Fatalf("audit output = %q", got)
	}
}
