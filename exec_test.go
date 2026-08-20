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

// ARCH-001 (#290): the stage tool boundary — mino exec refuses tools outside
// the stage whitelist (exported as MINO_EXEC_ALLOWED_TOOLS by the loop). The
// subprocess cannot reach the full registry from inside a stage script.

func TestExecToolAllowed(t *testing.T) {
	cases := []struct {
		tool, whitelist string
		want            bool
	}{
		{"read_file", "read_file,write_file,bash", true},
		{"bash", "read_file,write_file,bash", true},
		{"manage_playbook", "read_file,write_file,bash", false},
		{"run_playbook", "read_file,write_file,bash", false},
		{"read_file", "", false}, // empty never reaches here — caller checks env != "" first
		{"read_file", "read_file", true},
		{"search_web", "read_file", false},
	}
	for _, tc := range cases {
		if got := execToolAllowed(tc.tool, tc.whitelist); got != tc.want {
			t.Fatalf("execToolAllowed(%q, %q) = %v, want %v", tc.tool, tc.whitelist, got, tc.want)
		}
	}
}

func TestExecRefusesToolOutsideStageWhitelist(t *testing.T) {
	t.Setenv("MINO_EXEC_ALLOWED_TOOLS", "read_file,write_file,bash")
	r := NewRegistry()
	r.Register(&Tool{Name: "manage_playbook", Fn: func(map[string]any) string { return "should never run" }})
	w := &Core{Tools: r}
	if code := runExecTool(w, []string{"manage_playbook", "{}"}); code != 1 {
		t.Fatalf("mino exec manage_playbook exit = %d, want 1 (refused by stage boundary)", code)
	}
}

func TestExecAllowsWhitelistedTool(t *testing.T) {
	t.Setenv("MINO_EXEC_ALLOWED_TOOLS", "read_file")
	r := NewRegistry()
	r.Register(&Tool{Name: "read_file", Fn: func(map[string]any) string { return "ok" }})
	w := &Core{Tools: r}
	if code := runExecTool(w, []string{"read_file", `{"path": "/x"}`}); code != 0 {
		t.Fatalf("whitelisted tool exit = %d, want 0", code)
	}
}

func TestExecUnrestrictedWithoutEnv(t *testing.T) {
	t.Setenv("MINO_EXEC_ALLOWED_TOOLS", "")
	r := NewRegistry()
	r.Register(&Tool{Name: "manage_playbook", Fn: func(map[string]any) string { return "chat ok" }})
	w := &Core{Tools: r}
	if code := runExecTool(w, []string{"manage_playbook", "{}"}); code != 0 {
		t.Fatalf("unrestricted exec exit = %d, want 0 (chat keeps full registry)", code)
	}
}
