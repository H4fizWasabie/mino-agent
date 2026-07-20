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

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		slog.Error("telegram init failed", "error", err)
		return
	}
	slog.Info("telegram bot started", "username", bot.Self.UserName)

	w.restoreTelegramChatID()
	w.captureBot(bot, w.notifyChatID) // restore from DB or 0; real chatID captured below
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		chatID := update.Message.Chat.ID
		w.captureBot(bot, chatID) // capture for proactive notifications
		sid := fmt.Sprintf("tg:%d", chatID)
		text, images := telegramContent(bot, w, sid, update.Message)
		if text == "" && len(images) == 0 {
			continue
		}

		bot.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))

		result := w.RespondFor(sid, text, "telegram", nil, false, images...)

		sendTelegramReply(bot, chatID, result.Reply, result.ToolCalls)

		// auto-continue: if Mino hit the iteration limit (still mid-task),
		// feed "continue" back in so multi-step tasks complete without user prodding.
		// ponytail: max 10 auto-continues to prevent infinite loops.
		for auto := 0; auto < 10 && result.Iterations >= w.Settings.MaxIter; auto++ {
			slog.Info("telegram auto-continue", "auto", auto+1, "iterations", result.Iterations)
			bot.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))
			result = w.RespondFor(sid, "continue", "telegram", nil, false)
			sendTelegramReply(bot, chatID, result.Reply, result.ToolCalls)
		}
	}
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
