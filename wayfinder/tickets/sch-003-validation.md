# Schedule Reliability — Validation

## Question

How do we prove a schedule can no longer die silently?

## Resolution

**One Go regression test at the seam that already exists, plus production observation.**

`dispatchDueSchedulesAt(core, now, run)` already takes the runner as an injected parameter (playbook.go:1065) — the test seam was built in by accident of design. No eval framework needed.

### Test 1 — Starvation (table-driven, `playbook_test.go`)
Schedules: S1 at 13:00, S2 at 13:05. Runner for S1 blocks (or sleeps past the window); assert S2 still fires. Without fix A this fails: S2's window is consumed while S1 runs.

### Test 2 — Already-ran vs missed (table-driven)
- S with `LastRun` = today → not fired again (existing behavior, locked in)
- S with `LastRun` = yesterday, time passed → fired late by catch-up (fix B)
- S with `LastRun` = today but run failed → not double-fired (error path unchanged)

### Test 3 — Missed bookkeeping (fix C)
Advance `now` past a schedule's window with the runner never invoked (simulating downtime); assert `schedules.json` gains `"missed": true` + `MissedAt` and the outbox gained one notice per missed schedule.

## Production observation

After deploy, check the dashboard/trace for a week: any `schedule_fire_failed` or new `schedule_missed` entries should be visible in traces and the audit log — the same surfaces the loop already uses. Success criterion: a missed run is either fired late or reported; never both silent.

## Out of scope

- Behavioral eval cases (this is harness behavior, not LLM behavior)
- Testing the 1-minute ticker cadence itself (time-dependent; covered by `dispatchDueSchedulesAt` unit tests)
