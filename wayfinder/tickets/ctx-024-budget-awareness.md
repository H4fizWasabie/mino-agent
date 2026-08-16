# CTX — Mino doesn't know its own max-token budget

Status: **IMPLEMENTED** (code + tests, closes #240)

## Why

The model ran 30-iteration turns at 30k+ context with no awareness of the 100k ceiling (live 2026-08-16, portfolio task) and made no budget-aware planning (no "this is getting heavy, compact or wrap up"). The ceiling is a harness constant (`MINO_CONTEXT_CHARS`, default 100000); the model was never told it. This supersedes the MAP's earlier "context-budget telemetry (#2) — rejected" decision: the #171 stop-on-spin guard stopped *repetition*, but the live evidence showed the model still burned context without ever being told its own budget — the two are complementary, and the owner's live evidence reversed the rejection.

## Mechanism

1. **Per-turn budget block** (app.go, `contextBudgetBlock`): every user turn's tail gains `context budget: N chars used of C ceiling (P%), R headroom` — chars used = system + the messages the harness already built (same accounting the harness's own trimming uses; no new plumbing), ceiling = `Settings.ContextChars`, headroom = ceiling − used. Placement follows the clock pattern exactly (app.go:310): appended to the last user message per turn, never the byte-stable system prompt, so the provider's prefix cache stays warm.
2. **Threshold warning**: at ≥70% of the ceiling (the 90% level is the same locked template with a higher N, so one gate covers both), the block gains the warning line — template LOCKED by owner decision: "context at N% of the ceiling — compact or consolidate (manage_memory/consolidate), or wrap up with a status report of what's done and what remains." Exactly two options, both honest. The warning is INFORMATIONAL: CTX-003 (state both numbers), verify-then-claim, and action-grounding (CTX-016) are absolute rules with no context-budget escape hatch — stated in the code comment next to the warning. Never "finish quickly" or anything implying skipping verification or rushing.
3. **Guardrail test**: the warning text must contain "compact" and "status report" and must NOT contain "skip", "quickly", or "rush" — a future edit degrading the safe-options wording fails the suite. `contextBudgetBlock` is registered in `promptAssemblySeams` (REL-04).

## Tests

- `TestContextBudgetBlockGuardrail` — the locked template's safe-option wording and banned words.
- `TestContextBudgetBlockNumbersAndThresholds` — exact numbers on a small turn (no warning), warning at 70% and 90% with real N, over-ceiling clamp to 100%/0 headroom, zero ceiling → no block.
- `TestContextBudgetBlockInTurnTail` — real turn through `RespondForContext` (httptest provider): a small turn's tail carries the block with real numbers and no warning; a history-seeded turn near the ceiling carries the warning with a pct ≥ 70.

## Acceptance criteria

- [x] Budget block in the per-turn tail (same placement as the clock) with real numbers.
- [x] Warnings fire at 70%/90% with the locked template.
- [x] Guardrail test passes; small turns show the block but no warning.
- [x] CHANGELOG per format; full suite green incl. -race; discipline tests pass.
