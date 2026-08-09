# Schedule Reliability — Harness Fix

## Question

What is the smallest harness change that ends silent schedule loss: no starvation, no downtime miss, and a record when a run was due but didn't happen?

## Resolution

**Three separable changes, smallest first:**

### A. Per-schedule goroutine (fixes starvation) — ~10 lines
Fire each due schedule inside `safeGo` instead of inline in the dispatcher loop. One long routine can no longer block the ticker or sibling schedules.

Risk: two schedules of the same playbook could fire concurrently → guard with a per-name mutex (same playbook, one run at a time). SQLite is WAL + single process, so the DB side is already safe.

### B. Boot-time catch-up (fixes downtime) — ~15 lines
On startup, scan schedules once: if the scheduled time for today has passed and `LastRun` is not today, **fire it late immediately** (bounded: only the most recent missed run per schedule, only same-day). The alternative — report-only — leaves the work undone; catch-up matches the project's "recovery" principle (Phase 0 exit condition: *survive interruptions*).

Risk: catch-up fires a routine the owner expected to skip (e.g. weekday routine after a weekend outage). Mitigation: log + notify the catch-up run like any other run; the routine record shows when it ran.

### C. Missed-run record + notify (fixes silence) — ~20 lines
When a schedule's window passes with no run and no catch-up happened, write `"missed": true` (+ `MissedAt`) into `schedules.json` and send one Telegram notice via the existing outbox pattern. Same failure-class visibility the loop already gives fired-but-failed runs — now extended to never-fired.

## Options considered

**A. Only the goroutine split** — ends starvation but downtime misses stay silent. Too small; the gap is "silent", not "starved".

**B. Widening the window (e.g. 5 min)** — treats the symptom, not the cause; doesn't help at all when Mino is down. Rejected.

**C. Full cron-style scheduler** — overkill; the roadmap says playbooks, not a job framework. Rejected.

## Out of scope

- Changing the 1-minute ticker cadence or timezone semantics
- Webhook-mode Telegram (separate decision, DECISIONS §11)
- Missed-run alerting *policies* (always notify; per-schedule opt-out can come later)
