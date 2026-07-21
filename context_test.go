package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompactToolOutputWritesArtifact(t *testing.T) {
	output := strings.Repeat("x", artifactInlineLimit+1)
	compact := compactToolOutput("", "test-session", 1, "bash", output)
	if !strings.Contains(compact, "artifact") {
		t.Fatalf("got %q", compact)
	}
	path := strings.Split(strings.Split(compact, " at ")[1], ";")[0]
	data, err := os.ReadFile(path)
	if err != nil || string(data) != output {
		t.Fatalf("artifact: %v", err)
	}
	os.RemoveAll("/tmp/mino/results/test-session")
}

func TestPrepareToolOutputKeepsReadSliceInline(t *testing.T) {
	output := strings.Repeat("x", artifactInlineLimit+1)
	got := prepareToolOutput("", "test-session", 1, "read_file", output)
	if got != output {
		t.Fatalf("read_file was compacted: %q", got)
	}
}

func TestReadFileReturnsRequestedInlineSlice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.txt")
	content := strings.Repeat("a", 700) + "TARGET" + strings.Repeat("b", 700)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	got := makeReadTool().Fn(map[string]any{"path": path, "offset": float64(650), "limit": float64(100)})
	if !strings.Contains(got, "TARGET") || strings.Contains(got, "[artifact:") {
		t.Fatalf("read slice = %q", got)
	}
}

func TestContextMessagesKeepsTailOnly(t *testing.T) {
	s := &Session{history: []Message{
		{Role: "user", Content: "goal"}, {Role: "assistant", Content: "ack"},
		{Role: "user", Content: strings.Repeat("m", 100)}, {Role: "assistant", Content: "middle"},
		{Role: "user", Content: "tail question"}, {Role: "assistant", Content: "tail answer"},
	}}
	got := s.ContextMessages(120)
	joined := ""
	for _, m := range got {
		joined += m.Content
	}
	// most recent tail should be present; old head should not be forced
	if !strings.Contains(joined, "tail question") || !strings.Contains(joined, "tail answer") || !strings.Contains(joined, "compacted") {
		t.Fatalf("context = %q", joined)
	}
	if strings.Contains(joined, "goal") {
		t.Fatalf("stale first exchange should not be forced into context: %q", joined)
	}
	if len(joined) > 120 {
		t.Fatalf("context exceeded budget: %d", len(joined))
	}
}

func TestContextForBoundsCurrentInputAndKeepsArtifactCatalog(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	settings := &Settings{Home: home, ContextChars: 12000}
	mem := NewMemory(db, nil, settings)
	artifactPath := filepath.Join(home, "old-result.txt")
	if err := os.WriteFile(artifactPath, []byte("old result"), 0600); err != nil {
		t.Fatal(err)
	}
	mem.RecordArtifact("test-session", "bash", artifactPath, 10)
	s := NewSession(settings, mem)
	s.sessionID = "test-session"
	s.history = []Message{
		{Role: "user", Content: "HEAD=orchid"}, {Role: "assistant", Content: "ack"},
		{Role: "user", Content: strings.Repeat("x", 140000)}, {Role: "assistant", Content: "middle"},
		{Role: "user", Content: "TAIL=kuala-lumpur"}, {Role: "assistant", Content: "ack"},
	}
	system := strings.Repeat("s", 500)
	messages, userContext := s.ContextFor(system, strings.Repeat("u", 30000))
	total := len(system)
	joined := ""
	for _, message := range messages {
		total += len(message.Content)
		joined += message.Content
	}
	if total > settings.ContextChars {
		t.Fatalf("context exceeded budget: %d > %d", total, settings.ContextChars)
	}
	if !strings.Contains(userContext, "large user input") || !strings.Contains(joined, artifactPath) {
		t.Fatalf("context lost input or catalog: %q", joined)
	}
	os.RemoveAll(filepath.Join("/tmp/mino/results", "test-session"))
}

func TestArtifactFromOutput(t *testing.T) {
	got, ok := artifactFromOutput("[artifact: bash → 1234 chars at /tmp/mino/results/s/1/bash.txt; use read_file with offset and limit]")
	if !ok || got.Label != "bash" || got.Size != 1234 || !strings.Contains(got.Path, "bash.txt") {
		t.Fatalf("artifact = %#v, ok=%v", got, ok)
	}
}
