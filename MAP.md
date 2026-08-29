# Mino Runtime Map

Authoritative orientation for Mino's own runtime. This file describes the
filesystem under `/home/mino/.mino`; use purpose-built tools or `read_file` to
verify current state before claiming a changing fact.

## Runtime root

`MINO_HOME=/home/mino/.mino`

- `SOUL.md` — editable Mino voice and operating identity
- `MAP.md` — this global runtime map, loaded into chat and playbook runs
- `mino.env` — runtime environment values
- `providers.json` — model/provider configuration
- `schedules.json` — recurring schedule definitions
- `playbooks/` — recurring workspace definitions and run evidence
- `agents/` — shared legacy personas and agent files
- `skills/` — installed skills
- `memories/` — durable memory files
- `working_memory.md` — current working memory
- `state.db`, `mino.db`, `reminders.db` — runtime databases
- `audit.jsonl` — tool-call audit trail
- `usage.jsonl.imported` — imported usage records
- `outbox/` — pending outbound notifications
- `results/` — tool-result trails and reports
- `traces/` — execution traces
- `data/` — auxiliary durable ledgers
- `backups/` — recovery copies
- `extensions.json`, `mcp.d/`, `oauth.d/` — runtime integrations

## Playbook routing

Each active workspace is under `playbooks/<name>/`:

1. `AGENTS.md` — optional workspace map; currently absent from the live
   workspaces, so do not assume it exists.
2. `CONTEXT.md` — root purpose, routing, inputs, outputs, and safety.
3. `stages/NN-name/CONTEXT.md` — stage contract loaded for the active stage.
4. `stages/*/references/` — selective stable references when present.
5. `runs/<run-id>/` — working evidence: `state.json`, stage outputs, and
   external-side-effect markers.

Supporting workspace files include `config.md`, `persona/`, `persona.md`,
`tools/`, and mechanical stage scripts. The existing
`tools/link-check.sh` checks documentation links and orphaned non-runtime
files; run it from the playbook directory when auditing routing health.

## Live playbooks

- `ai-news-daily`
- `daily-ai-concept`
- `facebook-daily-ai-post`
- `gmail-daily-cleanup`
- `instagram-daily-capability`
- `morning-briefing`
- `post-mortem`
- `reddit-karma-builder`
- `threads-community`
- `threads-replies`
- `threads-tribal-battle`
- `threads-workplace-drama`
- `weekly-audit`
- `weekly-cost`

The hidden `.backup-daily-ai-concept-root-20260812/` directory is a backup,
not an active playbook.

## Operational rules

- Schedules invoke playbooks; they are not a second executor.
- A playbook run owns its own `runs/<run-id>/` evidence and must not overwrite
  prior run evidence.
- Failed or incomplete work is evidence. Read the latest run state and outputs
  before retrying or reporting completion.
- The filesystem and current tool read-backs outrank this map when they differ.
