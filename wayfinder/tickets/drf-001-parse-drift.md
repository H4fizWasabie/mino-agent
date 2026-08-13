# Quality Frontier — Drift prevention: loop drift is governed, parse drift is active

Status: **OPEN** (GitHub issue #180) — VPS data gathered; principle discussion scheduled for a dedicated session.

## Question

The old drift vector (loop-to-cap) is solved. The new one — repeated unparseable tool-call markers since the deepseek:deepinfra main-model swap (CTX-008) — burns iterations daily. What is the right drift prevention now?

## Root cause / evidence (VPS data, 2026-08-13)

Two drift stories in opposite directions:

- **Loop drift — governed.** `loop_detected`: 82 events total, but **69 in the 07-31→08-06 window** (pre-CTX-006). Only **1 since 08-11**. The degeneration guard works.
- **Parse drift — active.** `tool_call_parse_failed`: 76 events, **71 in 08-08→08-13**, all "text marker found but args did not parse (failure N)". This is the deepseek:deepinfra era. The corrective path (loop.go:430-447) already escalates at failure 3 ("use native function calling, or call the _FLAT variant...") and aborts at 6 — yet the same failure recurs daily across 6 days.
- Audit shows failures 1–5 but **never what shape the model was emitting** — the raw marker is traced (trace `tool_call_parse_failed` carries `marker`) but traces are only read after incidents.
- Related drift signals: `stage_output_missing` 7, `stage_rewrite_streak` 4, `schedule_fire_failed` 3, provider read timeouts on 08-06.

## The genuine gap

We don't know *why* the corrective isn't sticking: is it one recurring malformed shape (fixable parser tolerance) or random noise (model-side)? The answer decides fix-vs-tolerate, and no ticket can be scoped without it.

## Decision points (principle — owner decision, pending)

1. **Dig into the marker shape first** — pull the traced raw markers from the VPS (`tool_call_parse_failed` traces), classify the failure shape, then decide.
2. **Hard switch for this provider?** If the model can't reliably emit text markers, force native tool_calls for deepseek:deepinfra at provider config level instead of per-turn corrective.
3. **Real-provider eval tripwire?** Weekly scheduled playbook running the existing `ep_*.md` evals against the real model (DECISIONS.md:200 admits fakeClient misses real prompt drift). The standing tripwire that makes the *next* model swap boring instead of an incident.

## Recommendation (for the discussion session)

1 first (data decides), then 2 if the shape is stable, and 3 as the standing tripwire regardless.

## Acceptance criteria (to fix after discussion)

- [ ] Parse-failure churn drops measurably (baseline: ~12/day in 08-08→08-13 window).
- [ ] If provider switch: deepseek:deepinfra never falls back to text markers.
- [ ] If eval tripwire: a scheduled run reports eval pass/fail against the real provider.
