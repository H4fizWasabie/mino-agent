package main

import "testing"

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
