# Playbook Contract Consolidation — One Truth, Not 14 Copies

Type: `wayfinder:prototype` (HITL — owner reacts to a concrete shape)

## Question

What does the shared playbook contract look like, so the exclusion glob, judgment gate, anti-skip rule, and "EXACTLY ONCE" boilerplate live in one place instead of ~200 duplicated lines across 14 stage files?

## Context

- 579 lines of stage prose, ~200 copy-pasted (ALL_PLATFORMS glob in 11 stages, anti-skip rule in 8, "EXACTLY ONCE" in 10, near-identical judgment gates).
- Today's fixes required hand-editing 8 CONTEXT.md files — every contract change is a 14-file diff.
- The harness already resolves `references/`-prefixed inputs from the stage dir — a shared contract file is mechanically supported today.
- Deferred from the ponytail audit as regression-risk; this ticket makes the shape decision first, so execution is mechanical.

## Ask

- Shape: one shared `platform-rules.md` referenced by all playbooks (with per-playbook deltas), or per-playbook `references/`?
- What moves wholesale (exclusion glob, anti-skip rule, Telegram-report rule) vs what stays per-playbook (process steps, tools, outputs)?
- Migration guard: how do we prove no regression (same rendered prompt before/after for one playbook)?
