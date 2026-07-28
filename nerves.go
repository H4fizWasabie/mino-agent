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

	readOnlyTools := w.Tools.ObserveOnly()
	schemas := readOnlyTools.Schemas()

	if w.Client == nil {
		replyFunc("(provider not configured)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	messages := []Message{{Role: "user", Content: query}}
	resp, err := w.Client.CreateContext(ctx, sessionID+"_intr", MainModel, messages, 1024, system, schemas)
	if err != nil {
		replyFunc(fmt.Sprintf("(interrupt error: %v)", err))
		return
	}

	reply := extractText(resp.Content)
	if reply == "" {
		reply = "(no response)"
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
Use the tools available to inspect files, recall facts, or check state if needed.
Do NOT modify anything, create files, or run write operations.
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

var loopDetectionThreshold = 3

// detectLoop checks if the last N tool calls are identical.
// Returns true and a message if a loop is detected.
func detectLoop(history []string) (bool, string) {
	if len(history) < loopDetectionThreshold {
		return false, ""
	}
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
	return false, ""
}

// --- Read-only tool filtering ---

// ObserveOnly returns a lightweight registry copy with only BehaviorObserve tools.
func (r *Registry) ObserveOnly() *Registry {
	sub := &Registry{tools: make(map[string]*Tool)}
	for name, t := range r.tools {
		if t.Behavior == BehaviorObserve {
			sub.tools[name] = t
		}
	}
	return sub
}

// snapshotKey is the context key for the snapshot updater callback.
type snapshotKey struct{}
