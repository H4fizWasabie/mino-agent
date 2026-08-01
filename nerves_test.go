package main

import (
	"strings"
	"testing"
)

func TestDetectLoop(t *testing.T) {
	tests := []struct {
		name     string
		history  []string
		wantLoop bool
		wantMsg  string
	}{
		{"empty", []string{}, false, ""},
		{"below threshold", []string{"a", "b", "a"}, false, ""},
		{"exact threshold", []string{"a", "a", "a"}, true, "Detected 3 repeated calls to a"},
		{"above threshold", []string{"a", "a", "a", "a"}, true, "Detected 4 repeated calls to a"},
		{"mixed then loop", []string{"b", "a", "a", "a"}, true, "Detected 3 repeated calls to a"},
		{"interleaved", []string{"a", "b", "a", "b"}, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLoop, gotMsg := detectLoop(tt.history)
			if gotLoop != tt.wantLoop {
				t.Errorf("loop = %v, want %v", gotLoop, tt.wantLoop)
			}
			if gotMsg != tt.wantMsg {
				t.Errorf("msg = %q, want %q", gotMsg, tt.wantMsg)
			}
		})
	}
}

func TestDetectLoopSameNameVaryingArgs(t *testing.T) {
	// The Luna composio failure: same tool, args drifting (steps, metrics),
	// never progressing. Byte-exact matching missed it; name matching must not.
	drift := []string{
		"MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL(step DISCOVERING 0/1)",
		"MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL(step DISCOVERING 0/2)",
		"MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL(step DISCOVERING 1/2)",
		"MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL(step DISCOVERING 1/3)",
		"MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL(step DISCOVERING 2/3)",
		"MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL(step DISCOVERING 2/4)",
	}
	loop, msg := detectLoop(drift)
	if !loop || !strings.Contains(msg, "6 consecutive calls") {
		t.Fatalf("same-name loop missed: loop=%v msg=%q", loop, msg)
	}
	// five same-name calls stay quiet (legit batch reads)
	five := drift[:5]
	if loop, _ := detectLoop(five); loop {
		t.Fatalf("5 same-name calls falsely flagged")
	}
	// repeated run_playbook (Luna's other pattern) also caught
	runs := []string{"run_playbook(gmail)", "run_playbook(gmail)", "run_playbook(gmail)",
		"run_playbook(gmail)", "run_playbook(gmail)", "run_playbook(gmail)"}
	if loop, _ := detectLoop(runs); !loop {
		t.Fatalf("repeated run_playbook missed")
	}
	// interleaved names never flag
	mixed := []string{"a(1)", "b(1)", "a(2)", "b(2)", "a(3)", "b(3)"}
	if loop, _ := detectLoop(mixed); loop {
		t.Fatalf("interleaved names falsely flagged")
	}
}
