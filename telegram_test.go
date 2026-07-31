package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestTelegramChatAllowlistFailsClosed(t *testing.T) {
	settings := &Settings{TelegramChatID: 42}
	for _, tc := range []struct {
		name string
		id   int64
		want bool
	}{
		{"owner", 42, true},
		{"other chat", 7, false},
		{"unset owner", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := telegramChatAllowed(settings, tc.id); got != tc.want {
				t.Fatalf("telegramChatAllowed(%d) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
	if telegramChatAllowed(&Settings{}, 42) {
		t.Fatal("Telegram must reject all chats when the owner ID is unset")
	}
}

func TestTelegramContentIncludesReplyContext(t *testing.T) {
	message := &tgbotapi.Message{
		Text: "Delete",
		ReplyToMessage: &tgbotapi.Message{
			Text: "Gmail cleanup found 20 promotional emails.",
		},
	}
	got, images := telegramContent(nil, nil, "tg:42", message)
	if images != nil {
		t.Fatalf("images = %#v, want nil", images)
	}
	if !strings.Contains(got, "Delete") || !strings.Contains(got, "Gmail cleanup found 20 promotional emails.") {
		t.Fatalf("reply context missing: %q", got)
	}
}

func TestTelegramDashboardEnabledWhenPortConfigured(t *testing.T) {
	t.Setenv("MINO_DASHBOARD_PORT", "7779")
	if !telegramDashboardEnabled() {
		t.Fatal("dashboard should run alongside Telegram when a port is configured")
	}
}

func TestDeliverOutboxOnceSendsAndRemoves(t *testing.T) {
	home := t.TempDir()
	outbox := filepath.Join(home, "outbox")
	os.MkdirAll(outbox, 0700)
	os.WriteFile(filepath.Join(outbox, "msg_Abah.txt"), []byte("**Hello** Abah"), 0600)
	os.WriteFile(filepath.Join(outbox, "msg_other.txt"), []byte("second"), 0600)

	got := make(chan map[string]any, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		got <- p
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	telegramAPIBase = srv.URL
	defer func() { telegramAPIBase = "https://api.telegram.org" }()

	s := &Settings{Home: home, Telegram: "tok", TelegramChatID: 12345}
	if n := deliverOutboxOnce(s); n != 2 {
		t.Fatalf("delivered = %d, want 2", n)
	}
	for i := 0; i < 2; i++ {
		p := <-got
		if p["chat_id"] != float64(12345) || p["text"] == "" || p["parse_mode"] != "Markdown" {
			t.Fatalf("payload = %v", p)
		}
	}
	rest, _ := os.ReadDir(outbox)
	if len(rest) != 0 {
		t.Fatalf("outbox not drained: %v", rest)
	}
}

func TestDeliverOutboxFallsBackWithoutParseMode(t *testing.T) {
	home := t.TempDir()
	outbox := filepath.Join(home, "outbox")
	os.MkdirAll(outbox, 0700)
	os.WriteFile(filepath.Join(outbox, "msg_Abah.txt"), []byte("**unbalanced markdown"), 0600)

	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		attempts++
		if p["parse_mode"] != nil {
			w.Write([]byte(`{"ok":false}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	telegramAPIBase = srv.URL
	defer func() { telegramAPIBase = "https://api.telegram.org" }()

	s := &Settings{Home: home, Telegram: "tok", TelegramChatID: 12345}
	if n := deliverOutboxOnce(s); n != 1 {
		t.Fatalf("delivered = %d, want 1", n)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (markdown then plain)", attempts)
	}
	if rest, _ := os.ReadDir(outbox); len(rest) != 0 {
		t.Fatalf("outbox not drained after fallback")
	}
}
