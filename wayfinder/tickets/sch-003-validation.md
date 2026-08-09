# Schedule Reliability — Validation

Status: **RESOLVED** (2026-08-09, 28 schedule tests landed)

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

## What landed (playbook_test.go)

- `TestClassifySchedule` — 15-case table over the pure seam: window semantics, catch-up fire, covered-by-run skip (today's run, future LastRun), next-day miss, never-run miss, invalid timezone/time.
- `TestDispatchSlowRunDoesNotStarveSibling` — slow run blocks in the runner; the next minute's pass still fires the sibling. Fails on the old serial dispatcher (it could not even enter pass 2).
- `TestDispatchAlreadyRanTodaySkips` — existing already-ran behavior locked in.
- `TestDispatchFiresInWindowAndClaimsSlot` — in-window fire persists `LastRun`; a second pass in the same window does not double-fire.
- `TestDuplicateScheduleNameFiresOnce` — duplicate rows cannot run the same playbook concurrently.
- `TestCatchUpFiresLateSameDay` — downtime miss fires late on boot, claimed, not marked missed.
- `TestCatchUpRecordsMissedRunAndNotifiesOnce` — old miss: no fire, `missed_at` persisted, one outbox notice containing the schedule name, no duplicate notice on second boot.
- `TestCatchUpNeverRunScheduleIsNotAMiss` — never-run schedule: no fire, no flag, no notice.

Result: `go test ./...` 355 passed; `-race` clean; `go vet` clean. Tests use a real temp SQLite DB (responsibility recording included) with an injected fake runner at the existing `dispatchDueSchedulesAt(core, now, run)` seam.

## Production observation

After deploy, check the dashboard/trace for a week: any `schedule_fire_failed` or new `schedule_missed` entries should be visible in traces and the audit log — the same surfaces the loop already uses. Success criterion: a missed run is either fired late or reported; never both silent.

## Out of scope

- Behavioral eval cases (this is harness behavior, not LLM behavior)
- Testing the 1-minute ticker cadence itself (time-dependent; covered by `dispatchDueSchedulesAt` unit tests)
