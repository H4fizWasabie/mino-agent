package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newTestSessionWithFakeLLM(t *testing.T, response *string) *Session {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}],"usage":{}}`, *response)
	}))
	t.Cleanup(ts.Close)

	home := t.TempDir()
	db := Connect(home)
	t.Cleanup(func() { db.Close() })
	cfg := &Settings{Home: home, MemoriesDir: filepath.Join(home, "memories"), MaxHistoryTurns: 2, TopK: 4}
	mem := NewMemory(db, &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, Model: "m"}},
		clients:   map[string]*Client{"fake": NewClient("k", ts.URL)},
		state:     map[string]*providerState{"fake": {}},
		sticky:    map[string]string{}, now: time.Now,
	}, cfg)
	return &Session{settings: cfg, mem: mem, sessionID: "open-loops-test", history: []Message{
		{Role: "user", Content: "turn1-q, remind me the invoice number is INV-4471"},
		{Role: "assistant", Content: "turn1-a, noted, will follow up on INV-4471"},
		{Role: "user", Content: "turn2-q"}, {Role: "assistant", Content: "turn2-a"},
		{Role: "user", Content: "turn3-q"}, {Role: "assistant", Content: "turn3-a"},
	}}
}

func waitForOpenLoopsIdle(t *testing.T, s *Session) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.openLoopsMu.Lock()
		idle := !s.openLoopsInFlight
		s.openLoopsMu.Unlock()
		if idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("open-loops extraction never finished")
}

// #405: an unresolved item in a turn about to fall out of the compacted
// prompt is captured as a durable memory fact before compaction drops it,
// and raw chat history (s.history) is left completely unchanged.
func TestContextMessagesExtractsOpenLoopsBeforeCompaction(t *testing.T) {
	response := `{"loops":["Follow up on invoice INV-4471"]}`
	s := newTestSessionWithFakeLLM(t, &response)
	originalLen := len(s.history)

	s.ContextMessages(100000) // triggers compaction + async extraction

	waitForOpenLoopsIdle(t, s)

	if len(s.history) != originalLen {
		t.Fatalf("raw history mutated: len=%d, want %d", len(s.history), originalLen)
	}

	fact, ok := s.mem.graph.FindFact(fmt.Sprintf("open_loop_%s_%d", s.sessionID, 2))
	if !ok {
		t.Fatal("expected open-loops fact to be written")
	}
	if !strings.Contains(fact.Body, "INV-4471") {
		t.Fatalf("fact body missing captured identifier: %q", fact.Body)
	}
	if fact.Tier != "open_loop" {
		t.Fatalf("fact tier = %q, want open_loop", fact.Tier)
	}

	s.openLoopsMu.Lock()
	boundary := s.openLoopsThrough
	s.openLoopsMu.Unlock()
	if boundary != 2 {
		t.Fatalf("boundary = %d, want 2 (advances only on success)", boundary)
	}
}

// #405: a failed/garbage extraction must never mark the dropped span as
// processed — chat history stays untouched and the same span is retried on
// the next compaction rather than silently losing the open loop.
func TestContextMessagesOpenLoopsExtractionFailureDoesNotAdvanceBoundary(t *testing.T) {
	response := "not json at all"
	s := newTestSessionWithFakeLLM(t, &response)
	originalHistory := append([]Message(nil), s.history...)

	s.ContextMessages(100000)
	waitForOpenLoopsIdle(t, s)

	s.openLoopsMu.Lock()
	boundary := s.openLoopsThrough
	s.openLoopsMu.Unlock()
	if boundary != 0 {
		t.Fatalf("boundary advanced on failed extraction: %d", boundary)
	}
	if len(s.history) != len(originalHistory) {
		t.Fatalf("raw history mutated on failure: len=%d, want %d", len(s.history), len(originalHistory))
	}
	for i, m := range s.history {
		if !reflect.DeepEqual(m, originalHistory[i]) {
			t.Fatalf("history[%d] changed: %+v vs %+v", i, m, originalHistory[i])
		}
	}

	// Retried on the next compaction call: a valid response now succeeds.
	response = `{"loops":["Follow up on invoice INV-4471"]}`
	s.ContextMessages(100000)
	waitForOpenLoopsIdle(t, s)
	s.openLoopsMu.Lock()
	boundary = s.openLoopsThrough
	s.openLoopsMu.Unlock()
	if boundary != 2 {
		t.Fatalf("retry did not advance boundary: %d", boundary)
	}
}
