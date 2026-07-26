package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
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

func TestStopMessageVariants(t *testing.T) {
	for _, message := range []string{"stop", "ok mino stop.", "mino, cancel!", "never mind?"} {
		if !isStopMessage(message) {
			t.Errorf("isStopMessage(%q) = false", message)
		}
	}
	if isStopMessage("mino are you there") {
		t.Fatal("ordinary conversation was classified as stop")
	}
}

func TestTelegramNotificationContextSurvivesRestart(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	settings := &Settings{Home: home, ContextChars: 100000}
	mem := NewMemory(db, nil, settings)
	first := NewSessionManager(settings, mem).Get("tg:42")
	first.Session.AddNotification("Gmail cleanup found 20 promotional emails.")

	restored := NewSessionManager(settings, mem).Get("tg:42")
	if len(restored.Session.history) != 2 || !strings.Contains(restored.Session.history[1].Content, "Gmail cleanup") {
		t.Fatalf("notification context was not restored: %#v", restored.Session.history)
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


// Images must reach the API as vision parts for the current turn only —
// leaking base64 into history would blow the context budget every turn after.

// view_image output must become vision content, never inline base64 text.

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
