package main

import (
	"context"
	"database/sql"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// Mino — app.py — wires everything together.
// This is the assembly diagram in code.

type Core struct {
	notifyMu       sync.RWMutex
	notifyTelegram func(result *LoopResult)
	notifyChatID   int64
	Settings       *Settings
	DB             *sql.DB
	Client         *ProviderManager
	AuthStore      *AuthStore
	OAuth          *OAuthEngine
	Memory         *Memory
	Tools          *Registry
	Sessions       *SessionManager
	Scheduler      *Scheduler
}

func NewCore() *Core {
	s := LoadSettings()
	s.EnsureHome()
	seedBuiltinSkills(s.Home)
	CleanupArtifacts(24 * time.Hour)

	db := Connect(s.Home)
	authStore := LoadAuthStore(s.Home)
	client, err := NewProviderManager(s.Home, s, authStore)
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
	tools := BuildRegistry(db, s.Home, mem, s.Location())
	tools.SetMaxDescChars(s.MaxToolDescChars)
	LoadExtensions(s.Home, tools) // discover + register extension tools

	if s.ConsolidateEvery > 0 {
		go func() { // 6-hour full consolidation pass
			for {
				if n := mem.ConsolidateDue(); n > 0 {
					slog.Info("consolidation", "new_facts", n)
				}
				time.Sleep(6 * time.Hour)
			}
		}()
		go func() { // 5-minute threshold check — triggers when context nears 80% full
			for {
				if n := mem.ConsolidateIfFull(s.ContextChars); n > 0 {
					slog.Info("consolidation (threshold)", "new_facts", n)
				}
				time.Sleep(5 * time.Minute)
			}
		}()
	}

	dashHost := os.Getenv("MINO_DASHBOARD_HOST")
	dashPort := envOr("MINO_DASHBOARD_PORT", "7777")
	if dashHost == "" {
		dashHost = "127.0.0.1"
	}
	redirectBase := os.Getenv("MINO_PUBLIC_URL")
	if redirectBase == "" {
		redirectBase = "http://" + dashHost + ":" + dashPort
	}
	oauthEngine := LoadOAuthEngine(s.Home, authStore, redirectBase)

	w := &Core{
		Settings:  s,
		DB:        db,
		Client:    client,
		AuthStore: authStore,
		OAuth:     oauthEngine,
		Memory:    mem,
		Tools:     tools,
		Sessions:  NewSessionManager(s, mem),
	}
	// MCP bridge: connect configured servers and register their tools
	mcpBridge := NewMCPBridge(s.Home, tools)
	mcpBridge.Start()
	tools.Register(MakeReloadPluginsTool(s.Home, tools, mcpBridge))

	// Tool filter: use embeddings to send only relevant tools per turn
	coreTools := []string{"recall", "save_note", "read_file", "bash", "request_approval", "resolve_approval", "project_get", "project_update"}
	toolFilter := NewToolFilter(coreTools, 8) // top 8 + 8 core = max 16 tools/turn
	if mem.embedder != nil {
		toolFilter.Index(tools.Schemas(), mem.embedder)
		slog.Info("tool filter indexed", "tools", len(tools.Schemas()))
	}
	tools.SetFilter(toolFilter)

	addDelegateTools(w)

	// Scheduler: runs prompts through agent loop on schedule
	w.Scheduler = NewScheduler(s.Home, s.Location(), func(prompt string, notify bool) {
		result := w.RespondFor("scheduler", prompt, "scheduler", nil, false)
		slog.Info("scheduled job done", "id", prompt[:min(40, len(prompt))], "reply", result.Reply[:min(80, len(result.Reply))])
		if notify {
			w.sendNotification(result)
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
	if !telegramChatAllowed(w.Settings, chatID) {
		return
	}
	w.notifyMu.Lock()
	defer w.notifyMu.Unlock()
	w.notifyChatID = chatID
	w.notifyTelegram = func(result *LoopResult) {
		sendTelegramReply(bot, chatID, result.Reply, nil)
	}
}

func (w *Core) sendNotification(result *LoopResult) {
	w.notifyMu.RLock()
	notify := w.notifyTelegram
	w.notifyMu.RUnlock()
	if notify != nil {
		notify(result)
	}
}

func (w *Core) telegramChatID() int64 {
	w.notifyMu.RLock()
	defer w.notifyMu.RUnlock()
	return w.notifyChatID
}

// restoreTelegramChatID recovers the last known chat ID from DB after restart.
func (w *Core) restoreTelegramChatID() {
	if w.Settings == nil || w.Settings.TelegramChatID <= 0 {
		return
	}
	var sid string
	if err := w.DB.QueryRow("SELECT session_id FROM chat_log WHERE source = 'telegram' ORDER BY id DESC LIMIT 1").Scan(&sid); err == nil {
		var chatID int64
		if _, err := fmt.Sscanf(sid, "tg:%d", &chatID); err == nil && chatID == w.Settings.TelegramChatID {
			w.notifyMu.Lock()
			w.notifyChatID = chatID
			w.notifyMu.Unlock()
			return
		}
	}
	w.notifyMu.Lock()
	w.notifyChatID = 0
	w.notifyMu.Unlock()
}

func (w *Core) RespondFor(sessionID, userMessage, source string, obs Observer, stream bool, images ...string) *LoopResult {
	return w.RespondForContext(context.Background(), sessionID, userMessage, source, obs, stream, images...)
}

func (w *Core) RespondForContext(parent context.Context, sessionID, userMessage, source string, obs Observer, stream bool, images ...string) *LoopResult {
	conversation := w.Sessions.Get(sessionID)
	conversation.mu.Lock()
	defer conversation.mu.Unlock()
	ctx, finish := conversation.beginTurn(parent)
	defer finish()
	ctx = context.WithValue(ctx, turnMessageKey{}, userMessage)
	system := conversation.Session.BuildSystem(userMessage, source)
	// inject resume prompt if there's an active task
	if resume := conversation.Checkpoint.ResumePrompt(); resume != "" {
		system += "\n\n" + resume
	}
	logTrace(w.Settings.Home, "turn_start", map[string]any{"user_message": userMessage})
	messages, userContext := conversation.Session.ContextFor(system, userMessage)
	if len(images) > 0 {
		messages[len(messages)-1].Images = images
	}

	var es *EmbeddingStore
	if w.Memory != nil {
		es = w.Memory.embedder
	}
	result := RunLoopContext(
		ctx,
		w.Client, conversation.Session.sessionID, system, messages, w.Tools,
		w.Settings.MaxIter, w.Settings.MaxTokens, obs, stream,
		conversation.Checkpoint,
		w.Settings.Home,
		es,
	)

	conversation.Session.AddExchange(userMessage, userContext, result.Reply, result.ToolCalls, source)
	return result
}

func (w *Core) CancelTurn(sessionID string) bool {
	return w.Sessions.Get(sessionID).cancelTurn()
}

func isStopMessage(message string) bool {
	switch strings.ToLower(strings.TrimSpace(message)) {
	case "stop", "cancel", "stop task", "cancel task", "never mind", "nevermind":
		return true
	default:
		return false
	}
}

func (w *Core) Close() {
	closeTrace(w.Settings.Home)
	w.DB.Close()
}
