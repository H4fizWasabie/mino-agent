# Runtime Self-Management — Binary self-rollback

Status: **RESOLVED** (wayfinder ticket, RUN-004 — GitHub issue #218)

Resolved 2026-08-16: `rollback.go` — `mino update` keeps the running binary
at `exe.prev` before the swap, then health-checks the new binary against a
staged copy of the live state (the stage-smoke shape: same exclusions,
schedules.json removed, TELEGRAM_BOT_TOKEN stripped) and requires
/api/universe to answer within 30s. The candidate is a black box — it runs
the REAL boot path, no self-check flag, so the checked path cannot drift
from production. Boot failure (candidate exits before readiness),
health-check failure (no answer in the window), or owner call (`mino
rollback`) restores the previous binary through the same atomic rename the
updater and the emergency lane use, with a `rollback=` ledger line. The
swap is journaled as a `binary.swap` op (entity = binary path, before/after
= path+version+sha256) under RUN-002 discipline — mutation first, journal
failure tears the swap back down; a reverted swap is marked `rolled_back`
via the RUN-001/002 status seam. Tests: `TestVerifyNewBinaryHealthy` (real
binary built in-test, boots the real boot path),
`TestVerifyNewBinaryBootFailure`, `TestVerifyNewBinaryTimeout`,
`TestApplyUpdateHappyPathSwapsJournalsAndPassesHealthCheck`,
`TestApplyUpdateHealthFailureRevertsAndMarksRolledBack` (failing boot
drives the real revert decision),
`TestApplyUpdateJournalFailureRevertsSwap`,
`TestDoRollbackRestoresAndMarksRolledBack`,
`TestDoRollbackNothingToRollBack`, `TestStageStateSafetyList`.

## Question

`mino update` verifies SHA256 and swaps atomically — but nothing answers
"did it come up healthy, else revert". A broken release wedges the VPS
until a manual emergency deploy.

## Decisions so far

- **Health check = staged copy of live state + boot + /api/universe** —
  stage-smoke's boot shape, not its chat turn (that's the release gate;
  RUN-005 covers config self-heal). Extensions excluded from the staged
  copy — the staged boot validates the mino binary itself; spawning
  duplicate extension processes on every update was the alternative.
- **No self-check flag**: the candidate runs the real boot path — a flag
  would be a second boot path that could drift from production (the
  RUN-003 review lesson: test the real boundary).
- Revert = same atomic rename + ledger line as the update — no parallel
  copy mechanism (the emergency lane's `.new` + `mv` shape).
- Detection vocabulary: boot failure, health-check failure, owner call
  (`mino rollback`). The running process keeps its inode until restart —
  systemd stays the only thing that applies the reverted binary.

## Out of scope

- Approval tier (RUN-006) and config self-heal (RUN-005)
- The full stage-smoke chat-turn rehearsal (release lane only)
