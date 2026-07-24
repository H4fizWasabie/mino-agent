# Mino — Handoff

> Mino → Go port complete. Dashboard works. Memory works. Ready for DECISIONS.md iterations.

## What was built

Mino is a Go rewrite of Mino (ShenSeanChen's personal AI agent), with architecture decisions from Mino Personal and Mino.

```
mino/                          ~1,850 lines Go, ~7.5MB binary
  main.go                      CLI entry
  config.go                    Settings (env vars)
  db.go                        SQLite + FTS5 schema (15 tables matching the reference)
  provider.go                  OpenAI client + SSE streaming (MiMo v2.5)
  loop.go                      THE loop (Mino's exact pattern)
  session.go                   SOUL.md, system prompt, session management
  memory.go                    Retrieval gate, consolidation, recall tool
  tools.go                     8 tools: read/write/edit/bash + calendar/notes/messages/search + recall
  telegram.go                  Telegram bot (polling, HTML formatting)
  dashboard.go                 HTTP server serving Mino's exact static files + API
  static/                      Mino's exact index.html, style.css, app.js
  AGENTS.md                    Rules for AI coding agents
  DECISIONS.md                 14-section architecture document
  CHANGELOG.md                 Version history
```

## How to run

### Quick start (env vars)

```bash
# CLI mode
MINO_API_KEY="sk-..." MINO_MODEL="your-model" ./mino

# Dashboard (http://localhost:7779)
MINO_API_KEY="..." MINO_BASE_URL="..." MINO_MODEL="your-model" MINO_DASHBOARD_PORT=7779 ./mino dashboard

# Telegram
TELEGRAM_BOT_TOKEN="..." MINO_API_KEY="..." MINO_BASE_URL="..." ./mino

# Build
CGO_ENABLED=1 go build -tags "sqlite_fts5" -ldflags="-s -w" -o mino .
```

### Multi-provider (`~/.mino/providers.json`)

Drop a `providers.json` in `~/.mino/` to use multiple models with priority, fallback, and sticky routing.

**Ollama (local, no API key):**

```json
{
  "providers": [
    {
      "name": "ollama",
      "priority": 1,
      "base_url": "http://localhost:11434/v1",
      "api_key_env": "",
      "model": "llama3.1:8b",
      "small_model": "llama3.1:8b"
    }
  ]
}
```

**OpenAI + Anthropic (API keys):**

```json
{
  "providers": [
    {
      "name": "openai",
      "priority": 1,
      "base_url": "https://api.openai.com/v1",
      "api_key_env": "OPENAI_API_KEY",
      "model": "gpt-4o"
    },
    {
      "name": "anthropic",
      "priority": 2,
      "base_url": "https://api.anthropic.com",
      "api_key_env": "ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-20250514"
    }
  ]
}
```

**OpenRouter (one key, all models):**

```json
{
  "providers": [
    {
      "name": "openrouter",
      "priority": 1,
      "base_url": "https://openrouter.ai/api/v1",
      "api_key_env": "OPENROUTER_API_KEY",
      "model": "anthropic/claude-sonnet-4"
    }
  ]
}
```

Set `api_key_env` to `""` for providers that don't need auth (Ollama, LM Studio).

## Current status

### ✅ Complete
- Agent loop (observe → reason → act → repeat) + tool dedup cache
- 12 tools: read_file, write_file, edit_file, bash, create_event, list_events, save_note, send_message, recall, add_working_memory, add_pattern, search_web
- SOUL.md (editable persona, MiMo-tuned tool discipline + stop conditions)
- SQLite + FTS5 (15 tables, full-text search)
- Intent classifier: regex-based, gates retrieval (GREETING/CHAT skip memory)
- Retrieval gate: intent-driven + pull-based recall() tool
- Consolidation: distill chat → facts every N exchanges
- Extension protocol: discover + proxy external HTTP tools
- Scheduler: HH:MM daily + interval schedules via schedule.json
- Checkpoint/resume: task snapshots survive restarts
- Working memory: append-only with sections
- Patterns: "When X, do Y" compressed rules
- OpenRouter embeddings: text-embedding-3-large semantic search
- Telegram gateway (long-polling, HTML formatting)
- Dashboard (Mino's exact UI: chat dock, architecture SVG, Memory/Settings/Database tabs)
- All dashboard tabs working: Overview, Memory (view/edit/delete), Settings (SOUL.md live edit), Database (SQL console)
- SSE streaming (token-by-token chat + architecture animation)

### ⚠️ Known issues
- MiMo v2.5 sometimes ignores tool discipline — mitigated by dedup cache + [already executed] signal
- Consolidation runs in goroutine (async, no feedback on failure)
- Schedule format is simplified HH:MM + "every Nm" (no full cron expressions)

## Key decisions made during build

1. **recall() tool instead of passive injection** — MiMo ignores system prompt memory context, so we switched to Mino's pull-based pattern. The model calls `recall("what do I know about the user")` when needed. Works reliably.

2. **Mino's exact dashboard** — Copied Mino's static files (index.html, style.css, app.js) unchanged. Mino's Go server implements the API endpoints Mino's JS expects. Every JS error from stub endpoints was traced and fixed.

3. **SOUL.md is the system prompt** — Editable file at `~/.mino/SOUL.md`. Changes take effect next turn. Settings tab edits it live.

4. **FTS5 with triggers** — Schema matches Mino exactly: facts_fts, episodes_fts with INSERT/UPDATE/DELETE triggers. Initial multi-statement schema failed because Go's db.Exec doesn't split on `;` — fixed by storing each statement in a string slice.

## Natural next steps (in priority order)

1. ~~Fix DB path~~ ✅ — Uses $HOME/.mino
2. ~~Add prompt tuning~~ ✅ — SOUL.md with tool discipline + stop conditions
3. ~~Port Mino's scheduler~~ ✅ — HH:MM + interval schedules
4. ~~Add checkpoint/resume~~ ✅ — Task snapshots survive restarts
5. ~~Working memory + patterns~~ ✅ — append-only files + embedding search
6. ~~Build procurement extension~~ ✅ — Python HTTP service, wraps Mino's procurement module
7. ~~Build web search extension~~ ✅ — Tavily search + standard-library page fetch
8. **Deploy to VPS** — scp binary, systemd service files

## Useful commands

```bash
# Quick test
cd ~/mino && echo "say hi" | MINO_API_KEY="..." MINO_BASE_URL="..." MINO_MODEL="your-model" timeout 20 ./mino 2>/dev/null

# Check DB
python3 -c "import sqlite3; c=sqlite3.connect('.mino/state.db'); print([r[0] for r in c.execute('SELECT name FROM sqlite_master WHERE type=\"table\"').fetchall()])"

# Check dashboard API
curl -s http://localhost:7779/api/data | python3 -m json.tool
curl -s http://localhost:7779/api/memory | python3 -m json.tool
```
