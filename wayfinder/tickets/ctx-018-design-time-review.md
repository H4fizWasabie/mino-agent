# Harness — Design-time: Mino audits its own playbook contracts against agentic principles

Status: **IMPLEMENTED** (code + tests; rides the held v2.8.11 release)

## Prerequisite (shared with CTX-017, CTX-019): the brain knows it is the brain

See CTX-017 — the LLM must be explicitly aware it is Mino's mind driving Mino-the-harness (its tools, memory, loop, traces are its own body). A design-time audit of "my own playbook contracts" is only coherent if the brain knows the contracts are its own body's wiring. The identity block must land first.

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

## Implementation (2026-08-12)

- **`audit_playbook` tool** (audit_playbook.go): extracts each stage's contract and applies deterministic agentic-principle checks — research boundedness (commit/boundary keywords), verification, grounding/fabrication guard, size, tool references. The LLM renders a RISK-FLAG list from it (flags are risks, not assertions).
- **Adaptive design-time gate** (`needsPlaybookAudit`): auto-audits ONLY when the playbook is new, its last run failed, or a stage contract changed since the last run. A stable, recently-successful playbook skips the audit — no wasted resources (the owner's concern). When the gate fires, `stageRiskFlags` is injected into each stage prompt before execution so the LLM sees the risk and avoids the churn.
- Complements existing harness validation (`validateWorkspaceStageTools`, `stageRetrySafe`) with the agentic-principle layer.
- Tests: `TestAuditPlaybookContractsFlagsRisks`, `TestNeedsPlaybookAuditAdaptive`, `TestStageRiskFlags`. Full suite 532 pass.

## Acceptance criteria

- [x] On demand ("review playbook X"): `audit_playbook` emits per-stage flags.
- [x] Each flag references the specific contract keyword + the agentic principle (research boundedness, verification, grounding).
- [x] Flags are framed as risks (churn / unverified / fabrication), never as confirmed outcomes.
- [x] The harness supplies the lens + review surface; the LLM renders the flags.
- [x] Adaptive gate: auto-audit injects risk-flags into stage prompts ONLY when the playbook is new / recently failed / contract changed — a stable playbook costs nothing extra.