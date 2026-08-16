package main

// approval.go — RUN-006 (GitHub #220): the approval tier.
//
// Shape (decided on #220, 2026-08-16): a request_approval harness tool the
// model calls — NOT an auto-staged queue. docs/decisions.md's "no
// request_approval / resolve_approval protocol" governs playbooks
// (autonomous contracts); RUN-006 is the map's escalation for
// destructive/risky ops, and that constraint shapes it: no resolve tool, no
// persisted approval state machine. The approval is a conversational
// checkpoint — the tool stages the exact op and pages the owner through the
// existing outbox channel (telegram.go, the same path send_message uses;
// no second bot, no new UI); the owner replies "approve <id>" or "deny <id>"
// in any existing chat (Telegram, dashboard, CLI); the harness intercepts
// the reply BEFORE the loop and executes mechanically — the model can never
// approve its own op (the harness owns the gate).
//
// The whitelist IS the classifier (no LLM classifier): whitelisted ops run
// autonomously through the host tools; anything else is a candidate for
// this gate. Approval grants NO root privilege: the staged command runs as
// the Mino user via argv exec (no shell, no sudo) — the sudoers whitelist
// remains the only root transport and is never extended by approval (the
// fog "escalation when the whitelist is too narrow" stays out of scope;
// RUN-003's refusal message points the model at whitelist extension for
// privileged ops).
//
// Journal discipline (RUN-002): the stage is an op — approval.request
// (entity = the exact command, before = target snapshot, after = pending,
// status ok) is journaled BEFORE paging; approved execution is its own op
// approval.exec (before/after = target snapshots around the run, undo_of =
// the request, status ok/failed); denial and timeout mark the request
// rolled_back via SetStatus; boot reconciliation marks stale requests
// (ok, no exec child) rolled_back — the RUN-001/004/005 status seam.
//
// The pager is assumed fallible (map precondition): a page that fails to
// land is loud, and every unresolved request times out into a deny
// (MINO_APPROVAL_TIMEOUT_MINUTES, default 30) — the flow can never
// deadlock on an unreachable owner.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	approvalMaxCommand  = 4096            // staged command size cap (trust-boundary validation)
	approvalExecTimeout = 5 * time.Minute // approved ops run under the same cap as host ops
	approvalSnapshotCap = 16 << 20        // target hash reads capped — an arbitrary target must not hang the gate
)

// shellSyntaxRe — the staged command must be exact argv, not a shell line:
// the owner approves EXACTLY what runs. Shell operators, quotes, and
// backslashes would be silently mis-split by strings.Fields, so they are
// refused up front (same refusal family as the bash tool's sudo tripwire).
var shellSyntaxRe = regexp.MustCompile(`[&|;<>$()\x60'"\\]`)

// approvalReplyRe — the owner's conversational resolution: "approve 3" /
// "deny 3". Parsed at every owner-message entry point (telegram.go,
// dashboard.go, main.go CLI) before the message reaches the loop.
var approvalReplyRe = regexp.MustCompile(`(?i)^\s*(approve|deny)\s+(\d+)\s*$`)

func approvalTimeout() time.Duration {
	return time.Duration(envInt("MINO_APPROVAL_TIMEOUT_MINUTES", 30)) * time.Minute
}

// pendingApproval is one staged request awaiting the owner's decision.
type pendingApproval struct {
	id        int64
	command   string
	target    string
	reason    string
	sessionID string
	createdAt time.Time
}

// ApprovalGate is the approval tier: stage → page → resolve. The pending
// set is in-memory only — after a restart a request id is unresolvable, so
// a crash-orphaned request can never execute (boot reconciliation marks its
// journal entry rolled_back). The journal is the record; the map is the
// gate.
type ApprovalGate struct {
	mu      sync.Mutex
	home    string
	journal *OpJournal
	pending map[int64]*pendingApproval
	nextID  int64

	page func(msg string) error                                   // pages the owner (real: outbox draft)
	exec func(ctx context.Context, argv []string) (string, error) // executes an approved command (real: plain exec)
	now  func() time.Time
}

// NewApprovalGate wires the real seams. The pager writes through the
// existing outbox (queueOutbox) and verifies the draft landed; the executor
// is the same unprivileged runner the read-only probes use.
func NewApprovalGate(home string, j *OpJournal) *ApprovalGate {
	return &ApprovalGate{
		home:    home,
		journal: j,
		pending: map[int64]*pendingApproval{},
		page: func(msg string) error {
			queueOutbox(home, "owner", msg)
			if _, err := os.Stat(filepath.Join(home, "outbox", "msg_owner.txt")); err != nil {
				return fmt.Errorf("approval page not written to outbox: %v", err)
			}
			return nil
		},
		exec: runPlain,
		now:  time.Now,
	}
}

func (g *ApprovalGate) sessionID(ctx context.Context) string {
	if v := ctx.Value(sessionIDKey{}); v != nil {
		if sid, ok := v.(string); ok {
			return sid
		}
	}
	return ""
}

// Stage validates the proposed op, journals the intent (RUN-002 discipline:
// the stage is an op, recorded BEFORE the page goes out), registers it
// pending, and pages the owner. Returns the result string for the tool.
// A failed page does NOT fail the stage: the op is harmless while pending
// (nothing runs without approval), the model's own turn tells the owner the
// id, and the timeout denies the request if nobody answers — the flow never
// deadlocks on a broken pager.
func (g *ApprovalGate) Stage(ctx context.Context, command, target, reason string) string {
	if strings.TrimSpace(command) == "" {
		return "Error: command cannot be empty"
	}
	if len(command) > approvalMaxCommand {
		return fmt.Sprintf("Error: command too long (%d bytes, max %d)", len(command), approvalMaxCommand)
	}
	if strings.ContainsRune(command, '\x00') {
		return "Error: command must not contain NUL bytes"
	}
	if containsSudoInvocation(command) {
		return "Error: sudo is refused here — approval never grants root. Privileged operations stay whitelist-only (install_package, write_unit, restart_service); an op that missed the whitelist needs the owner to extend the whitelist, not approval."
	}
	if shellSyntaxRe.MatchString(command) {
		return "Error: shell operators, quotes, and backslashes are not allowed in an approval request — pass the exact argv (space-separated) so the owner approves exactly what runs"
	}
	if strings.TrimSpace(target) == "" {
		return "Error: target is required (what the command acts on — it is snapshotted before/after as journal evidence)"
	}
	if strings.TrimSpace(reason) == "" {
		return "Error: reason is required (shown to the owner)"
	}

	entry := &OpEntry{
		OpType:      "approval.request",
		Entity:      command,
		BeforeState: snapshotTarget(target),
		AfterState:  `{"state":"pending"}`,
		SessionID:   g.sessionID(ctx),
	}
	id, err := g.journal.Run(entry, nil)
	if err != nil {
		return fmt.Sprintf("Error: journal approval request: %v — nothing staged", err)
	}

	g.mu.Lock()
	g.pending[id] = &pendingApproval{id: id, command: command, target: target, reason: reason, sessionID: entry.SessionID, createdAt: g.now()}
	g.mu.Unlock()

	msg := fmt.Sprintf("[MINO APPROVAL] Request %d\nCommand: %s\nTarget: %s\nReason: %s\nReply: approve %d to execute, or deny %d to cancel. Unanswered requests expire and are denied after %d minutes.",
		id, command, target, reason, id, id, int(approvalTimeout().Minutes()))
	if err := g.page(msg); err != nil {
		// Fallible pager (map precondition): loud, never fatal — the timeout
		// below still denies the request, so no deadlock.
		slog.Error("approval page failed — request stays pending and will time out into a deny if unanswered", "request_id", id, "error", err)
	}
	slog.Info("approval request staged", "request_id", id, "command", command)
	return fmt.Sprintf("Approval request %d staged and paged to the owner. Nothing has been executed. The owner replies \"approve %d\" or \"deny %d\" in chat. Stop here and tell the owner the request is pending — do not run the operation yourself.", id, id, id)
}

// ResolveReply handles an owner message that resolves a pending request.
// Returns (reply, true) when the message was an approve/deny resolution
// (matched and consumed, even for unknown ids — the format is reserved),
// (\"\", false) when it was an ordinary message. Deny marks the request
// rolled_back; approve executes the staged command via the executor and
// journals the run as an approval.exec op (undo_of = the request).
func (g *ApprovalGate) ResolveReply(text string) (string, bool) {
	m := approvalReplyRe.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	var id int64
	fmt.Sscanf(m[2], "%d", &id)

	g.mu.Lock()
	p, ok := g.pending[id]
	if ok {
		delete(g.pending, id)
	}
	g.mu.Unlock()
	if !ok {
		return fmt.Sprintf("No pending approval request %s (it may already be resolved, or expired).", m[2]), true
	}

	if strings.EqualFold(m[1], "deny") {
		if err := g.journal.SetStatus(p.id, OpStatusRolledBack); err != nil {
			slog.Error("approval deny: marking request rolled_back failed", "request_id", p.id, "error", err)
		}
		slog.Warn("approval request denied", "request_id", p.id, "command", p.command)
		return fmt.Sprintf("Approval request %d denied — nothing was executed.", p.id), true
	}

	// approve: run the exact staged argv, then journal before/after.
	argv := strings.Fields(p.command)
	ctx, cancel := context.WithTimeout(context.Background(), approvalExecTimeout)
	defer cancel()
	before := snapshotTarget(p.target)
	out, err := g.exec(ctx, argv)
	after := snapshotTarget(p.target)
	entry := &OpEntry{
		OpType:      "approval.exec",
		Entity:      p.command,
		BeforeState: before,
		AfterState:  after,
		SessionID:   p.sessionID,
		UndoOf:      p.id,
	}
	if err != nil {
		entry.Status = OpStatusFailed
	}
	if _, jerr := g.journal.Run(entry, nil); jerr != nil {
		// The approved op already ran; a lost record must be loud, not silent.
		slog.Error("approval exec journal failed — approved op executed without a journal record", "request_id", p.id, "error", jerr)
	}
	if err != nil {
		slog.Error("approval request executed but failed", "request_id", p.id, "command", p.command, "error", err)
		return fmt.Sprintf("Approval request %d executed but failed: %v\n%s", p.id, err, out), true
	}
	slog.Info("approval request executed", "request_id", p.id, "command", p.command)
	return fmt.Sprintf("Approval request %d executed. Output:\n%s", p.id, out), true
}

// sweep denies every request older than the timeout — the safe default when
// the owner cannot be reached. Journal + loud log, exactly the map's
// precondition: deny, never hang.
func (g *ApprovalGate) sweep(now time.Time) {
	timeout := approvalTimeout()
	g.mu.Lock()
	var expired []*pendingApproval
	for id, p := range g.pending {
		if now.Sub(p.createdAt) >= timeout {
			expired = append(expired, p)
			delete(g.pending, id)
		}
	}
	g.mu.Unlock()
	for _, p := range expired {
		if err := g.journal.SetStatus(p.id, OpStatusRolledBack); err != nil {
			slog.Error("approval timeout: marking request rolled_back failed", "request_id", p.id, "error", err)
		}
		slog.Error("approval request timed out and was denied — the owner never answered the page; nothing was executed",
			"request_id", p.id, "command", p.command, "timeout_minutes", int(timeout.Minutes()))
	}
}

// sweepLoop is the background denier, started at boot (safeGo).
func (g *ApprovalGate) sweepLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		g.sweep(time.Now())
	}
}

// ReconcileStaleApprovals marks approval.request entries that never got a
// decision (ok, no approval.exec child) rolled_back at boot — the pending
// map is gone after a restart, so those requests are unresolvable; a staged
// op that can never run must not read as live in the journal. Returns the
// count reconciled. Mirrors ReconcileInterruptedRuns.
func ReconcileStaleApprovals(j *OpJournal) int {
	rows, err := j.db.Query(`SELECT r.id FROM ops_journal r
		WHERE r.op_type = 'approval.request' AND r.status = 'ok'
		AND NOT EXISTS (SELECT 1 FROM ops_journal e WHERE e.op_type = 'approval.exec' AND e.undo_of = r.id)`)
	if err != nil {
		slog.Error("stale approval reconciliation query failed", "error", err)
		return 0
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	// Update only after the read rows are closed — SQLite is single-writer;
	// Exec while a query holds the read lock deadlocks.
	n := 0
	for _, id := range ids {
		if err := j.SetStatus(id, OpStatusRolledBack); err != nil {
			slog.Error("stale approval reconciliation failed", "request_id", id, "error", err)
			continue
		}
		n++
	}
	return n
}

// snapshotTarget captures before/after evidence for the staged op: the
// target path's existence, size, mtime, and a content hash (capped — an
// arbitrary target must not hang the gate). Mirrors the host tools'
// before/after state JSON.
func snapshotTarget(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		b, _ := json.Marshal(map[string]any{"path": path, "exists": false})
		return string(b)
	}
	sha := ""
	truncated := false
	if st.Mode().IsRegular() {
		if f, err := os.Open(path); err == nil {
			h := sha256.New()
			if n, cerr := io.Copy(h, io.LimitReader(f, approvalSnapshotCap)); cerr == nil && n >= approvalSnapshotCap {
				truncated = true
			}
			f.Close()
			sha = fmt.Sprintf("%x", h.Sum(nil))
		}
	}
	b, _ := json.Marshal(map[string]any{
		"path": path, "exists": true, "size": st.Size(),
		"mtime":  st.ModTime().UTC().Format(time.RFC3339),
		"sha256": sha, "hash_truncated": truncated,
	})
	return string(b)
}

// --- request_approval tool ---

func makeRequestApprovalTool(g *ApprovalGate) *Tool {
	return &Tool{
		Name:        "request_approval",
		Description: "Stage an operation for the owner's explicit approval. The exact command is journaled, the owner is paged through Telegram, and the command executes ONLY after the owner replies \"approve <id>\" (\"deny <id>\" cancels). Use this for destructive or risky operations — anything that deletes, overwrites, or irreversibly changes state — and for operations the host tools (install_package, write_unit, restart_service) refuse as outside the privilege whitelist: the whitelist IS the autonomous/approval boundary, whitelisted ops run autonomously, everything else goes through this gate. The command runs as the Mino user with NO root privilege — approval never grants root (the sudoers whitelist is the only root transport and is not extended by approval; a privileged op that missed the whitelist needs the owner to extend the whitelist, not approval). Pass the command as exact argv: space-separated, no shell operators (| > && ; $ quotes), no sudo. While a request is pending, stop and report to the owner — do not run the operation yourself.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "The exact command to run, argv form. Example: \"rm -rf /home/mino/.mino/results/stale-dir\"."},
				"target":  map[string]any{"type": "string", "description": "What the command acts on (path or resource) — shown to the owner and snapshotted before/after as journal evidence."},
				"reason":  map[string]any{"type": "string", "description": "Why this operation needs approval — shown to the owner."},
			},
			"required": []string{"command", "target", "reason"},
		},
		ContextFn: func(ctx context.Context, args map[string]any) string {
			command, _ := args["command"].(string)
			target, _ := args["target"].(string)
			reason, _ := args["reason"].(string)
			return g.Stage(ctx, command, target, reason)
		},
	}
}
