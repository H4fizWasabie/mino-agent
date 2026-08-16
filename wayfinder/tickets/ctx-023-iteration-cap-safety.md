# Harness — Iteration-cap safety: bounded contracts, a gate that fires, one retry

Status: **IMPLEMENTED** (code + tests, closes #238)

## Why

weekly-audit hit the 50-iteration cap every run (08-15, 08-16; the 08-16 10:03 rerun after the 10:00 failure succeeded). Root cause was the *contract*, not the runtime: step 2 demanded reading "every published post" across 12+ playbooks × 7 days — inherently 50+ iterations. Two compounding harness gaps: the design-time gate (CTX-018) was supposed to flag unbounded research and didn't (a stray "send EXACTLY ONCE" boundary word laundered the stage into "bounded"), and a capped run had no recovery path (a cap was silent — no alert, no streak).

## Mechanism

1. **Bounded contract (data fix, repo copy only)**: weekly-audit stage 01 now samples — at most the 10 most recent runs per playbook, at most 2 output files per run, hard ≤30-iteration read/score budget, stop when spent. Four scoring dimensions untouched. Live VPS copy is a config sync the moderator/owner applies post-merge.
2. **Gate fires on "every"** (`researchBounded` in audit_playbook.go): bounded = boundary word AND no universal-coverage language ("every"). Shared by the audit output and the run-time `stageRiskFlags` injection. Regression test: "every" + glob + no bound must fail the gate.
3. **One automatic retry on iteration_limit** (playbook.go): a scheduled run capped at the iteration limit re-fires the same occurrence once after a 3-minute delay (the 10:00 → 10:03 evidence window), journaled as `schedule_retry` (trace + audit_events). The claim is in-memory per occurrence — a retry can never retry itself. `alertScheduleHealth` now treats `iteration_limit` as a failure, so a second cap alerts with the cap reason and counts the streak (was: silent).

## Tests

- `TestAuditPlaybookFlagsUnboundedEveryGlob` — "every" + glob + no bound fails the gate; bounded wording stays clean; the run-time injection uses the same check.
- `TestFireScheduleRetriesIterationLimitOnce` — cap → delayed retry → complete: silent, streak reset, journaled.
- `TestFireScheduleAlertsWhenRetryAlsoCaps` — exactly one retry; a second cap alerts with the cap reason and persists the fail streak.

## Acceptance criteria

- [x] Bounded contract produces the four-dimension audit within the iteration cap.
- [x] `audit_playbook` flags an unbounded ("every" + glob + no bound) contract — regression test.
- [x] iteration_limit failure auto-retries once, then alerts — tests.
- [x] Repo contract copy updated; VPS sync flagged to the owner (config change, post-merge).
