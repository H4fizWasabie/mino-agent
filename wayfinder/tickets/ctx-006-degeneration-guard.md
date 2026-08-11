# Context Truth — Degeneration guard

Status: **OPEN**

## Question

How do we stop a run that is degrading (repeatedly emitting unparseable tool calls) from silently burning to the iteration cap?

## Evidence

2026-08-10 23:00–23:04 turn (audit_events, session tg:1794722543): 9 `tool_call_parse_failed` events — iterations 4, 11–14 (4 failures on one marker), 16, 24–26 (3 failures on one marker). The model was writing malformed `[tool_call:]` text markers; each miss cost an iteration and grew the re-sent context, making the next call sloppier. The run ended at the 30-iteration cap with "(stopped after 30 iterations)".

This is the #110 failure class (543 parse-failure events on 2026-08-08) — the lenient parser reduces the cost per failure but cannot stop the spiral.

## Design sketch

- In the loop (loop.go), count consecutive `tool_call_parse_failed` iterations per turn.
- At N consecutive failures (proposal: 3), stop the turn with an explicit result: "stopped after N failed tool-call attempts" + the snippet of the last failing marker (the #110 trace already logs the snippet).
- This is an early-stop, not a cap change: `MINO_MAX_ITERATIONS=30` stays as the outer bound.

## Acceptance criteria

- [ ] 3 consecutive parse-failure iterations end the turn with a bounded, self-diagnosing message
- [ ] Normal turns (isolated parse failure, then recovery) unaffected — the existing retry path still works
- [ ] Regression test reproducing a 3-failure spiral at the loop seam
- [ ] `-race` clean
