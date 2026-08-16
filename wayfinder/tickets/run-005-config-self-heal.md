# Runtime Self-Management — Config self-heal

Status: **RESOLVED** (wayfinder ticket, RUN-005 — GitHub issue #219)

Resolved 2026-08-16: `config_heal.go` — Mino backs up, validates, and
reverts its own config on a bad edit, for the config set in docs/config.md
(providers.json, mino.env, cost-watch.json). Backup = sibling `.prev` files
(the RUN-004 exe.prev shape), refreshed to the last VALID content by the
SIGHUP hook and the write guard; every edit is journaled as a `config.edit`
op (entity = path, before/after = path+sha256 — hash+path as the ticket
allows, mino.env holds secrets). Three triggers, one mechanism:
write-time (`write_file`/`edit_file` validate BEFORE the write lands — an
invalid model edit is refused with the file untouched; valid edits are
backed up and journaled under RUN-002 discipline with teardown on journal
failure), reload-time (the SIGHUP hook `HealConfig` validates the whole set
before `ReloadProviders` runs and reverts any failure from `.prev`, marking
the last op `rolled_back`), owner call (`mino config-rollback <name>`).
Tests: `TestHealConfigRevertsBadProviders` (genuinely unparseable file on
disk drives the real revert decision with content equality),
`TestHealConfigRefreshesBaseline`, `TestHealConfigNoPrevLeavesFile`,
`TestApplyConfigEditRefusesInvalid`, `TestApplyConfigEditJournalsAndBacksUp`,
`TestApplyConfigEditJournalFailureReverts`,
`TestApplyConfigEditNonConfigUntouched`, `TestApplyConfigEditAppendComposes`,
`TestWriteFileToolGuardsConfig` (real tool boundary through the registry),
`TestDoConfigRollbackRestoresAndMarksRolledBack`,
`TestDoConfigRollbackRejects`, `TestValidateConfigPerFile` (671 total green
incl. -race).

## Question

A bad config edit is a quiet log line: a bad SIGHUP reload of providers.json
only logs, mino.env lines the loader skips degrade silently, cost-watch.json
garbage silently falls back to defaults. Nothing reverts.

## Decisions so far

- **Validation bar per file** (parse check + minimal sanity, deliberately
  not a heavyweight validator): providers.json must yield at least one NAMED
  provider — the "don't accept a config that bricks routing" guard, same bar
  as `loadProviders` plus the name check. mino.env must match `loadEnvFile`'s
  exact accept grammar — a line the loader would silently skip is a
  validation failure. cost-watch.json must be a valid JSON object — field
  types stay with the extension's own loader (which silently defaults);
  mino catches the "not even JSON" class, the extension owns its schema.
- **Backup = `.prev` sibling, journal = record**: revert restores the
  `.prev` file, not the journal's before-state — the journal holds hash+path
  because mino.env holds secrets that must not be duplicated into state.db.
- **Revert COPIES `.prev` back and keeps it** — deliberately not RUN-004's
  consuming rename: a revert is idempotent and a re-revert stays possible
  (RUN-004's "run rollback to redo" message landed on an already-consumed
  `.prev`; this shape cannot).
- **Hook placement — both**: write-time in `write_file`/`edit_file` (the
  model edits config through the tools; the refusal happens before the bad
  content lands — for cost-watch.json and mino.env there IS no reload to
  catch a bad edit, only the write guard) and reload-time on SIGHUP
  (`HealConfig` runs before `ReloadProviders` — covers every non-tool
  writer: manual, bash, cost-watch pinning). Refused writes are NOT
  journaled as `failed` — nothing mutated, the error return is the record
  (differs from RUN-001, where a failed install may have partially mutated).
- **New-file teardown**: a config file that never existed before has no
  known-good; on journal failure it is REMOVED (the RUN-003 write_unit
  shape), not restored.
- **Boot-time validation is out of scope**: the ticket's triggers are
  write-time, reload-time, and owner call; a bad boot config is a separate
  failure class (the release lane's stage-smoke gate covers the binary's
  boot path).

## Out of scope

- Approval tier (RUN-006) and binary rollback (RUN-004)
- New config formats or a unified config file (docs/config.md's rejection
  stands)
- The `/root/.mino` split-brain hazard (docs/config.md) — awareness only:
  everything resolves through MINO_HOME, so the heal lands wherever the
  daemon lives
