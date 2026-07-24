package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionManagerKeepsGatewayConversationAcrossRestart(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	settings := &Settings{Home: home, ContextChars: 100000}
	mem := NewMemory(db, nil, settings)
	manager := NewSessionManager(settings, mem)
	first := manager.Get("tg:42")
	first.Session.AddExchange("hello", "hello", "hi", nil, "telegram")
	if manager.Get("tg:42") != first || len(first.Session.history) != 2 {
		t.Fatal("gateway session was not retained")
	}
	if manager.Get("dashboard") == first {
		t.Fatal("gateway sessions must be isolated")
	}

	manager = NewSessionManager(settings, mem) // process restart: reload persisted history
	restored := manager.Get("tg:42")
	if len(restored.Session.history) != 2 || restored.Session.history[0].Content != "hello" {
		t.Fatalf("history was not restored: %#v", restored.Session.history)
	}
	db.Close()
}

func TestSessionListShowsGatewaySources(t *testing.T) {
	db := Connect(t.TempDir())
	defer db.Close()
	for _, row := range []struct{ session, source string }{
		{"dashboard:1", "dashboard"},
		{"tg:42", "telegram"},
		{"tg:42", "telegram"},
	} {
		if _, err := db.Exec("INSERT INTO chat_log (role, content, session_id, source) VALUES ('user', 'hello', ?, ?)", row.session, row.source); err != nil {
			t.Fatal(err)
		}
	}

	sessions := sessionList(db)
	byID := map[string]map[string]any{}
	for _, session := range sessions {
		byID[session["id"].(string)] = session
	}
	if !reflect.DeepEqual(byID["dashboard:1"]["sources"], []string{"dashboard"}) {
		t.Fatalf("dashboard source missing: %#v", byID["dashboard:1"])
	}
	if !reflect.DeepEqual(byID["tg:42"]["sources"], []string{"telegram"}) {
		t.Fatalf("telegram source missing: %#v", byID["tg:42"])
	}
}

func TestAddExchangePersistsFullToolArguments(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	settings := &Settings{Home: home, ContextChars: 100000}
	mem := NewMemory(db, nil, settings)
	session := NewSession(settings, mem)
	session.Switch("full-args")
	content := strings.Repeat("payload-", 120) + "tail-marker"
	session.AddExchange("write it", "write it", "done", []ToolCall{{
		Name: "write_file", Args: map[string]any{"path": "/tmp/file", "content": content}, Output: "ok",
	}}, "test")
	var persisted string
	if err := db.QueryRow("SELECT content FROM chat_log WHERE session_id = 'full-args' AND role = 'assistant'").Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(persisted, "tail-marker") {
		t.Fatal("persisted tool arguments were truncated")
	}
}

// The old Telegram/dashboard race was fixed by funneling every gateway through
// RespondFor with a per-conversation mutex. This pins that guarantee.
func TestRespondForSerializesSameSession(t *testing.T) {
	var inFlight, maxSeen atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		if n > maxSeen.Load() {
			maxSeen.Store(n)
		}
		time.Sleep(20 * time.Millisecond) // widen the race window
		inFlight.Add(-1)
		fmt.Fprint(w, openAICompletionJSON("ok"))
	}))
	defer ts.Close()

	s := &Settings{Home: t.TempDir(), MaxIter: 3, MaxTokens: 100, ContextChars: 100000, TopK: 4}
	s.EnsureHome()
	db := Connect(s.Home)
	defer db.Close()
	mem := NewMemory(db, nil, s)
	pm := &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, Model: "m"}},
		clients:   map[string]*Client{"fake": NewClient("k", ts.URL)},
		state:     map[string]*providerState{"fake": {}},
		sticky:    map[string]string{}, now: time.Now, sleep: func(time.Duration) {},
	}
	w := &Core{Settings: s, DB: db, Client: pm, Memory: mem,
		Tools: NewRegistry(), Sessions: NewSessionManager(s, mem)}

	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := w.RespondFor("same", fmt.Sprintf("msg %d", i), "test", nil, false); r.Reply != "ok" {
				t.Errorf("reply = %q", r.Reply)
			}
		}()
	}
	wg.Wait()
	if maxSeen.Load() != 1 {
		t.Fatalf("same-session turns overlapped: max in-flight LLM calls = %d", maxSeen.Load())
	}
	if got := len(w.Sessions.Get("same").Session.history); got != 8 {
		t.Fatalf("history = %d messages, want 8 (4 serialized exchanges)", got)
	}
}

func TestCancelTurnInterruptsProviderRequest(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer ts.Close()

	s := &Settings{Home: t.TempDir(), MaxIter: 3, MaxTokens: 100, ContextChars: 100000, TopK: 4}
	s.EnsureHome()
	db := Connect(s.Home)
	defer db.Close()
	mem := NewMemory(db, nil, s)
	w := &Core{Settings: s, DB: db, Client: fakePM(ts.URL), Memory: mem,
		Tools: NewRegistry(), Sessions: NewSessionManager(s, mem)}

	done := make(chan *LoopResult, 1)
	go func() {
		done <- w.RespondFor("cancel-me", "Investigate this", "test", nil, false)
	}()
	<-started
	if !w.CancelTurn("cancel-me") {
		t.Fatal("active turn was not cancellable")
	}
	select {
	case result := <-done:
		if result.Status != "cancelled" || result.Reply != "Stopped." {
			t.Fatalf("result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled provider request did not return")
	}
	close(release)
}

// Images must reach the API as vision parts for the current turn only —
// leaking base64 into history would blow the context budget every turn after.
func TestImagesAttachToCurrentTurnOnly(t *testing.T) {
	var bodies []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		fmt.Fprint(w, openAICompletionJSON("ok"))
	}))
	defer ts.Close()

	s := &Settings{Home: t.TempDir(), MaxIter: 3, MaxTokens: 100, ContextChars: 100000, TopK: 4}
	s.EnsureHome()
	db := Connect(s.Home)
	defer db.Close()
	mem := NewMemory(db, nil, s)
	w := &Core{Settings: s, DB: db, Client: fakePM(ts.URL), Memory: mem,
		Tools: NewRegistry(), Sessions: NewSessionManager(s, mem)}

	w.RespondFor("v", "what is in this photo?", "test", nil, false, "data:image/png;base64,AAAA")
	w.RespondFor("v", "thanks", "test", nil, false)

	if !strings.Contains(bodies[0], `"image_url"`) || !strings.Contains(bodies[0], "base64,AAAA") {
		t.Fatalf("turn 1 request missing vision part: %.300s", bodies[0])
	}
	if strings.Contains(bodies[1], "AAAA") {
		t.Fatal("image leaked into history on turn 2")
	}
}

// view_image output must become vision content, never inline base64 text.
func TestViewImageBecomesVisionContent(t *testing.T) {
	img := filepath.Join(t.TempDir(), "page.png")
	os.WriteFile(img, []byte("fakepng"), 0600)

	var bodies []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) == 1 {
			args, _ := json.Marshal(fmt.Sprintf(`{"path":%q}`, img)) // JSON-in-JSON needs escaping
			fmt.Fprintf(w, `{"choices":[{"message":{"content":"","tool_calls":[{"id":"1","function":{"name":"view_image","arguments":%s}}]},"finish_reason":"tool_calls"}],"usage":{}}`, args)
			return
		}
		fmt.Fprint(w, openAICompletionJSON("a scanned invoice"))
	}))
	defer ts.Close()

	reg := NewRegistry()
	reg.Register(makeViewImageTool())
	home := t.TempDir()
	result := RunLoop(fakePM(ts.URL), "s", "sys", []Message{{Role: "user", Content: "read the scan"}}, reg, 3, 100, nil, false, nil, home, nil)

	if result.Reply != "a scanned invoice" {
		t.Fatalf("reply = %q", result.Reply)
	}
	if !strings.Contains(bodies[1], `"image_url"`) || !strings.Contains(bodies[1], "data:image/png;base64,") {
		t.Fatalf("image not attached as vision content: %.300s", bodies[1])
	}
	if out := result.ToolCalls[0].Output; strings.Contains(out, "base64") {
		t.Fatalf("tool output leaked base64 into text/logs: %.100s", out)
	}
}

func fakePM(url string) *ProviderManager {
	return &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, Model: "m"}},
		clients:   map[string]*Client{"fake": NewClient("k", url)},
		state:     map[string]*providerState{"fake": {}},
		sticky:    map[string]string{}, now: time.Now, sleep: func(time.Duration) {},
	}
}

func openAICompletionJSON(reply string) string {
	args, _ := json.Marshal(map[string]string{"status": "complete", "reply": reply})
	encoded, _ := json.Marshal(string(args))
	return fmt.Sprintf(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"finish","function":{"name":"complete_task","arguments":%s}}]},"finish_reason":"tool_calls"}],"usage":{}}`, encoded)
}
