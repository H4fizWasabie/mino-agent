package main

import (
	"database/sql"
	"fmt"

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
		if needsOnboarding(s.Home) {
			slog.Info("dashboard awaiting provider setup")
		} else if !dashboardRequested() {
			fmt.Fprintln(os.Stderr, "Welcome to Mino! Set up your API key to get started:")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "  Option 1 — Quick start with env vars:")
			fmt.Fprintln(os.Stderr, "    export MINO_API_KEY=your-key-here")
			fmt.Fprintln(os.Stderr, "    export MINO_BASE_URL=https://api.openai.com/v1")
			fmt.Fprintln(os.Stderr, "    export MINO_MODEL=gpt-4.1-mini")
			fmt.Fprintln(os.Stderr, "    mino")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "  Option 2 — Create ~/.mino/providers.json (multi-provider):")
			fmt.Fprintln(os.Stderr, "    See github.com/H4fizWasabie/mino-agent#readme")
			os.Exit(1)
		}
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

	dashHost := os.Getenv("MINO_DASHBOARD_HOST")
	dashPort := envOr("MINO_DASHBOARD_PORT", "7777")
	if dashHost == "" {
		dashHost = "127.0.0.1"
	}
	redirectBase := os.Getenv("MINO_PUBLIC_URL")
	if redirectBase == "" {
		redirectBase = "http://" + dashHost + ":" + dashPort
	}

	w := &Core{
		Settings:  s,
		DB:        db,
		Client:    client,
		AuthStore: authStore,
		OAuth:     LoadOAuthEngine(s.Home, authStore, redirectBase),
		Memory:    mem,
		Tools:     tools,
		Sessions:  NewSessionManager(s, mem),
	}
	// MCP bridge: connect configured servers and register their tools
	mcpBridge := NewMCPBridge(s.Home, tools)
	mcpBridge.Start()
	tools.Register(MakeReloadPluginsTool(s.Home, tools, mcpBridge))

	// Tool filter: use embeddings to send only relevant tools per turn
	coreTools := []string{"recall", "save_note", "read_file", "bash", "request_approval", "resolve_approval"}
	toolFilter := NewToolFilter(coreTools, 8) // top 8 + 6 core = max 14 tools/turn
	if mem.embedder != nil {
		toolFilter.Index(tools.Schemas(), mem.embedder)
		slog.Info("tool filter indexed", "tools", len(tools.Schemas()))
	}
	tools.SetFilter(toolFilter)

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
		sendTelegramReply(bot, w.notifyChatID, result.Reply, result.ToolCalls)
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

	var es *EmbeddingStore
	if w.Memory != nil {
		es = w.Memory.embedder
	}
	result := RunLoop(
		w.Client, conversation.Session.sessionID, system, messages, w.Tools,
		w.Settings.MaxIter, w.Settings.MaxTokens, obs, stream,
		conversation.Checkpoint,
		w.Settings.Home,
		es,
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
