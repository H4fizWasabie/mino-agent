# Context Truth — Degeneration guard

Status: **RESOLVED** (closes GitHub issue #147)

## Question

How do we stop a run that is degrading (repeatedly emitting unparseable tool calls) from silently burning to the iteration cap?

## Resolution

`parseFailures` in loop.go is now a **per-turn total**, not a resettable streak — the reset on successful calls is removed. Abort stays at 6, corrective push still escalates at 3.

**Why total, not consecutive:** the #24 circuit breaker (6 consecutive) never fired on 2026-08-10 — the CHEM 15 turn failed at iterations 4, 11–14, 16, 24–26 (9 total), each burst broken by a successful call. A model alternating success and malformed markers degrades just as surely as one stuck in a streak.

**Deviation from the ticket's "3 consecutive" proposal:** the deployed baseline was already 6-consecutive. 6-total preserves that threshold while closing the alternation loophole; 3-total would abort legitimate recovering turns (the existing escalation test — 3 failures then done → complete — must keep passing).

## Acceptance criteria (all met)

- [x] Alternating fail/success pattern aborts at 6 total failures (`TestLoopAbortsAfterSixTotalParseFailuresWithInterleavedSuccesses`: 7 broken + 7 successful calls, abort at iteration 11, not the 30-cap)
- [x] Consecutive pattern still aborts at 6 (existing `TestLoopAbortsAfterSixConsecutiveParseFailures` unchanged)
- [x] 3-failure-then-recover still completes (existing `TestLoopEscalatesParsePushAfterThreeFailures` unchanged)
- [x] `-race` clean

## Validation

- `go test ./...` — 503 pass
