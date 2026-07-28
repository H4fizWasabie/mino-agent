# Tighten Tool Schema Selection — Wayfinder Map

## Destination

Mino's dashboard token counter shows 14.5k input tokens per trivial turn ("ping", "hello"). Half is tool schemas: 30 tools, ~29k chars of JSON, 50%+ from MCP/composio descriptions that shouldn't appear in casual chat. The destination: **a tool selection mechanism that sends fewer schemas for trivial turns without breaking real workflows**, validated by token-per-turn measurements on the VPS before and after.

Measurable target: trivial turns ("hello", "ping") under 8k input tokens (currently 14.5k); real task turns stay under current levels but with more precise tool inclusion.

## Notes

- **Language:** Go (tools.go, loop.go) + JS (app.js for dashboard display)
- **Existing pieces:** `SchemasForContext` in tools.go, `semanticToolNames` + `searchToolNames`, `essentialToolNames`, `toolFamilies`
- **Playbook architecture:** playbooks already use `tools.Only(names...)` — they don't go through `SchemasForContext`. The fix is only about the main conversation loop.
- **MCP tools are untouchable:** descriptions come from external servers, can't be shortened. Must be excluded when irrelevant.
- **VPS data:** traces at `/home/mino/.mino/traces/`, tool catalog in SQLite, SOUL + skills + playbooks on disk
- **No GitHub:** work stays local, map lives in `wayfinder/`

## Decisions so far

- [tool-001 — Redefine the essential tools set](tickets/tool-001-essentials.md) — 8 tools: read_file, write_file, bash, search_web, remember, save_note, list_playbooks, run_playbook. +3 via family: edit_file, fetch_url, schedule_playbook. Floor drops from 17 → 11.

## Frontier (open tickets)

- [tool-002 — Tune semantic embedding matching](tickets/tool-002-semantic-tuning.md) — Last user message + last assistant reply (one-turn window). Threshold 0.40, cap 8.
- [tool-003 — MCP/extension tool treatment](tickets/tool-003-mcp-treatment.md) — Keyword-gate: excluded from semantic matching, only enter via FTS5 keyword match. Cap 3 MCP tools.

## Frontier (open tickets)

- [tool-004 — Validation strategy](tickets/tool-004-validation.md) — Live VPS test: deploy → test session via dashboard → observe traces → clean up → report.

## Not yet specified

_All fog graduated to tickets. Frontier is clear._

## Out of scope

- Shortening MCP tool descriptions (owned by external servers)
- Changing the OpenAI-compatible API format (provider overhead)
- Reducing chat history size (separate concern — session.go `MaxHistoryTurns` already set to 5)
