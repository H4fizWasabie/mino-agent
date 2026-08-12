# Harness — Post-mortem: Mino diagnoses its own failures from its own traces

Status: **IMPLEMENTED** (code + tests; rides the held v2.8.11 release)

## Prerequisite (shared with CTX-018, CTX-019): the brain knows it is the brain

The LLM must be explicitly aware it is Mino's mind driving Mino-the-harness — that its tools, memory (facts, graph, consolidation), loop, session notes, and traces are its own body's subsystems. Without this identity block, self-repair is incoherent: reading `session_notes`/`audit.jsonl` is "snooping some app's logs," not "reading my own vital signs," and "my loop is spinning" is meaningless. The anti-lie rule (CTX-016) gets its teeth because a self-claim is a claim about the body, and the body (db, trace) is the truth. The harness supplies this identity too (a prompt block) — the brain doesn't invent being Mino; Mino tells it. This must land before any of the three levels.

## Framing (harness, not LLM)

The LLM is a component, not the agent. Mino (the harness) owns providing the trace, the context, and the grounding rule. When a run fails, the harness instruments the failure and the LLM *renders* a diagnosis — but the **responsibility for a grounded diagnosis is the harness's**: it must make the trace available and enforce "cite the trace line, or label it a hypothesis."

## Why

Every session failure was a harness gap, not a model fault: FB 50-iter churn (no iteration awareness), consolidation "lie" (tool returned 0 silently, no grounding rule), token leak (tool not in schema), stale URL (no freshness signal). Automating the post-mortem turns each failure into a verified learning event — and feeds the daily AI-concept library's "how Mino uses this" on refresh.

## Scope (low risk — happens after the fact)

- A post-mortem mode/playbook: on a failed run, reads `runs/<id>/` + the day's trace and emits a wayfinder-style ticket: symptom → trace evidence → root cause (or hypothesis) → fix.
- Grounding rule: every mechanism claim cites the actual trace line, or is labeled a hypothesis. No citation = a story, not a diagnosis (CTX-016 applies to self-narrative).
- Output format mirrors CTX/VFY tickets (symptom, evidence, root cause, fix).

## Implementation (2026-08-12)

- **`post_mortem` tool** (post_mortem.go): extracts bounded failure evidence for the newest failed run (or a named playbook): parse-failures with iteration numbers, outcome-claim-contradictions, stage-rewrite streaks, iteration usage, final reply — from the run's trace window. The LLM renders the ticket from the returned evidence (no bash scanning → no churn).
- **Auto-injection** (playbook.go `formatPlaybookResult`): when a run fails, the run_playbook tool result now appends the failure evidence — the LLM is told what happened immediately, no extra call.
- Tests: `TestNewestFailedRunSkipsComplete`, `TestTraceFailureEvidenceBoundsWindowAndSignals`. Full suite 529 pass.
- The earlier on-demand post-mortem playbook was rewritten tool-driven (call `post_mortem`, write ticket, report) so it can no longer churn.

## Acceptance criteria

- [x] Triggered on a failed run (auto-injection into the run_playbook result) or on demand (`post_mortem` tool).
- [x] Diagnosis cites specific trace/tool-call lines (iteration numbers) — the evidence is returned with citations for the LLM to restate.
- [x] Writes a ticket-format markdown (the LLM renders the wayfinder-style ticket from the tool's evidence).
- [x] The LLM (component) renders; the harness supplies the trace + enforces grounding.
- [x] Live (2026-08-12, v2.8.11-rc2): `post_mortem` diagnosed the real FB run that hit its 50-iteration cap — returned iterations=50, final reply, gate decision. Also fixed `daily-ai-concept` missing root CONTEXT.md (would have failed loadPlaybookWorkspace).