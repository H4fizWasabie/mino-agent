package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Alert checker — DECISIONS.md §18.1: error rate and dead man's switch.
// Runs on the Scheduler goroutine, checks every 5 minutes.
//
// Two conditions:
//  1. High error rate: > MINO_ALERT_ERROR_RATE (default 0.10) errors in last hour
//  2. Dead man's switch: no tool calls in MINO_ALERT_SILENCE_HOURS (default 6)
//
// Delivery: Telegram DM if configured, else slog.Error.

type alertState struct {
	mu                sync.Mutex
	lastErrorAlert    time.Time
	lastSilenceAlert  time.Time
	stopCh            chan struct{}
}

var alerts = alertState{stopCh: make(chan struct{})}

func checkAlerts(db *sql.DB, notifyFn func(string), checkInterval time.Duration, loc *time.Location) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			checkErrorRate(db, notifyFn)
			checkSilence(db, notifyFn, loc)
			checkExtensionRetryLoops(db, notifyFn)
			checkLoopStall(notifyFn)
		case <-alerts.stopCh:
			return
		}
	}
}

// sqliteNow returns the current time formatted for SQLite datetime comparisons.
// SQLite's datetime('now') produces "2006-01-02 15:04:05", not RFC3339.
func sqliteNow() string { return time.Now().UTC().Format("2006-01-02 15:04:05") }

func stopAlerts() {
	close(alerts.stopCh)
}

func checkErrorRate(db *sql.DB, notifyFn func(string)) {
	cutoff := time.Now().Add(-1 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	var total, errors int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM tool_calls WHERE created_at > ?", cutoff,
	).Scan(&total); err != nil {
		return
	}
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM tool_calls WHERE created_at > ? AND status = 'error'", cutoff,
	).Scan(&errors); err != nil {
		return
	}
	if total == 0 {
		return
	}
	rate := float64(errors) / float64(total)
	threshold := envFloat("MINO_ALERT_ERROR_RATE", 0.10)
	if rate <= threshold {
		return
	}

	alerts.mu.Lock()
	if time.Since(alerts.lastErrorAlert) < 1*time.Hour {
		alerts.mu.Unlock()
		return // rate limit: once per hour
	}
	alerts.lastErrorAlert = time.Now()
	alerts.mu.Unlock()

	msg := fmt.Sprintf("[MINO ALERT] High error rate: %d/%d tool calls failed in the last hour (%.0f%%)",
		errors, total, rate*100)
	slog.Error(msg)
	// The error-rate alert is operational detail — useful to the LLM (readable
	// via the journal / tool_calls DB) but noise for the owner's Telegram. Do
	// NOT page the owner for it; keep the silence (dead man's switch) and
	// extension-stuck alerts as the owner-facing ones.
}

func checkSilence(db *sql.DB, notifyFn func(string), loc *time.Location) {
	hours := envInt("MINO_ALERT_SILENCE_HOURS", 6)
	if hours <= 0 {
		return
	}

	// Night gate: suppress alerts between 10 PM and 7 AM in the configured timezone
	now := time.Now().In(loc)
	if now.Hour() >= 22 || now.Hour() < 7 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour).UTC().Format("2006-01-02 15:04:05")
	var count int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM tool_calls WHERE created_at > ?", cutoff,
	).Scan(&count); err != nil {
		return
	}
	if count > 0 {
		return
	}

	alerts.mu.Lock()
	if time.Since(alerts.lastSilenceAlert) < 1*time.Hour {
		alerts.mu.Unlock()
		return // rate limit
	}
	alerts.lastSilenceAlert = time.Now()
	alerts.mu.Unlock()

	msg := fmt.Sprintf("[MINO ALERT] No tool calls in %d hours — loop may be stuck", hours)
	slog.Error(msg)
	if notifyFn != nil {
		notifyFn(msg)
	}
}

// --- Loop-stall heartbeat (OBS-001) ---
//
// The 2026-08-14 wedge froze the loop while background tickers (edge
// judgment) kept writing traces — so global trace freshness cannot detect a
// stuck session. The signal is per-active-turn staleness: a turn has started,
// no loop event has been traced for MINO_ALERT_STALL_MINUTES, and the turn
// never ended. logTrace feeds this watcher; checkAlerts pages on it.

type loopWatchState struct {
	mu           sync.Mutex
	lastActivity time.Time
	inFlight     int
	alerted      bool
}

var loopWatch loopWatchState

func markLoopActivity() {
	loopWatch.mu.Lock()
	defer loopWatch.mu.Unlock()
	loopWatch.lastActivity = time.Now()
}

func markTurnStart() {
	loopWatch.mu.Lock()
	defer loopWatch.mu.Unlock()
	loopWatch.inFlight++
}

func markTurnEnd() {
	loopWatch.mu.Lock()
	defer loopWatch.mu.Unlock()
	if loopWatch.inFlight > 0 {
		loopWatch.inFlight--
	}
	if loopWatch.inFlight == 0 {
		loopWatch.alerted = false // episode resolved — allow the next page
	}
}

// loopStalled reports whether an in-flight turn has produced no loop
// activity for the threshold — the wedge signature.
func loopStalled(threshold time.Duration) bool {
	loopWatch.mu.Lock()
	defer loopWatch.mu.Unlock()
	return loopWatch.inFlight > 0 && time.Since(loopWatch.lastActivity) > threshold
}

// checkLoopStall pages once per stall episode: a turn is in flight, no loop
// activity for the threshold, and we have not already paged for this episode.
func checkLoopStall(notifyFn func(string)) {
	minutes := envInt("MINO_ALERT_STALL_MINUTES", 10)
	threshold := time.Duration(minutes) * time.Minute
	loopWatch.mu.Lock()
	stalled := loopWatch.inFlight > 0 && time.Since(loopWatch.lastActivity) > threshold
	already := loopWatch.alerted
	if stalled && !already {
		loopWatch.alerted = true
	}
	loopWatch.mu.Unlock()
	if stalled && !already {
		notifyFn(fmt.Sprintf("[MINO ALERT] loop stalled: an active turn has produced no trace for %d minutes — the session may be wedged. Restart Mino if it does not recover.", minutes))
	}
}
