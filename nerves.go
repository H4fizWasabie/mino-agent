package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// --- Snapshot ---

// LoopSnapshot is the ephemeral window into a running loop.
// Updated each iteration by the main loop; read by the interrupt goroutine.
type LoopSnapshot struct {
	Iteration   int
	Status      string   // "thinking", "running_tool", "done"
	CurrentTool string   // tool name + truncated args
	LastOutput  string   // last tool result (truncated to 300 chars)
	ToolHistory []string // last 10 calls: "read_file(notes.md) -> ok"
	StartedAt   time.Time
}

// --- Interrupt routing ---

var interruptPrefixes = []string{
	"btw", "by the way", "status", "what are you doing",
	"what's happening", "whats happening", "report", "check in",
	"how's it going", "hows it going", "progress", "update",
}

// isInterrupt checks if a message should interrupt a running loop.
// Returns the query (stripped of prefix) and whether it matches.
func isInterrupt(message string) (query string, ok bool) {
	lower := strings.ToLower(strings.TrimSpace(message))
	for _, p := range interruptPrefixes {
		if strings.HasPrefix(lower, p) {
			query := strings.TrimSpace(message[len(p):])
			if query == "" {
				query = "What are you currently doing? Give a brief status update."
			}
			return query, true
		}
		// Also match standalone status queries
		if lower == p {
			return "What are you currently doing? Give a brief status update.", true
		}
	}
	return "", false
}

// --- Snapshot store (on Core via sync.Map) ---

func (w *Core) startLoop(sessionID string) {
	w.snapshots.Store(sessionID, &LoopSnapshot{
		StartedAt: time.Now(),
		Status:    "thinking",
	})
}

func (w *Core) endLoop(sessionID string) {
	w.snapshots.Delete(sessionID)
}

func (w *Core) snapshot(sessionID string) *LoopSnapshot {
	v, ok := w.snapshots.Load(sessionID)
	if !ok {
		return nil
	}
	return v.(*LoopSnapshot)
}

// snapshotUpdate returns a func for RunLoopContext to call each iteration.
func (w *Core) snapshotUpdater(sessionID string) func(LoopSnapshot) {
	return func(snap LoopSnapshot) {
		// keep StartedAt from the original
		if existing := w.snapshot(sessionID); existing != nil {
			snap.StartedAt = existing.StartedAt
		}
		// cap tool history
		if len(snap.ToolHistory) > 10 {
			snap.ToolHistory = snap.ToolHistory[len(snap.ToolHistory)-10:]
		}
		if len(snap.LastOutput) > 300 {
			snap.LastOutput = snap.LastOutput[:300] + "..."
		}
		w.snapshots.Store(sessionID, &snap)
	}
}

// --- Interrupt handler ---

// InterruptRequest carries a mid-loop query and how to reply.
type InterruptRequest struct {
	Query     string
	ReplyFunc func(string)
}

// handleInterrupt runs a short, read-only LLM call to answer a mid-loop query.
// Synchronous — callers wrap in a goroutine if async is needed.
func (w *Core) handleInterrupt(sessionID, query string, replyFunc func(string)) {
	snap := w.snapshot(sessionID)
	if snap == nil {
		replyFunc("(no active task)")
		return
	}

	conversation := w.Sessions.Get(sessionID)
	system := w.buildInterruptSystem(snap, query)

	if w.Client == nil {
		replyFunc("(provider not configured)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	messages := []Message{{Role: "user", Content: query}}
	// CTX-012: no tool schemas — the state snapshot is sufficient, and a
	// tool-call answer gets dropped by extractText (observed 2026-08-11:
	// "btw status" returned "(no response)" while the model had answered
	// with a tool call).
	resp, err := w.Client.CreateContext(ctx, sessionID+"_intr", MainModel, messages, 1024, system, nil)
	if err != nil {
		replyFunc(fmt.Sprintf("(interrupt error: %v)", err))
		return
	}

	reply := extractText(resp.Content)
	if reply == "" {
		// Last resort: a tool-call-only response still gets a useful status
		// line from the snapshot instead of a dead "(no response)".
		reply = fmt.Sprintf("(status: iteration %d, %s", snap.Iteration, snap.Status)
		if snap.CurrentTool != "" {
			reply += ", on " + snap.CurrentTool
		}
		reply += ")"
	}
	replyFunc(reply)

	// Push to dashboard event stream
	pushDashEvent(map[string]any{
		"type": "interrupt", "session_id": sessionID,
		"query": query, "reply": truncate(reply, 200),
		"iteration": snap.Iteration,
	})

	// Write to persistent audit log
	w.auditLog(sessionID, "interrupt", fmt.Sprintf("query=%q reply=%q", query, truncate(reply, 80)), snap.Iteration)

	// log to trace (existing)
	logTrace(w.Settings.Home, "interrupt", map[string]any{
		"session":   sessionID,
		"query":     query,
		"reply":     reply,
		"iteration": snap.Iteration,
		"tool":      snap.CurrentTool,
	})

	conversation.Session.AddNotification(reply)
}

func (w *Core) buildInterruptSystem(snap *LoopSnapshot, query string) string {
	var b strings.Builder
	b.WriteString(`You are Mino's self-awareness system. Answer the user's mid-task query concisely and factually.
Base your answer on the CURRENT STATE below. Reply in plain text only — do NOT call any tools; the state below is sufficient.
Be direct — the user is checking in on a running task.

CURRENT STATE:
`)
	fmt.Fprintf(&b, "Iteration: %d\n", snap.Iteration)
	fmt.Fprintf(&b, "Status: %s\n", snap.Status)
	if snap.CurrentTool != "" {
		fmt.Fprintf(&b, "Current tool: %s\n", snap.CurrentTool)
	}
	if snap.LastOutput != "" {
		fmt.Fprintf(&b, "Last output: %s\n", snap.LastOutput)
	}
	if len(snap.ToolHistory) > 0 {
		b.WriteString("\nRecent tool history:\n")
		for _, h := range snap.ToolHistory {
			fmt.Fprintf(&b, "  %s\n", h)
		}
	}
	fmt.Fprintf(&b, "\nTask running for: %s\n", time.Since(snap.StartedAt).Round(time.Second))

	return b.String()
}

// --- Loop detection (ticket 003 — placeholder, wired to snapshot) ---

var loopDetectionThreshold = 3 // exact repeats

// loopNameThreshold: consecutive same-tool calls regardless of args. Higher
// than the exact threshold so legit batch reads (read_file ×5) stay quiet;
// a 6th same-name call is almost always a stuck model (composio discovery
// loops, repeated run_playbook) and worth a nudge.
var loopNameThreshold = 6

// detectLoop checks the recent tool history for two loop signals:
// exact repeats (identical name+args) and same-name streaks (any args).
// Returns true and a message if a loop is detected.
func detectLoop(history []string) (bool, string) {
	// exact-repeat signal: identical calls in a row
	if len(history) >= loopDetectionThreshold {
		last := history[len(history)-1]
		count := 0
		for i := len(history) - 1; i >= 0; i-- {
			if history[i] == last {
				count++
			} else {
				break
			}
		}
		if count >= loopDetectionThreshold {
			return true, fmt.Sprintf("Detected %d repeated calls to %s", count, last)
		}
	}
	// same-name signal: the same tool over and over, args varying (the args
	// of a stuck call often drift — composio steps, metrics — so byte-exact
	// matching misses the loop; the tool name does not). Only count entries
	// whose args stay SIMILAR to the previous one: genuine enumeration
	// (manage_playbook over 7 distinct playbooks) reuses the tool with
	// meaningfully different args — that is progress, not a loop.
	if len(history) >= loopNameThreshold {
		lastName := toolName(history[len(history)-1])
		count := 0
		for i := len(history) - 1; i >= 0; i-- {
			if toolName(history[i]) != lastName {
				break
			}
			if i < len(history)-1 && !similarArgs(history[i], history[i+1]) {
				break // args jumped to something genuinely different: enumeration
			}
			count++
		}
		if count >= loopNameThreshold {
			return true, fmt.Sprintf("Detected %d consecutive calls to %s without progress", count, lastName)
		}
	}
	return false, ""
}

// similarArgs reports whether two history entries share a long common
// prefix — true for drifting args ("step DISCOVERING 0/1" vs "0/2"), false
// for enumeration ("name:ai-news-daily" vs "name:facebook-daily-ai-post").
// ponytail: common-prefix is a lazy similarity proxy; revisit with a real
// edit-distance if a stuck loop with reordered args ever slips through.
func similarArgs(a, b string) bool {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	max := len(a)
	if len(b) > max {
		max = len(b)
	}
	return max > 0 && float64(n)/float64(max) >= 0.7
}

// toolName extracts the tool name from a history entry "name(args)".
func toolName(entry string) string {
	if i := strings.Index(entry, "("); i > 0 {
		return entry[:i]
	}
	return entry
}

// snapshotKey is the context key for the snapshot updater callback.
type snapshotKey struct{}
