# Harness — Post-mortem: Mino diagnoses its own failures from its own traces

Status: **OPEN** (wayfinder ticket, CTX-017)

## Framing (harness, not LLM)

The LLM is a component, not the agent. Mino (the harness) owns providing the trace, the context, and the grounding rule. When a run fails, the harness instruments the failure and the LLM *renders* a diagnosis — but the **responsibility for a grounded diagnosis is the harness's**: it must make the trace available and enforce "cite the trace line, or label it a hypothesis."

## Why

Every session failure was a harness gap, not a model fault: FB 50-iter churn (no iteration awareness), consolidation "lie" (tool returned 0 silently, no grounding rule), token leak (tool not in schema), stale URL (no freshness signal). Automating the post-mortem turns each failure into a verified learning event — and feeds the daily AI-concept library's "how Mino uses this" on refresh.

## Scope (low risk — happens after the fact)

- A post-mortem mode/playbook: on a failed run, reads `runs/<id>/` + the day's trace and emits a wayfinder-style ticket: symptom → trace evidence → root cause (or hypothesis) → fix.
- Grounding rule: every mechanism claim cites the actual trace line, or is labeled a hypothesis. No citation = a story, not a diagnosis (CTX-016 applies to self-narrative).
- Output format mirrors CTX/VFY tickets (symptom, evidence, root cause, fix).

## Acceptance criteria

- [ ] Triggered on a failed run (or on demand: "post-mortem last failure").
- [ ] Diagnosis cites specific trace/tool-call lines, or is explicitly labeled a hypothesis.
- [ ] Writes a ticket-format markdown to the wayfinder-style output.
- [ ] The LLM (component) renders; the harness supplies trace + enforces grounding.