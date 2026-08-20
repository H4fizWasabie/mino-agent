# Context Bloat & Run Degeneration — daily-ai-concept 50-iteration burn (CTX-025)

Status: **RESOLVED** (2026-08-20 — all three fix arms landed): **A** contract-bound via scripting daily-ai-concept (scripted playbook, ~0 LLM tokens; failure class gone for it), **C** code mode (#271, v2.18) removed the malformed-serialization class, **B** harness recovery shipped as the stage-scoped divergence detector + state-reset turn (loop.go): in-tokens > 3× iteration-1 baseline within the first 10 iterations while required outputs are still missing → one midflight_signal divergence event, context truncated back to the stage prompt, required outputs re-injected, exploration forcibly stopped. The parse-failure/empty-args half of fix-B is inherently covered by code mode (no JSON to mis-serialize; the malformed-marker guard already exists). Live evidence: the chat-driven instagram test's 29-iteration explore loop is the pattern the detector now catches. Tests: `TestLoopDivergenceResetClearsExploration`, `TestLoopDivergenceSkippedOutsideStage`.

## Incident

Run `20260819T133043.232285396Z` (daily-ai-concept / stage 01-learn-and-store) failed at
the 50-iteration cap (`runtime iteration_limit`), 12 minutes wall time, **982k input
tokens**, zero output (no `learn-log.md`, no library fact written). Previous run
(08-18) completed in 96s / 12 iterations. First failure ever for this playbook.

## Evidence (traces 2026-08-19, run-scoped)

- 50 llm calls / 25 tool calls / 27 `stage_output_missing` / 9 `midflight_signal`
  (repetition) / 1 `near_cap` / 1 `tool_call_parse_failed` (iteration 2).
- **Context bloat**: llm in-tokens grew 4,619 → 29,025 across the run (6.3×).
  Mino's own analysis: `remember` calls pull ~100k-char connected-graph dumps —
  consistent with the measured token growth (100k chars ≈ 25k tokens).
- **Degeneration after iteration-2 parse failure** (text-marker call with garbled
  path `/home/mino/.playbooks/` — missing `.mino`): 4× `bash {}` empty args, 1× empty
  tool name, 2× `__raw_arguments__` raw JSON. Model ignored all 9 repetition signals.
- Core task never reached: no `save_note`/`manage_memory`, `write_file` only at call
  25 (raw args), output file never produced.

## Root cause (reconciled: harness traces + Mino's diagnosis)

Two compounding factors:

1. **Oversized `remember` results** — the stage's remember calls return huge
   connected-graph dumps (~100k chars each) into context, ballooning every
   subsequent LLM call (the measured 4.6k → 29k in-token growth).
2. **No recovery from degeneration** — after the iteration-2 parse failure the model
   spiraled (malformed serialization + explore-forever), and the existing
   guardrails (repetition signal, near_cap) only warn; nothing resets the turn or
   re-states the binding contract. The 50-cap was the only hard stop.

## Resolution direction

Smallest first:

- **A. Contract bound (CONTEXT.md edit, no code)**: bound the memory query —
  "at most ONE `remember` call, results are advisory; do not read full graph
  dumps" + explicit converge-to-write pressure ("if research does not yield in
  N steps, write from reliable knowledge and mark unverified" — the contract
  already says this; make the bounded-result rule explicit).
- **B. Harness recovery (code)**: state-reset turn when parse-failure count ≥ N or
  empty-args pattern detected (clear noise, re-inject the contract + required
  output path); divergence detector (in-tokens > 3× baseline within 10 iterations
  AND read-only calls → hard reset signal). Fits the #171 midflight machinery.
- **C. Code mode (#271)** eliminates the malformed-serialization class entirely —
  no JSON to mis-serialize; failure is visible stderr. This incident is direct
  evidence for that design.

## Measurable

- No 50-cap failure on daily-ai-concept for 2 weeks post-fix.
- llm in-tokens/turn bounded (< 15k for this playbook); wall time back to
  ~1.5–2 min (baseline 08-18: 96s).

## Options considered

- Raising the iteration cap — treats the symptom; the run was not converging.
- Skipping the harness recovery (contract-only) — cheaper, but the parse-failure →
  degeneration path can hit any playbook, not just this one.

## Out of scope

- Code-mode implementation (#271) — referenced, not done here.
- The 50-iteration cap itself.
