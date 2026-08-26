package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
	os.WriteFile(filepath.Join(outbox, "msg_Owner.txt"), []byte("**Hello** Owner"), 0600)
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

func TestDeliverOutboxChunksOversizedMessage(t *testing.T) {
	home := t.TempDir()
	outbox := filepath.Join(home, "outbox")
	os.MkdirAll(outbox, 0700)
	os.WriteFile(filepath.Join(outbox, "msg_Owner.txt"), []byte(strings.Repeat("x", telegramTextChunkLimit*2+1)), 0600)

	var chunks []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		chunks = append(chunks, p["text"].(string))
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	telegramAPIBase = srv.URL
	defer func() { telegramAPIBase = "https://api.telegram.org" }()

	if n := deliverOutboxOnce(&Settings{Home: home, Telegram: "tok", TelegramChatID: 12345}); n != 1 {
		t.Fatalf("delivered = %d, want 1 file", n)
	}
	if len(chunks) != 3 {
		t.Fatalf("chunks = %d, want 3", len(chunks))
	}
	for i, chunk := range chunks {
		if got := len([]rune(chunk)); got > telegramTextChunkLimit {
			t.Fatalf("chunk %d has %d runes, want <= %d", i, got, telegramTextChunkLimit)
		}
	}
	if rest, _ := os.ReadDir(outbox); len(rest) != 0 {
		t.Fatalf("outbox not drained: %v", rest)
	}
}

// CTX-009: send_document queues a file pointer, never the token; the
// dispatcher posts it as multipart to /sendDocument and drains on success.
func TestSendDocumentOutboxDeliversAndDrains(t *testing.T) {
	home := t.TempDir()
	filePath := filepath.Join(home, "report.xlsx")
	os.WriteFile(filePath, []byte("XLSX-BYTES"), 0600)
	if err := queueDocument(home, filePath, "June report"); err != nil {
		t.Fatal(err)
	}
	if err := queueDocument(home, filepath.Join(home, "missing.txt"), ""); err == nil {
		t.Fatal("missing file must not queue")
	}

	var gotBody []byte
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	telegramAPIBase = srv.URL
	defer func() { telegramAPIBase = "https://api.telegram.org" }()

	s := &Settings{Home: home, Telegram: "tok", TelegramChatID: 12345}
	if n := deliverOutboxOnce(s); n != 1 {
		t.Fatalf("delivered = %d, want 1", n)
	}
	if !strings.HasSuffix(gotPath, "/sendDocument") {
		t.Fatalf("path = %q, want sendDocument", gotPath)
	}
	if !strings.Contains(string(gotBody), "XLSX-BYTES") || !strings.Contains(string(gotBody), "June report") {
		t.Fatalf("multipart body missing file or caption")
	}
	if strings.Contains(string(gotBody), "tok") {
		t.Fatal("bot token leaked into the request body")
	}
	rest, _ := os.ReadDir(filepath.Join(home, "outbox"))
	if len(rest) != 0 {
		t.Fatalf("outbox not drained: %v", rest)
	}
}

func TestSendDocumentToolDraftsWithoutToken(t *testing.T) {
	home := t.TempDir()
	tool := makeSendDocumentTool(home)
	filePath := filepath.Join(home, "x.pdf")
	os.WriteFile(filePath, []byte("PDF"), 0600)
	out := tool.Fn(map[string]any{"path": filePath, "caption": "hi"})
	if !strings.Contains(out, "queued") {
		t.Fatalf("tool output = %q", out)
	}
	entries, _ := os.ReadDir(filepath.Join(home, "outbox"))
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "doc_") {
		t.Fatalf("drafts = %v, want one doc_ file", entries)
	}
	data, _ := os.ReadFile(filepath.Join(home, "outbox", entries[0].Name()))
	if strings.Contains(string(data), "\"tok\"") {
		t.Fatalf("token in draft: %s", data)
	}
	if out := tool.Fn(map[string]any{"path": filepath.Join(home, "nope.pdf")}); !strings.Contains(out, "Error") {
		t.Fatalf("missing file must error, got %q", out)
	}
}

func TestDeliverOutboxFallsBackWithoutParseMode(t *testing.T) {
	home := t.TempDir()
	outbox := filepath.Join(home, "outbox")
	os.MkdirAll(outbox, 0700)
	os.WriteFile(filepath.Join(outbox, "msg_Owner.txt"), []byte("**unbalanced markdown"), 0600)

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

// issue #181: a reply containing a --- divider arrives as separate messages,
// each threaded to the previous one (the caller's message for the first).
func TestSendTelegramReplySectionsThreaded(t *testing.T) {
	type sentMsg struct {
		text             string
		replyToMessageID int
		messageID        int
	}
	var sent []sentMsg
	nextID := 100
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		nextID++
		if r.FormValue("text") == "" {
			w.Write([]byte(`{"ok":true,"result":{"message_id":` + fmt.Sprint(nextID) + `,"chat":{"id":1},"date":1}}`))
			return // tgbotapi's own getMe request — not a reply section
		}
		id, _ := strconv.Atoi(r.FormValue("reply_to_message_id"))
		sent = append(sent, sentMsg{r.FormValue("text"), id, nextID})
		w.Write([]byte(`{"ok":true,"result":{"message_id":` + fmt.Sprint(nextID) + `,"chat":{"id":1},"date":1}}`))
	}))
	defer srv.Close()
	bot, err := tgbotapi.NewBotAPIWithAPIEndpoint("test-token", srv.URL+"/bot%s/%s")
	if err != nil {
		t.Fatal(err)
	}

	sendTelegramReply(bot, 42, "first section\n---\nsecond section", nil, 7)

	if len(sent) != 2 {
		t.Fatalf("sent %d messages, want 2: %+v", len(sent), sent)
	}
	if sent[0].replyToMessageID != 7 {
		t.Fatalf("first section reply_to = %d, want the caller's message 7", sent[0].replyToMessageID)
	}
	if sent[1].replyToMessageID != sent[0].messageID {
		t.Fatalf("second section reply_to = %d, want first section's message id %d", sent[1].replyToMessageID, sent[0].messageID)
	}
	if sent[0].text != "first section" || sent[1].text != "second section" {
		t.Fatalf("section texts = %+v", sent)
	}
}
