# Observability & reliability — trace-freshness heartbeat, boot reconciliation, log coverage

Status: **OPEN** (wayfinder ticket, OBS-001 — GitHub issue #191)

## Question

The 2026-08-14 session killed two silent failure classes — the bind-error (#188) and the session wedge (v2.10.1) — and **both were found by testing, not by monitoring**. A wedged session was invisible for 19 minutes and would have stayed invisible forever on a session nobody touches. How do we make Mino fail visibly and diagnose-ably, so the next failure is found by a page, not by accident?

## Evidence (2026-08-14)

| Gap | What happened | Current backstop |
|---|---|---|
| No liveness signal for the loop | Session wedge held the mutex 19 min; only a restart recovered; found because a test happened to be watching | Dead-man's switch pages after **6 hours** of tool-call silence — too slow |
| No crash-state reconciliation | The wedged daily-ai-concept run stayed `state.json: running` forever; quarantined by hand (`-wedge-orphan`) | None — the scheduler tolerates orphans, nothing fixes them |
| Silent paths exist | #188 (bind), wedge, and the 2026-08-12 "fabricated consolidation count" all produced zero journal evidence at the moment of failure | Post-hoc trace archaeology |

**The free heartbeat already exists:** the edge-judgment ticker writes a trace event every ~5 minutes (`graph edge judgment edges=N`). Silence in that stream = something is stuck.

## Scope

1. **Trace-freshness heartbeat.** A check (in the existing health-alert ticker) that pages via the established outbox channel when no trace event has been written for N minutes (15 — three missed judgment ticks). This would have caught today's wedge in minutes. The 6h dead-man's switch stays as the slow backstop.
2. **Boot reconciliation.** On startup: runs stuck in `running` across a restart are marked `interrupted` with the crash evidence (the 08-14 orphan class dies); outbox drafts older than a threshold get flagged. Deterministic, no manual quarantine.
3. **Log coverage ("logs everywhere").** Inventory every background path (scheduler dispatch, consolidation, distill, edge judgment, outbox delivery, provider failover, session lifecycle) and confirm each either completes with a journal line, fails with a journal line, or pages. Name the remaining silent ones and close them — the auditability principle: *every path either completes, logs, or pages.*

## Acceptance criteria

- [x] A stuck loop/session produces a page within 15 minutes of trace silence (not 6h) — **shipped**: stall heartbeat pages after `MINO_ALERT_STALL_MINUTES` (default 10) of per-active-turn silence (implemented 2026-08-14, pending release)
- [x] A crashed run's `state.json` is reconciled to `interrupted` on next boot with evidence — no manual quarantine — **shipped**: `ReconcileInterruptedRuns` at startup (implemented 2026-08-14, pending release)
- [ ] The background-path inventory is written into this ticket's resolution: every path has a journal line on success and failure, and the silent ones are named and fixed — **remaining frontier**
- [x] No new metrics infrastructure — the trace journal IS the log — held throughout

## Out of scope

- New observability sinks/dashboards (the traces + journal + dashboard traces page are the surface)
- Changing the 6h dead-man's switch semantics (backstop stays)
- The judgment gap (DRF-001 frontier) — verification is a separate question from visibility
