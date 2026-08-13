package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"unicode"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// Mino — app.py — wires everything together.
// This is the assembly diagram in code.

// safeGo runs a background loop with a panic guard: one panic in any of the
// dispatcher/consolidation goroutines must not kill the whole agent (systemd
// restarts it, but in-flight sessions and today's schedule die with it).
func safeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in background loop", "loop", name, "panic", r)
			}
		}()
		fn()
	}()
}

type Core struct {
	notifyMu         sync.RWMutex
	notifyTelegram   func(result *LoopResult)
	notifyChatID     int64
	Settings         *Settings
	DB               *sql.DB
	Client           *ProviderManager
	AuthStore        *AuthStore
	OAuth            *OAuthEngine
	Memory           *Memory
	Responsibilities *ResponsibilityStore
	Tools            *Registry
	Sessions         *SessionManager
	mcp              *MCPBridge
	snapshots        sync.Map // sessionID → *LoopSnapshot (ephemeral, per-loop state)
}

func NewCore() *Core {
	s := LoadSettings()
	s.EnsureHome()
	seedBuiltinSkills(s.Home)

	db := Connect(s.Home)
	responsibilities := NewResponsibilityStore(db)
	if _, err := responsibilities.Bootstrap(s.Home, s.Location(), time.Now()); err != nil {
		panic(fmt.Sprintf("initialize responsibilities: %v", err))
	}
	authStore := LoadAuthStore(s.Home)
	client, err := NewProviderManager(s.Home, s, authStore)
	if err != nil {
		if !dashboardRequested() || !needsOnboarding(s.Home) {
			panic(err)
		}
		slog.Info("dashboard awaiting provider setup")
	}

	mem := NewMemory(db, client, s)
	mem.graph.StartReconciler(5 * time.Second)
	mem.CleanupArtifacts()

	// Working-memory pruning is the only embedding-store leftover; it now only
	// trims the file (issue #179 removed the store itself).
	PruneRecentFixes(s.Home, 7*24*time.Hour)
	mem.skills = NewSkillLoader(s.Home)
	tools := BuildRegistry(db, s.Home, s.Workspace, mem, s.Location())
	tools.SetMaxToolDescChars(s.MaxToolDescChars)           // schema payload description cap
	tools.SetLogDB(db)                                      // enable tool_calls table logging
	tools.SetAuditLog(filepath.Join(s.Home, "audit.jsonl")) // §8.4: immutable audit log
	LoadExtensions(s.Home, tools)                           // discover + register extension tools

	if s.ConsolidateEvery > 0 {
		safeGo("consolidation", func() { // 6-hour full consolidation pass
			for {
				time.Sleep(6 * time.Hour)
				if n := mem.ConsolidateDue(); n > 0 {
					slog.Info("consolidation", "new_facts", n)
				}
			}
		})
		safeGo("graph-maintenance", func() { // graph maintenance — 6-hour, offset +45min from consolidation
			time.Sleep(45 * time.Minute)
			for {
				time.Sleep(6 * time.Hour)
				edges, comms, err := mem.MaintainGraph()
				if err != nil {
					slog.Warn("graph maintenance incomplete", "error", err)
				} else {
					slog.Info("graph maintenance", "edges", edges, "communities", comms)
				}
			}
		})
		safeGo("context-threshold", func() { // 5-minute threshold check — triggers when context nears 80% full
			for {
				time.Sleep(5 * time.Minute)
				if n := mem.ConsolidateIfFull(s.ContextChars); n > 0 {
					slog.Info("consolidation (threshold)", "new_facts", n)
				}
				if n := mem.JudgeChangedFacts(); n > 0 {
					slog.Info("graph edge judgment", "edges", n)
				}
				if n := mem.DistillOutputsDue(); n > 0 {
					slog.Info("playbook output distillation", "written", n)
				}
			}
		})
	}

	dashHost := os.Getenv("MINO_DASHBOARD_HOST")
	dashPort := s.DashboardPort()
	if dashHost == "" {
		dashHost = "127.0.0.1"
	}
	redirectBase := os.Getenv("MINO_PUBLIC_URL")
	if redirectBase == "" {
		redirectBase = "http://" + dashHost + ":" + dashPort
	}
	oauthEngine := LoadOAuthEngine(s.Home, authStore, redirectBase)

	w := &Core{
		Settings:         s,
		DB:               db,
		Client:           client,
		AuthStore:        authStore,
		OAuth:            oauthEngine,
		Memory:           mem,
		Responsibilities: responsibilities,
		Tools:            tools,
		Sessions:         NewSessionManager(s, mem),
	}
	// MCP bridge: connect configured servers and register their tools
	mcpBridge := NewMCPBridge(s.Home, tools)
	mcpBridge.Start()
	tools.Register(MakeReloadPluginsTool(s.Home, tools, mcpBridge))
	w.mcp = mcpBridge

	// Playbook tools — LLM can discover and run playbooks
	tools.Register(behaves(makeQueryAuditTool(db), BehaviorObserve))
	tools.Register(behaves(makeSystemCheckTool(db, s.Home), BehaviorObserve))
	tools.Register(behaves(makeListPlaybooksTool(s.Home), BehaviorObserve))
	tools.Register(behaves(makeManagePlaybookTool(w), BehaviorMutate))
	tools.Register(behaves(makeRunPlaybookTool(w), BehaviorMutate))
	tools.Register(behaves(makeCapturePlaybookTool(w), BehaviorMutate))
	tools.Register(behaves(makeSchedulePlaybookTool(s.Home, s.Timezone), BehaviorMutate))
	tools.Register(behaves(makeListSchedulesTool(s.Home), BehaviorObserve))
	tools.Register(behaves(makeCancelScheduleTool(s.Home), BehaviorMutate))
	// seed example playbook if none exist
	if len(ListPlaybooks(s.Home)) == 0 {
		CreateExamplePlaybook(s.Home)
	}

	// In-process playbook scheduler — checks schedules.json every minute
	safeGo("schedule-dispatcher", func() { runScheduleDispatcher(w) })

	// Outbox delivery — send_message drafts to the outbox; this drains it
	// to Telegram so scheduled reports actually arrive.
	safeGo("outbox-dispatcher", func() { runOutboxDispatcher(w) })

	// Reminder delivery — runs in every gateway mode; no-ops without Telegram config.
	safeGo("reminder-dispatcher", func() { runReminderDispatcher(w) })

	// Archive digest — daily Telegram note of archived facts (MEM-08)
	safeGo("archive-digest", func() { runArchiveDigest(w) })

	// Alert checker (§18.1): error rate + dead man's switch, every 5 minutes
	safeGo("alert-checker", func() { checkAlerts(db, w.sendAlertMessage, 5*time.Minute, w.Settings.Location()) })

	// Audit pruning: remove events older than 30 days, runs daily
	safeGo("audit-prune", func() {
		for {
			time.Sleep(24 * time.Hour)
			w.pruneOldAuditEvents()
		}
	})

	return w
}

func dashboardRequested() bool {
	return os.Getenv("MINO_DASHBOARD_PORT") != "" || len(os.Args) > 1 && os.Args[1] == "dashboard"
}

func telegramDashboardEnabled() bool {
	return dashboardRequested()
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
		w.recordTelegramNotification(chatID, result.Reply)
		sendTelegramReply(bot, chatID, result.Reply, nil, 0)
	}
}

func (w *Core) recordTelegramNotification(chatID int64, reply string) {
	conversation := w.Sessions.Get(fmt.Sprintf("tg:%d", chatID))
	conversation.mu.Lock()
	defer conversation.mu.Unlock()
	conversation.Session.AddNotification(reply)
}

func (w *Core) sendNotification(result *LoopResult) {
	w.notifyMu.RLock()
	notify := w.notifyTelegram
	w.notifyMu.RUnlock()
	if notify != nil {
		notify(result)
	}
}

// sendAlertMessage sends a plain text alert to Telegram if configured.
func (w *Core) sendAlertMessage(msg string) {
	w.notifyMu.RLock()
	notify := w.notifyTelegram
	w.notifyMu.RUnlock()
	if notify != nil {
		notify(&LoopResult{Reply: msg})
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

	w.startLoop(sessionID)
	defer w.endLoop(sessionID)

	ctx, finish := conversation.beginTurn(parent)
	defer finish()
	// Wire snapshot updates through context
	ctx = context.WithValue(ctx, snapshotKey{}, w.snapshotUpdater(sessionID))
	// Wire audit logging through context
	ctx = context.WithValue(ctx, auditKey{}, func(eventType, message string, iteration int) {
		w.auditLog(sessionID, eventType, message, iteration)
	})
	system, routing := conversation.Session.BuildContext(userMessage, source)
	// Cache stability: keep the system prompt byte-stable across calls so the
	// provider prefix cache stays warm. The clock AND the per-turn routing
	// block (matched skills + playbook routing) are appended to the fresh user
	// turn only — they change per message, so they must live at the tail, not
	// in the system prompt, or every turn would invalidate the cached prefix.
	clock := authoritativeClock(time.Now(), w.Settings.Location())
	messages, userContext := conversation.Session.ContextFor(system, userMessage)
	if len(messages) > 0 {
		tail := clock + "\nUse this clock; do not infer the current time from conversation history."
		if routing != "" {
			tail = routing + "\n\n" + tail
		}
		messages[len(messages)-1].Content += "\n\n" + tail
	}
	msgLen := 0
	for _, m := range messages {
		msgLen += len(m.Content)
	}
	logTrace(w.Settings.Home, "turn_start", map[string]any{"user_message": userMessage, "system_chars": len(system), "msg_count": len(messages), "msg_chars": msgLen})
	if len(images) > 0 {
		messages[len(messages)-1].Images = images
	}

	result := RunLoopContext(
		ctx,
		w.Client, conversation.Session.sessionID, system, messages, w.Tools,
		w.Settings.MaxIter, w.Settings.MaxTokens, obs, stream,
		w.Settings.Home,
	)

	conversation.Session.AddExchange(userMessage, userContext, result.Reply, result.ToolCalls, source)
	return result
}

func authoritativeClock(now time.Time, loc *time.Location) string {
	local := now.In(loc)
	zone, offset := local.Zone()
	return fmt.Sprintf("[AUTHORITATIVE LOCAL CLOCK: %s %s (UTC%+03d:%02d). Today is %s.]",
		local.Format("Monday, 2006-01-02 15:04:05"), zone, offset/3600, (abs(offset)%3600)/60,
		local.Format("2006-01-02"))
}

func (w *Core) CancelTurn(sessionID string) bool {
	conversation := w.Sessions.Get(sessionID)
	active := conversation.cancelTurn()
	// Even when no loop is running (task already ended — e.g. iteration_limit),
	// record the stop so the next turn doesn't resume the cancelled task.
	conversation.mu.Lock()
	conversation.Session.MarkStopped()
	conversation.mu.Unlock()
	return active
}

// cancelPhrases (CTX-005): natural cancel phrasings anywhere in the message.
// "Its fine then, ill get this data myself" must stop a task, not spawn a
// 30-iteration turn. The phrase list is deliberately short and concrete —
// this is a harness stop-word gate, not an intent classifier.
var cancelPhrases = []string{
	"its fine", "it's fine", "never mind", "nevermind",
	"ill do it myself", "i'll do it myself",
	"ill get this data", "i'll get this data",
	"ill fetch this", "i'll fetch this",
	"forget it", "dont bother", "don't bother",
	"lets drop it", "let's drop it",
}

// cancelGlue: connective words that can remain after a cancel phrase is
// stripped ("its fine THEN, i'll get this data MYSELF") — not substance.
var cancelGlue = []string{"then", "myself", "so", "now", "just", "already", "please", "ok", "okay", "first", "again"}

func isStopMessage(message string) bool {
	clean := strings.NewReplacer(".", " ", ",", " ", "!", " ", "?", " ", ":", " ").Replace(strings.ToLower(message))
	words := strings.Fields(clean)
	for len(words) > 0 && (words[0] == "ok" || words[0] == "okay" || words[0] == "mino") {
		words = words[1:]
	}
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "stop", "cancel", "halt":
		return true
	}
	// CTX-011: a stop-word anywhere is decisive, even non-leading ("its fine,
	// stop"). The 2026-08-11 suite showed the substantive-remainder guard
	// treated the trailing "stop" as substance and queued the message as a
	// normal turn behind the running one. Leading behavior is unchanged.
	// Questions about stopping are not stops.
	questionWords := map[string]bool{"why": true, "what": true, "when": true, "where": true, "how": true, "is": true, "are": true, "did": true, "does": true, "do": true, "can": true, "could": true, "would": true, "should": true}
	isQuestion := strings.Contains(message, "?") || (len(words) > 0 && questionWords[words[0]])
	if !isQuestion {
		for _, w := range words {
			if w == "stop" || w == "halt" {
				return true
			}
		}
	}
	// Natural cancel phrasings (CTX-005). A message whose remainder still
	// contains substantive text after the phrases are stripped is a question
	// or a new instruction, not a stop — e.g. "i think chem 15 is not
	// supposed to be in it. its fine, ill get this data myself" keeps the
	// doubt alive, so the turn proceeds (cheaply, now that CTX-002/004 hold).
	// A rhetorical trailing "?" on a bare cancel ("never mind?") still stops.
	lower := strings.ToLower(message)
	found := false
	for _, p := range cancelPhrases {
		if strings.Contains(lower, p) {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	rest := lower
	for _, p := range cancelPhrases {
		rest = strings.ReplaceAll(rest, p, " ")
	}
	for _, g := range cancelGlue {
		rest = strings.ReplaceAll(rest, g, " ")
	}
	for _, r := range rest {
		if unicode.IsLetter(r) {
			return false // substantive remainder — not a pure cancel
		}
	}
	return true
}

func (w *Core) Close() {
	stopAlerts()
	closeTrace(w.Settings.Home)
	w.Tools.CloseAuditLog()
	if w.mcp != nil {
		w.mcp.Close()
	}
	w.DB.Close()
}
