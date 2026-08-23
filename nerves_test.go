package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	// enumeration over distinct entities reuses the tool but is progress:
	// 7 manage_playbook inspections of different playbooks must stay quiet
	// (2026-08-06 audit false positive: 7 consecutive calls flagged).
	audit := []string{
		"manage_playbook(map[action:inspect name:ai-news-daily])",
		"manage_playbook(map[action:inspect name:facebook-daily-ai-post])",
		"manage_playbook(map[action:inspect name:gmail-daily-cleanup])",
		"manage_playbook(map[action:inspect name:instagram-daily-capability])",
		"manage_playbook(map[action:inspect name:malaysian-news-daily])",
		"manage_playbook(map[action:inspect name:threads-ai-learning])",
		"manage_playbook(map[action:inspect name:threads-daily-capability])",
	}
	if loop, msg := detectLoop(audit); loop {
		t.Fatalf("distinct-entity enumeration falsely flagged: %s", msg)
	}
}

// CTX-012: an interrupt whose model response is tool-call-only must fall back
// to a snapshot status line instead of "(no response)"; a text response
// passes through. The interrupt call carries no schemas, so the model cannot
// emit native tool calls at all.
func TestInterruptFallsBackWhenModelAnswersWithToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"1","function":{"name":"system_check","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()
	t.Setenv("MINO_TEST_KEY", "k")
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "providers.json"), []byte(`{"providers":[{"name":"t","priority":1,"base_url":"`+srv.URL+`","api_key_env":"MINO_TEST_KEY","model":"test-model"}]}`), 0600)
	pm, err := NewProviderManager(home, &Settings{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	core := &Core{Settings: &Settings{Home: home}, Client: pm, Sessions: NewSessionManager(&Settings{Home: home}, nil)}
	core.startLoop("s")
	core.snapshotUpdater("s")(LoopSnapshot{Iteration: 3, Status: "running_tool", CurrentTool: "bash", ToolHistory: []string{"bash(ls)"}})

	var reply string
	core.handleInterrupt("s", "status", func(r string) { reply = r })
	if strings.Contains(reply, "no response") || !strings.Contains(reply, "iteration 3") {
		t.Fatalf("fallback reply = %q, want snapshot status", reply)
	}
}

func TestInterruptTextReplyPassesThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"On iteration 2, running bash"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()
	t.Setenv("MINO_TEST_KEY", "k")
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "providers.json"), []byte(`{"providers":[{"name":"t","priority":1,"base_url":"`+srv.URL+`","api_key_env":"MINO_TEST_KEY","model":"test-model"}]}`), 0600)
	pm, err := NewProviderManager(home, &Settings{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	core := &Core{Settings: &Settings{Home: home}, Client: pm, Sessions: NewSessionManager(&Settings{Home: home}, nil)}
	core.startLoop("s")

	var reply string
	core.handleInterrupt("s", "status", func(r string) { reply = r })
	if !strings.Contains(reply, "On iteration 2") {
		t.Fatalf("reply = %q, want the model's text", reply)
	}
}
