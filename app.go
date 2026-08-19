package main

import (
	"context"
	"database/sql"
	"encoding/json"
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
	ext              *ExtensionSupervisor
	mcp              *MCPBridge
	approvals        *ApprovalGate // RUN-006: stage/page/approve gate for risky ops
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
	pruneSpillsIfDue(s.Home) // RUN-007: durable spill store, bounded by max-age at boot
	mem.skills = NewSkillLoader(s.Home)
	tools := BuildRegistry(db, s.Home, s.Workspace, mem, s.Location())
	tools.SetMaxToolDescChars(s.MaxToolDescChars)           // schema payload description cap
	tools.SetLogDB(db)                                      // enable tool_calls table logging
	tools.SetAuditLog(filepath.Join(s.Home, "audit.jsonl")) // §8.4: immutable audit log
	// RUN-006: the approval tier — same journal as the other consumers.
	journal := NewOpJournal(db)
	// RUN-001: in-process extension supervision — spawns, health-checks,
	// restarts on crash, kills on shutdown; boot reconciliation re-spawns
	// every supervised entry. Runs BEFORE LoadExtensions so supervised
	// children are up when their tools get discovered.
	extSup := NewExtensionSupervisor(s.Home, tools, journal)
	extSup.Start()
	LoadExtensions(s.Home, tools) // discover + register extension tools
	// RUN-003: the privilege bridge — harness-native host tools. The
	// sudoers command whitelist is the boundary; these are the only way
	// Mino touches host state (the bash tool refuses sudo outright).
	host := NewHostTools(s.Home, NewOpJournal(db))

	if s.ConsolidateEvery > 0 {
		safeGo("consolidation", func() { // 6-hour full consolidation pass
			// First fire shortly after boot, then every 6h (#211): the old
			// sleep-first loop deferred the first pass 6h past boot, so any
			// restart (deploys, crashes) starved consolidation indefinitely —
			// 2026-08-15: three boots, zero passes, 76 rows pending.
			time.Sleep(5 * time.Minute)
			for {
				if n := mem.ConsolidateDue(); n >= 0 {
					slog.Info("consolidation", "new_facts", n)
				}
				time.Sleep(6 * time.Hour)
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
		ext:              extSup,
		Sessions:         NewSessionManager(s, mem),
		approvals:        NewApprovalGate(s.Home, journal),
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
	tools.Register(behaves(makeManageExtensionTool(extSup), BehaviorMutate))
	tools.Register(behaves(makeInstallPackageTool(host), BehaviorMutate))
	tools.Register(behaves(makeWriteUnitTool(host), BehaviorMutate))
	tools.Register(behaves(makeRestartServiceTool(host), BehaviorMutate))
	tools.Register(behaves(makeRequestApprovalTool(w.approvals), BehaviorMutate))
	tools.Register(behaves(makeRunPlaybookTool(w), BehaviorMutate))
	tools.Register(behaves(makeTaskifyTool(w), BehaviorMutate))
	tools.Register(behaves(makeSplitStageTool(w), BehaviorMutate))
	tools.Register(behaves(makeCapturePlaybookTool(w), BehaviorMutate))
	tools.Register(behaves(makeSchedulePlaybookTool(w), BehaviorMutate))
	tools.Register(behaves(makeComposeMessageTool(w.Client), BehaviorObserve))
	tools.Register(behaves(makeListSchedulesTool(s.Home), BehaviorObserve))
	tools.Register(behaves(makeCancelScheduleTool(s.Home), BehaviorMutate))
	// seed example playbook if none exist
	if len(ListPlaybooks(s.Home)) == 0 {
		CreateExamplePlaybook(s.Home)
	}
	// task-232: seed the generic default playbooks (idempotent — absent only)
	if err := SeedDefaultPlaybooks(s.Home); err != nil {
		slog.Error("seeding default playbooks", "error", err)
	}
	// PSN-001: seed the persona roster so seeded playbooks' agent: bindings resolve
	if err := SeedDefaultAgents(s.Home); err != nil {
		slog.Error("seeding default agents", "error", err)
	}

	// In-process playbook scheduler — checks schedules.json every minute
	safeGo("schedule-dispatcher", func() { runScheduleDispatcher(w) })

	// Outbox delivery — send_message drafts to the outbox; this drains it
	// to Telegram so scheduled reports actually arrive.
	safeGo("outbox-dispatcher", func() { runOutboxDispatcher(w) })

	// Approval timeout — unanswered approval requests deny themselves (RUN-006).
	safeGo("approval-sweeper", func() { w.approvals.sweepLoop() })

	// Reminder delivery — runs in every gateway mode; no-ops without Telegram config.
	safeGo("reminder-dispatcher", func() { runReminderDispatcher(w) })

	// Archive digest — daily Telegram note of archived facts (MEM-08)
	safeGo("archive-digest", func() { runArchiveDigest(w) })

	// Alert checker (§18.1): error rate + dead man's switch, every 5 minutes
	safeGo("alert-checker", func() { checkAlerts(db, w.sendAlertMessage, 5*time.Minute, w.Settings.Location()) })

	// OBS-001 boot reconciliation: runs stuck in "running" across a crash get
	// marked "interrupted" with evidence — no manual quarantine.
	if n := ReconcileInterruptedRuns(w.Settings.Home); n > 0 {
		slog.Info("startup reconciliation", "interrupted_runs", n)
	}

	// RUN-006 boot reconciliation: approval requests that never got a
	// decision are unresolvable after a restart (the pending map is gone) —
	// mark them rolled_back so the journal never reads a staged op as live.
	if n := ReconcileStaleApprovals(w.approvals.journal); n > 0 {
		slog.Info("startup reconciliation", "stale_approval_requests", n)
	}

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
	// #237: the owner's approval of a paused task gate is a harness decision
	// (the model can never approve its own run — approval.go's RUN-006
	// discipline). Detect the approval BEFORE the loop, mark the run approved
	// in its state.json, and route the turn to resume it via run_playbook.
	gateRouting := approvePendingTaskGate(w.Settings.Home, sessionID, userMessage)
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
		if gateRouting != "" {
			tail = gateRouting + "\n\n" + tail
		}
		// #237 task-intent detection: the offer is a DISCUSSION OPENER — no
		// scaffold, no work, until the owner approves (owner lock 2026-08-16).
		// Suppressed on the turn that just approved the gate: the approval is
		// the start signal, not another discussion.
		if gateRouting == "" {
			if offer := taskIntentOffer(userMessage); offer != "" {
				tail = offer + "\n\n" + tail
			}
		}
		messages[len(messages)-1].Content += "\n\n" + tail
	}
	msgLen := 0
	for _, m := range messages {
		msgLen += len(m.Content)
	}
	// #240 — budget awareness: tell the model its own context ceiling each
	// turn (chars used / ceiling / headroom), computed from the messages the
	// harness already built. Same per-turn-tail placement as the clock, so the
	// byte-stable system prompt and its prefix cache stay warm. INFORMATIONAL
	// ONLY — CTX-003 (state both numbers), verify-then-claim, and
	// action-grounding (CTX-016) are absolute rules with no context-budget
	// escape hatch; this block never waives them and never offers skipping
	// verification or rushing.
	if len(messages) > 0 {
		if block := contextBudgetBlock(len(system)+msgLen, w.Settings.ContextChars); block != "" {
			messages[len(messages)-1].Content += "\n\n" + block
			msgLen += len(block) + 2
		}
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

	// CTX-022 C (round 3): post-reply verification — generation is
	// probabilistic, so the harness checks the draft reply against any
	// owner-established facts it carried before delivery, and signs the
	// contradiction instead of letting it ship silently. One small call per
	// turn that carried owner facts; verification failure fails open (never
	// blocks the reply on a check error).
	if strings.Contains(routing, ownerEstablishedMarker) && w.Memory != nil && w.Memory.graph != nil {
		if facts := ownerEstablishedFacts(userMessage, w.Memory.graph.Remember(userMessage, "")); facts != "" {
			if correction := verifyReplyAgainstOwnerFacts(w.Client, facts, result.Reply, userMessage); correction != "" {
				result.Reply += "\n\n" + correction
			}
		}
	}

	conversation.Session.AddExchange(userMessage, userContext, result.Reply, result.ToolCalls, source)
	return result
}

// contextBudgetWarnPct — the warning fires on every turn at or above 70% of
// the ceiling; the 90% level from #240 is the same locked template with a
// higher N, so one gate covers both thresholds.
const contextBudgetWarnPct = 70

// contextBudgetBlock (issue #240) renders the per-turn context-budget block:
// chars used, the max ceiling, remaining headroom, and — at or above
// contextBudgetWarnPct — the threshold warning. The warning template is
// LOCKED (owner decision): exactly two safe options — compact/consolidate,
// or wrap up with a status report. It never offers skipping verification or
// rushing: verification discipline is budget-independent. Guarded by
// TestContextBudgetBlockGuardrail.
func contextBudgetBlock(used, ceiling int) string {
	if ceiling <= 0 || used < 0 {
		return ""
	}
	if used > ceiling {
		used = ceiling
	}
	pct := used * 100 / ceiling
	block := fmt.Sprintf("context budget: %d chars used of %d ceiling (%d%%), %d headroom", used, ceiling, pct, ceiling-used)
	if pct >= contextBudgetWarnPct {
		block += fmt.Sprintf("\nWARNING: context at %d%% of the ceiling — compact or consolidate (manage_memory/consolidate), or wrap up with a status report of what's done and what remains.", pct)
	}
	return block
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
	if w.ext != nil {
		w.ext.Shutdown() // RUN-001: kill supervised extension children
	}
	closeTrace(w.Settings.Home)
	w.Tools.CloseAuditLog()
	if w.mcp != nil {
		w.mcp.Close()
	}
	w.DB.Close()
}

// verifyReplyAgainstOwnerFacts (CTX-022 C, round 3) asks the small model
// whether the draft reply contradicts or ignores any owner-established fact;
// returns a harness-signed correction when it does. Fail-open: any error
// returns "" — a verification outage never blocks the reply.
func verifyReplyAgainstOwnerFacts(client *ProviderManager, facts, reply, question string) string {
	if client == nil || reply == "" {
		return ""
	}
	prompt := fmt.Sprintf("Owner-established facts:\n%s\n\nQuestion: %s\n\nDraft reply:\n%s\n\nDoes the draft reply contradict or ignore any owner-established fact? Reply with JSON only: {\"ok\": true} or {\"ok\": false, \"reason\": \"<one sentence>\"}.", facts, question, reply)
	resp, err := client.CreateJSON("reply-verify", SmallModel, []Message{{Role: "user", Content: prompt}}, 300, "")
	if err != nil {
		slog.Warn("reply verification failed, failing open", "error", err)
		return ""
	}
	ok, reason := parseVerifyResponse(resp.FinalText)
	if ok {
		return ""
	}
	if reason == "" {
		reason = "the draft contradicts the owner's established record"
	}
	return fmt.Sprintf("[Memory verification — the owner's record stands: %s]", reason)
}

// parseVerifyResponse extracts the ok/reason verdict from the small model's
// JSON reply, tolerating surrounding prose.
func parseVerifyResponse(text string) (bool, string) {
	start := strings.Index(text, "{")
	if start < 0 {
		return true, "" // no JSON verdict — assume ok
	}
	for end := start + 1; end <= len(text); end++ {
		if text[end-1] != '}' || !json.Valid([]byte(text[start:end])) {
			continue
		}
		var v struct {
			OK     bool   `json:"ok"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(text[start:end]), &v); err == nil {
			return v.OK, v.Reason
		}
	}
	return true, ""
}
