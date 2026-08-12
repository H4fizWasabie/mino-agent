# Harness — Design-time: Mino audits its own playbook contracts against agentic principles

Status: **OPEN** (wayfinder ticket, CTX-018)

## Framing (harness, not LLM)

The LLM is the component that *renders* a contract review; Mino (the harness) owns **surfacing the config + the relevant lens** and defining the review as a *risk-flag* pass. The harness decides what a contract gets audited against and what the output must be.

## Why

Level 2 of the ladder (after CTX-017 post-mortem). Contract-time review prevents failures before they run — "this stage is over-specified, it will churn, bound it" — which is the FB 50-iter churn fix made *preemptive* instead of reactive.

## Scope

- Audits a playbook's stage CONTEXT.md against agentic principles (bounded research, commit boundary, iteration budget, verify-then-claim, tool availability).
- Output is a **risk-flag list**, not assertions: "stage 4 has no commit boundary → likely to churn" as a *risk* grounded in the lens + the stage's actual instructions — never a certainty until run.
- Reuses the daily AI-concept library lenses (loop engineering, context management, grounding).

## Difficulty note

Harder than CTX-017: a design-time review is a *prediction* about behavior, not a *fact* about a past failure. The discipline shifts to "flags, not assertions" — a prediction is a risk until the run confirms it.

## Acceptance criteria

- [ ] On demand ("review playbook X"): emits a risk-flag list per stage.
- [ ] Each flag references the specific contract line + the agentic principle.
- [ ] Flags are framed as risks (may churn / may spin / lacks grounding), never as confirmed outcomes.
- [ ] The harness supplies the lens and the review surface; the LLM renders the flags.