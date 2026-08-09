# Schedule Reliability — Root Cause

Status: **CONFIRMED** (2026-08-09, verified against playbook.go @ 2d5f7b5c)

## Question

Why do scheduled playbook runs disappear without a trace, and when did "schedule died" become a known failure class?

## Resolution

**The dispatcher is one serial goroutine with a 1-minute fire window and no catch-up. A run that isn't fired in its window is skipped silently — no trace, no LastError, no audit entry, no notification.**

Three distinct mechanisms, all in `playbook.go`:

1. **Starvation.** `runScheduleDispatcher` (playbook.go:1051) is a single goroutine started once in app.go:182. `dispatchDueSchedulesAt` (playbook.go:1065) calls the playbook runner **synchronously** in that goroutine. A routine that takes 40 minutes blocks the ticker; every schedule whose window falls inside that span is skipped.

2. **Narrow window.** Each schedule fires only within `[sched, sched+1min)` (playbook.go:1085). Anything outside → `continue`. The 1-minute ticker has no margin: a tick that arrives 2 seconds late after a slow iteration misses the window entirely.

3. **No catch-up, no record.** The "already ran today?" check (playbook.go:1090) means a never-ran schedule is simply eligible tomorrow — yesterday's miss is never noticed. `LastError` is only written when a run *fires and fails* (playbook.go:1115). "Due but never fired" has no representation in `schedules.json`, the audit log, or the dashboard. If Mino is down at 13:00 and restarts at 13:10, the 13:00 run never happened and nobody is told.

## Context

- AGENTS.md names "schedules dying" as the canonical urgent-bug class: *"🔴 Urgent — a bug actively breaking something (e.g. schedules dying) → release immediately."* The project already knows this failure mode hurts.
- DECISIONS.md §11 documents Telegram message loss during downtime as a **deliberate** tradeoff. There is no equivalent section for schedule loss — this is **undecided, not chosen**.
- The visibility machinery already exists and works for fired-but-failed runs: `logTrace("schedule_fire_failed", ...)` + `core.auditLog` (playbook.go:1109-1113). Only the never-fired case is invisible.

## Confirmation (verified against playbook.go @ 2d5f7b5c)

- `runScheduleDispatcher` (playbook.go:1051) is a single goroutine started once in app.go:182; the only caller of `dispatchDueSchedules` (grep: no other callers) — no boot pass exists.
- `dispatchDueSchedulesAt` runs the playbook **synchronously** inside the tick loop: the loop does not return until the playbook completes.
- Window check (playbook.go:1085): `if nowInLoc.Before(today) || nowInLoc.After(today.Add(time.Minute)) { continue }` — a bare `continue`, no record of the skip.
- "Already ran today" (playbook.go:1090): compares `LastRun`'s calendar day to today. A never-ran schedule is simply eligible tomorrow; yesterday's miss is never noticed.
- `LastError` is written only when `startRoutine` fails (playbook.go:1115); a run that never fired has no field, no trace, no audit entry.
- `LastRun` is set to the **completion** time (playbook.go:1126) — fine for day-guarding, but it means the fire window stays unguarded against double-fires once runs go parallel (fix A must claim the slot at start).

All three mechanisms in the diagnosis reproduce against the code; no additional root causes found.

## Patterns to investigate

1. Does anything besides the ticker call `dispatchDueSchedules`? (grep: no — no boot-time catch-up.)
2. Does the dashboard render schedule health / last-run age? (Unknown — check dashboard.go.)
3. How often do long routines (>1 min) actually run? The 13:00-style business playbooks are the candidates.
