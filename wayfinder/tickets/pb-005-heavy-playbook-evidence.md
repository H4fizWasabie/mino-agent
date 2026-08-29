# Playbooks -- bounded evidence for heavy runs

Status: **OPEN** (child of PB-001)

## Destination

Give weekly audits, post-mortems, and similar heavy playbooks bounded evidence
packets instead of letting an LLM scan an entire historical workspace.

## Scope

- Mechanically select the date range, run class, sample, and evidence budget.
- Exclude test traffic before judgement.
- Preserve pointers to full artifacts for follow-up inspection.
- Reuse the existing weekly-audit sampling and post-mortem extraction paths.
- Keep reports and selected facts in the normal output/distillation pipeline.

## Acceptance criteria

- [ ] The evidence selector is deterministic and auditable.
- [ ] The LLM receives a bounded packet with source/run attribution.
- [ ] Full historical artifacts are never implicitly injected into context.
- [ ] Weekly audit and post-mortem behavior remains bounded and resumable.
