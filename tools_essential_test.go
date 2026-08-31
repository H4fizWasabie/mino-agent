package main

import "testing"

// CTX-013 regression: send_document must stay in the essential tool set so it
// is present in every turn's schema. Without it the model cannot see the native
// tool and falls back to bash+curl, leaking the Telegram bot token in args
// (observed 2026-08-11 — raw token in the api.telegram.org/bot<token> URL).
func TestSendDocumentIsEssential(t *testing.T) {
	if !floorToolNames["send_document"] {
		t.Fatalf("send_document must be essential; otherwise the model reverts to curl with the bot token in bash args (CTX-013)")
	}
}
