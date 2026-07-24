package main

import (
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
