package main

import (
	"database/sql"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log/slog"
	"os"
	"time"
)

// Mino — app.py — wires everything together.
// This is the assembly diagram in code.

type Core struct {
	notifyTelegram func(result *LoopResult)
	notifyChatID   int64
	Settings       *Settings
	DB             *sql.DB
	Client         *ProviderManager
	Memory         *Memory
	Tools          *Registry
	Sessions       *SessionManager
	Scheduler      *Scheduler
}

func NewCore() *Core {
	s := LoadSettings()
	s.EnsureHome()
	CleanupArtifacts(24 * time.Hour)

	db := Connect(s.Home)
	client, err := NewProviderManager(s.Home, s)
	if err != nil {
		if !dashboardRequested() || !needsOnboarding(s.Home) {
			panic(err)
		}
		slog.Info("dashboard awaiting provider setup")
	}

	mem := NewMemory(db, client, s)
	mem.CleanupArtifacts()

	// embedding store (OpenRouter, Phase 3)
	embKey := os.Getenv("MINO_OPENROUTER_KEY")
	embModel := envOr("MINO_EMBED_MODEL", "openai/text-embedding-3-large")
	if embKey != "" {
		mem.embedder = NewEmbeddingStore(db, embKey, embModel)
		for _, entry := range PruneRecentFixes(s.Home, 7*24*time.Hour) {
			mem.embedder.Remove("working_memory", entry)
		}
		go mem.BackfillEmbeddings()
	} else {
		PruneRecentFixes(s.Home, 7*24*time.Hour)
	}
	mem.skills = NewSkillLoader(s.Home, mem.embedder)
	tools := BuildRegistry(db, s.Home, mem)
	LoadExtensions(s.Home, tools) // discover + register extension tools

	if s.ConsolidateEvery > 0 {
		go func() { // single loop = no consolidation races (DECISIONS.md 3)
			for {
				if n := mem.ConsolidateDue(); n > 0 {
					slog.Info("consolidation", "new_facts", n)
				}
				time.Sleep(6 * time.Hour)
			}
		}()
	}

	w := &Core{
		Settings: s,
		DB:       db,
		Client:   client,
		Memory:   mem,
		Tools:    tools,
		Sessions: NewSessionManager(s, mem),
	}
	// MCP bridge: connect configured servers and register their tools
	mcpBridge := NewMCPBridge(s.Home, tools)
	mcpBridge.Start()

	addDelegateTools(w)

	// Scheduler: runs prompts through agent loop on schedule
	w.Scheduler = NewScheduler(s.Home, func(prompt string, notify bool) {
		result := w.RespondFor("scheduler", prompt, "scheduler", nil, false)
		slog.Info("scheduled job done", "id", prompt[:min(40, len(prompt))], "reply", result.Reply[:min(80, len(result.Reply))])
		if notify && w.notifyTelegram != nil {
			w.notifyTelegram(result)
		}
	})

	return w
}

func dashboardRequested() bool {
	return os.Getenv("MINO_DASHBOARD_PORT") != "" || len(os.Args) > 1 && os.Args[1] == "dashboard"
}

func (w *Core) Respond(userMessage, source string, obs Observer, stream bool) *LoopResult {
	return w.RespondFor("default", userMessage, source, obs, stream)
}

// RespondFor runs one turn. Optional images (data URLs) attach to the current
// user message only — AddExchange persists text, so they never enter history.

func (w *Core) captureBot(bot *tgbotapi.BotAPI, chatID int64) {
	w.notifyChatID = chatID
	w.notifyTelegram = func(result *LoopResult) {
		reply := result.Reply
		if len(reply) > 4000 {
			reply = reply[:4000] + "..."
		}
		html := escapeHTML(reply)
		if len(result.ToolCalls) > 0 {
			names := make([]string, len(result.ToolCalls))
			for i, t := range result.ToolCalls {
				names[i] = t.Name
			}
			html += "\n\n<code>" + strings.Join(names, " -> ") + "</code>"
		}
		msg := tgbotapi.NewMessage(w.notifyChatID, html)
		msg.ParseMode = tgbotapi.ModeHTML
		bot.Send(msg)
	}
}

// restoreTelegramChatID recovers the last known chat ID from DB after restart.
func (w *Core) restoreTelegramChatID() {
	var sid string
	if err := w.DB.QueryRow("SELECT session_id FROM chat_log WHERE source = 'telegram' ORDER BY id DESC LIMIT 1").Scan(&sid); err == nil {
		var chatID int64
		if _, err := fmt.Sscanf(sid, "tg:%d", &chatID); err == nil && chatID > 0 {
			w.notifyChatID = chatID
			return
		}
	}
	w.notifyChatID = 0
}

func (w *Core) RespondFor(sessionID, userMessage, source string, obs Observer, stream bool, images ...string) *LoopResult {
	conversation := w.Sessions.Get(sessionID)
	conversation.mu.Lock()
	defer conversation.mu.Unlock()
	system := conversation.Session.BuildSystem(userMessage)
	// inject resume prompt if there's an active task
	if resume := conversation.Checkpoint.ResumePrompt(); resume != "" {
		system += "\n\n" + resume
	}
	logTrace(w.Settings.Home, "turn_start", map[string]any{"user_message": userMessage})
	messages, userContext := conversation.Session.ContextFor(system, userMessage)
	if len(images) > 0 {
		messages[len(messages)-1].Images = images
	}

	result := RunLoop(
		w.Client, conversation.Session.sessionID, system, messages, w.Tools,
		w.Settings.MaxIter, w.Settings.MaxTokens, obs, stream,
		conversation.Checkpoint,
		w.Settings.Home,
	)

	conversation.Session.AddExchange(userMessage, userContext, result.Reply, result.ToolCalls, source)
	// clear checkpoint if task completed (no tool calls = final reply)
	if len(result.ToolCalls) > 0 && result.Reply != "" {
		conversation.Checkpoint.Clear()
	}
	return result
}

func (w *Core) Close() {
	w.DB.Close()
}
