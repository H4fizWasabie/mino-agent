# Ship generic playbooks as install defaults (data-only)

Status: **OPEN** (GitHub issue #232)

## Question

A fresh Mino install gets the engine but not the operating context: only the
hello-world example playbook is seeded (`CreateExamplePlaybook`, playbook.go),
while the generic reusable playbooks (morning-briefing, ai-news-daily,
weekly-cost, weekly-audit) exist only in `playbooks-vps/playbooks/` in the
repo — not wired into the binary, not installed by default. A new owner must
author everything from scratch.

## Proposed approach (data-only — no Go per playbook)

The discovery mechanism already exists: `ListPlaybooks` scans `~/.mino/playbooks/`
at boot. So defaults are just template files + a copy step into the home
directory on first boot (alongside the hello-world seed), not per-playbook Go
code.

## Constraints

- Data-only: no engine changes, no Go per playbook.
- Copy must be idempotent (don't overwrite a playbook the owner has edited —
  only seed when absent).
- Generic playbooks must not leak owner-specific data (the VPS copies in
  `playbooks-vps/` may carry owner context; shipping defaults needs sanitized
  templates).

## Status check (2026-08-17)

Not started. `playbook.go` still seeds only hello-world; no changelog entry.
