# Ship generic playbooks as install defaults (data-only)

Status: **RESOLVED** (closes GitHub issue #232, commit pending)

## Resolution

- **Seeding**: `SeedDefaultPlaybooks` (playbook.go) walks an embedded `playbook_defaults/`
  tree and copies absent playbooks into `~/.mino/playbooks/` on boot, alongside the
  hello-world seed (app.go). Data-only — no per-playbook Go.
- **Idempotent**: an existing playbook directory is never overwritten; owner edits win.
- **Sanitized**: templates carry no owner-specific data (verified by
  `TestDefaultPlaybooksSanitized` — no recipient names, no absolute home paths).
- **Validated**: seeded defaults pass the same edit-time validation as any playbook
  (`TestSeededDefaultsValidate`).
- **Shipped defaults**: ai-news-daily, morning-briefing, weekly-cost, weekly-audit +
  shared platform-rules/threads-gate. Personas intentionally NOT baked in — they
  belong to the PSN-001 roster mechanism, not the generic templates.

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
