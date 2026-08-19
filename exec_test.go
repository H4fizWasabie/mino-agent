package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SCR-001: `mino exec` is the stub layer — one binary front door for
// playbook scripts. The exit contract is binary (0 clean / 1 failed) and
// every call lands in tool_calls + audit under the run's session.

func TestExecFailed(t *testing.T) {
	cases := []struct {
		out  string
		want bool
	}{
		{"Error: unknown tool 'x'", true},
		{"Error: invalid arguments for ping", true},
		{"pong", false},
		{"Error without colon is a value, not a failure", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := execFailed(tc.out); got != tc.want {
			t.Fatalf("execFailed(%q) = %v, want %v", tc.out, got, tc.want)
		}
	}
}

func TestExecSession(t *testing.T) {
	if got := execSession(); got != "cli-exec" {
		t.Fatalf("execSession without env = %q, want cli-exec", got)
	}
	t.Setenv("MINO_EXEC_SESSION", "scheduled-weekly-audit")
	if got := execSession(); got != "scheduled-weekly-audit" {
		t.Fatalf("execSession with env = %q", got)
	}
}

func TestRunExecToolRecordsCallUnderSession(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	r := NewRegistry()
	r.SetLogDB(db)
	if err := r.SetAuditLog(filepath.Join(home, "audit.jsonl")); err != nil {
		t.Fatal(err)
	}
	r.Register(&Tool{Name: "ping", Fn: func(args map[string]any) string { return "pong" }})
	w := &Core{Tools: r}

	t.Setenv("MINO_EXEC_SESSION", "scheduled-test")
	if code := runExecTool(w, []string{"ping", "{}"}); code != 0 {
		t.Fatalf("clean exec exit = %d, want 0", code)
	}
	var sid string
	if err := db.QueryRow("SELECT session_id FROM tool_calls ORDER BY id DESC LIMIT 1").Scan(&sid); err != nil {
		t.Fatal(err)
	}
	if sid != "scheduled-test" {
		t.Fatalf("tool_calls session = %q, want scheduled-test", sid)
	}
	data, err := os.ReadFile(filepath.Join(home, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"session_id":"scheduled-test"`) || !strings.Contains(string(data), `"tool_name":"ping"`) {
		t.Fatalf("exec call missing from audit log:\n%s", data)
	}
}

func TestRunExecToolFailures(t *testing.T) {
	w := &Core{Tools: NewRegistry()}
	if code := runExecTool(w, []string{}); code != 1 {
		t.Fatalf("no args exit = %d, want 1", code)
	}
	if code := runExecTool(w, []string{"nope"}); code != 1 {
		t.Fatalf("unknown tool exit = %d, want 1", code)
	}
	if code := runExecTool(w, []string{"nope", "not json"}); code != 1 {
		t.Fatalf("bad args-json exit = %d, want 1", code)
	}
}