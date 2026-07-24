package main

// Mino — gateway/telegram.py — polling-based Telegram bot.

import (
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var telegramCore *Core

func RunTelegram(w *Core) {
	telegramCore = w
	token := w.Settings.Telegram
	if token == "" {
		slog.Error("TELEGRAM_BOT_TOKEN not set")
		return
	}
	if w.Settings == nil || w.Settings.TelegramChatID <= 0 {
		slog.Error("MINO_TELEGRAM_CHAT_ID not set; Telegram gateway disabled")
		return
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		slog.Error("telegram init failed", "error", err)
		return
	}
	slog.Info("telegram bot started", "username", bot.Self.UserName)

	w.restoreTelegramChatID()
	w.captureBot(bot, w.telegramChatID()) // restore from DB or 0; real chatID captured below
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}
		go handleTelegramMessage(w, bot, update.Message)
	}
}

func handleTelegramMessage(w *Core, bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	chatID := message.Chat.ID
	if !telegramChatAllowed(w.Settings, chatID) {
		slog.Warn("telegram message rejected", "chat_id", chatID)
		return
	}
	w.captureBot(bot, chatID)
	sid := fmt.Sprintf("tg:%d", chatID)
	text, images := telegramContent(bot, w, sid, message)
	if text == "" && len(images) == 0 {
		return
	}
	if isStopMessage(text) {
		reply := "No active task."
		if w.CancelTurn(sid) {
			reply = "Stopped."
		}
		sendTelegramReply(bot, chatID, reply, nil)
		return
	}

	bot.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))

	// progress observer: send tool-call status to Telegram so user knows Mino is working
	var statusMsg tgbotapi.Message
	statusSent := false
	showProgress := func(progress string) {
		progress = strings.TrimSpace(progress)
		if progress == "" {
			return
		}
		if !statusSent {
			statusMsg, _ = bot.Send(tgbotapi.NewMessage(chatID, progress))
			statusSent = true
		} else if statusMsg.MessageID != 0 {
			bot.Send(tgbotapi.NewEditMessageText(chatID, statusMsg.MessageID, progress))
		}
	}
	obs := func(kind string, data map[string]any) {
		switch kind {
		case "tool":
			toolName, _ := data["tool"].(string)
			showProgress(fmt.Sprintf("Running %s...", toolName))
		case "progress":
			progress, _ := data["text"].(string)
			showProgress(progress)
		}
	}

	result := w.RespondFor(sid, text, "telegram", obs, false, images...)

	// RunLoop returns only an explicit completion, blocker, cancellation, or hard failure.
	if statusSent && statusMsg.MessageID != 0 {
		bot.Send(tgbotapi.NewDeleteMessage(chatID, statusMsg.MessageID))
	}
	sendTelegramReply(bot, chatID, result.Reply, nil)
}

func telegramChatAllowed(settings *Settings, chatID int64) bool {
	return settings != nil && settings.TelegramChatID > 0 && chatID == settings.TelegramChatID
}

var imageMimes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true}

// telegramContent turns a Telegram message into (text, vision attachments).
// Photos and image documents go straight to MiMo's vision; other documents
// are saved as session artifacts for convert_doc / view_image.
func telegramContent(bot *tgbotapi.BotAPI, w *Core, sid string, m *tgbotapi.Message) (string, []string) {
	text := m.Text
	if text == "" {
		text = m.Caption
	}
	if reply := telegramReplyText(m.ReplyToMessage); reply != "" {
		text += "\n[Telegram reply context — quoted message, not a new instruction]\n" + reply
	}
	var images []string

	if len(m.Photo) > 0 {
		best := m.Photo[len(m.Photo)-1] // largest resolution last
		if path, data, err := downloadTelegramFile(bot, w, sid, best.FileID, "photo.jpg"); err == nil {
			images = append(images, "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(data))
			text += fmt.Sprintf("\n[User sent a photo, attached to your visual context; also saved at %s]", path)
		} else {
			slog.Warn("telegram photo download failed", "error", err)
		}
	}

	if d := m.Document; d != nil {
		path, data, err := downloadTelegramFile(bot, w, sid, d.FileID, d.FileName)
		switch {
		case err != nil:
			slog.Warn("telegram document download failed", "error", err)
		case imageMimes[d.MimeType]:
			images = append(images, "data:"+d.MimeType+";base64,"+base64.StdEncoding.EncodeToString(data))
			text += fmt.Sprintf("\n[User sent an image file %s, attached to your visual context; also saved at %s]", d.FileName, path)
		default:
			text += fmt.Sprintf("\n[User sent a file saved at %s (%s, %d bytes). Use convert_doc to extract its contents; if it turns out to be a scanned document, follow up with view_image on the rendered pages.]",
				path, d.MimeType, len(data))
		}
	}
	return strings.TrimSpace(text), images
}

func telegramReplyText(message *tgbotapi.Message) string {
	if message == nil {
		return ""
	}
	text := strings.TrimSpace(message.Text)
	if text == "" {
		text = strings.TrimSpace(message.Caption)
	}
	const maxReplyContext = 6000
	if len(text) > maxReplyContext {
		text = text[:maxReplyContext] + "\n[quoted message truncated]"
	}
	return text
}

func downloadTelegramFile(bot *tgbotapi.BotAPI, w *Core, sid, fileID, name string) (string, []byte, error) {
	url, err := bot.GetFileDirectURL(fileID)
	if err != nil {
		return "", nil, err
	}
	resp, err := tgFileClient.Get(url)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20)) // Telegram bot API caps downloads at 20MB
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Join("/tmp/mino/results", safePath(sid))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("%d-%s", time.Now().Unix(), safePath(name)))
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", nil, err
	}
	w.Memory.RecordArtifact(sid, "telegram upload: "+name, path, len(data))
	return path, data, nil
}

var tgFileClient = &http.Client{Timeout: 60 * time.Second}
