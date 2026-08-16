# Mino — personal AI agent

One binary. One SQLite file. Your own AI assistant.

**Liking Mino? [Star it on GitHub ⭐](https://github.com/H4fizWasabie/mino-agent/stargazers) — it takes one click and keeps this project alive.**

[![GitHub stars](https://img.shields.io/github/stars/H4fizWasabie/mino-agent?style=social)](https://github.com/H4fizWasabie/mino-agent/stargazers)
[![DeepWiki](https://img.shields.io/badge/DeepWiki-Architecture%20Docs-blue)](https://deepwiki.com/H4fizWasabie/mino-agent)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/H4fizWasabie/mino-agent)
![Version](https://img.shields.io/badge/version-v2.9.0-blue)
![License](https://img.shields.io/badge/license-MIT-green)

- **Dashboard** — chat, memory, tools, database, ops
- **Telegram** — same agent, any device
- **Playbooks** — autonomous repeatable workflows: teach once, compile from evidence, schedule daily, verified outputs, Telegram reports
- **Coding tools** — read, write, edit, grep, glob, git, graphify, codegraph, bash
- **Memory** — Markdown-authoritative graph memory (facts as frontmatter `.md` files) with self-maintenance (edge judgment, community detection, 6-hourly maintenance), episodic/semantic split (episodes expire to archive after 30 days; semantic facts are protected), config-whitelisted semantic distillation, FTS5 retrieval, auto-consolidation, one-time reminders
- **Multi-provider** — priority, fallback, circuit breaking, and OpenRouter routing (model `:provider` pins + privacy-safe `provider_routing`/`allow_fallbacks`)
- **OAuth login** — Claude, Codex (ChatGPT), GitHub Copilot, xAI/Grok
- **Web search** — Tavily API (free tier available)
- **Guardrails** — prefers specialized tools over bash, workspace boundary enforcement
- **MCP + Extensions** — plug in external tools via HTTP or stdio

📋 **[docs/decisions.md](docs/decisions.md)** — architecture decisions, philosophy, and what NOT to build.
📋 **[docs/config.md](docs/config.md)** — every config file, who writes/reads it, the provider↔files mapping.
📋 **[CHANGELOG.md](CHANGELOG.md)** — release history.

## Quickstart

**No Go, no dependencies — just download and run:**

```bash
# Linux (x86-64)
curl -L https://github.com/H4fizWasabie/mino-agent/releases/latest/download/mino-linux-amd64 -o mino
chmod +x mino
./mino

# macOS (Apple Silicon)
curl -L https://github.com/H4fizWasabie/mino-agent/releases/latest/download/mino-darwin-arm64 -o mino
chmod +x mino
./mino

# macOS (Intel)
curl -L https://github.com/H4fizWasabie/mino-agent/releases/latest/download/mino-darwin-amd64 -o mino
chmod +x mino
./mino
```

Windows: download `mino-windows-amd64.exe` from the [releases page](https://github.com/H4fizWasabie/mino-agent/releases/latest), rename to `mino.exe`, run in terminal.

**Or build from source (needs Go 1.25+):**

```bash
git clone https://github.com/H4fizWasabie/mino-agent.git
cd mino-agent
go build -o mino .
./mino
```

Browser opens → fill one form → done. No build tags, no CGo, no system dependencies.

**Want it available everywhere?**

```bash
sudo cp mino /usr/local/bin/
mino
```

## Commands

| Command | What |
|---------|------|
| `mino` | Launch dashboard (default) |
| `mino cli` | Terminal chat |
| `mino remember "query"` | Print graph-memory recall (same output as the in-loop `remember` tool, no LLM call) — CTX-022 A |
| `mino memory path <from> <to>` | Shortest path between two memory facts |
| `mino version` | Show version |
| `mino update` | Self-update from GitHub releases |
| `mino setup-privileges` | Write the sudoers command whitelist (RUN-003) — run as root |

## Configuration

| Env | Default | Description |
|-----|---------|-------------|
| `MINO_HOME` | `~/.mino` | State directory (DB, config, traces) |
| `MINO_API_KEY` | — | OpenAI-compatible API key |
| `MINO_BASE_URL` | — | API base URL |
| `MINO_MODEL` | `mimo-v2.5` | Main model |
| `MINO_SMALL_MODEL` | `mimo-v2.5` | Model for background tasks |
| `MINO_DASHBOARD_PORT` | `7779` | Dashboard port |
| `MINO_DASHBOARD_HOST` | (all interfaces) | Bind address |
| `MINO_MAX_ITERATIONS` | `25` | Max tool calls per turn |
| `MINO_MAX_TOKENS` | `16384` | Max output tokens per call |
| `MINO_CONTEXT_CHARS` | `100000` | Context window budget |
| `TELEGRAM_BOT_TOKEN` | — | Optional Telegram bot token |
| `TAVILY_API_KEY` | — | Web search (free key at tavily.com) |
| `MINO_OPENROUTER_KEY` | — | OpenRouter API key |

See `.env.example` for a copy-paste template.

## Providers

Mino works with any OpenAI-compatible API. Configure via the dashboard or `~/.mino/providers.json`:

### API key

```json
{
  "providers": [
    {
      "name": "my-provider",
      "priority": 1,
      "base_url": "https://api.openai.com/v1",
      "model": "gpt-4o-mini",
      "small_model": "gpt-4o-mini"
    }
  ]
}
```

Set the key in the dashboard onboarding form, or write to `~/.mino/auth.json`:

```json
{
  "my-provider": {
    "type": "api_key",
    "key": "sk-..."
  }
}
```

For OpenRouter, use the OpenRouter base URL and model slug. The optional
`:provider` suffix pins a discount provider route (e.g.
`deepseek/deepseek-v4-flash-0731:deepinfra`); the `provider_routing` list
forces the same set. By default Mino never falls back outside that list
(`allow_fallbacks: true` opts into arbitrary hosts — keep it off for
privacy-safe routing).

Optional `transport` field declares the wire family explicitly — `openai`
(default), `anthropic`, or `codex` — it is never guessed from the URL. The
Codex OAuth login writes it automatically.

```json
{
  "providers": [
    {
      "name": "openrouter",
      "priority": 1,
      "base_url": "https://openrouter.ai/api/v1",
      "api_key_env": "MINO_OPENROUTER_KEY",
      "model": "deepseek/deepseek-v4-flash-0731:deepinfra",
      "small_model": "deepseek/deepseek-v4-flash-0731:deepinfra",
      "provider_routing": ["DeepInfra"],
      "small_provider_routing": ["DeepInfra"]
    }
  ]
}
```

### OAuth (no API key needed)

Mino ships with OAuth configs for Claude, Codex (ChatGPT), GitHub Copilot, and xAI/Grok. Login from the Settings page — no API key required.

### Local LLMs (Ollama, LM Studio, vLLM)

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

Empty `api_key_env` means no auth. Works with any OpenAI-compatible local server.

## Recommended installs

Mino runs with none of these, but a few helper binaries make it noticeably better:

| Tool | What it does | Install |
|------|--------------|---------|
| **rtk** (Token Killer) | The `bash` tool rewrites noisy commands (`ls`, `cat`, `git log`…) through RTK to shrink tool output — fewer tokens, cheaper turns | Debian/Ubuntu: `curl -fsSL https://github.com/rtk-ai/rtk/releases/download/v0.43.0/rtk_0.43.0-1_amd64.deb -o rtk.deb && sudo dpkg -i rtk.deb` — other platforms from the [releases page](https://github.com/rtk-ai/rtk/releases) |
| **markitdown** | URL fetch pipes HTML through markitdown for clean Markdown (tables, headings, links) instead of stripped plain text; the fileingest extension uses it too | `pip install markitdown` (make sure `markitdown` is on `PATH`) |
| **composio** | Hosted MCP gateway: Gmail, Google Drive, Slack and many more services become `MCP_composio_*` tools — no local install, just a key | Get a key at composio.dev, then drop the config below into `~/.mino/mcp.d/` |

```json
{"name": "composio", "url": "https://connect.composio.dev/mcp", "headers": {"Authorization": "Bearer ck_..."}}
```

All three degrade gracefully — Mino works without them, but noisy commands and web pages eat more tokens.

### Host privileges (optional)

To let Mino manage host state itself (install packages, write systemd units, restart its own
service — the `install_package` / `write_unit` / `restart_service` tools), install the sudoers
command whitelist once, as root: `sudo MINO_HOME=/home/mino/.mino mino setup-privileges`.
This grants the mino user exactly six command shapes as root (apt-get install/remove -y,
systemctl restart/daemon-reload, `install -o root -g root -m 0644` from `~/.mino/tmp` to
/etc/systemd/system, `rm -f /etc/systemd/system/*`) — never a shell, never ALL. Without it the
tools refuse every op with a clear boundary message, and `bash` refuses `sudo` outright.

## A task failed — now what?

| Symptom | Cause | Fix |
|---------|-------|-----|
| `(stopped after 25 iterations)` | The turn ran out of tool-call budget mid-task | Raise `MINO_MAX_ITERATIONS` (see below), or split the request into smaller steps and send `continue` |
| Reply cuts off mid-sentence, or Mino repeats itself | `MINO_MAX_TOKENS` too small for the output | Raise `MINO_MAX_TOKENS` (16384 works well) |
| Mino forgets earlier context in long chats | `MINO_MAX_HISTORY_TURNS` too low | Raise it — 10 is a good ceiling |
| Tool errors, wrong results, or a tool that exists but is never used | Missing helper binary, or the model needs a clearer instruction | Install rtk/markitdown above; check the dashboard traces page or `MINO_HOME/traces/` and `MINO_HOME/audit.jsonl`, then retry with a more specific instruction |
| Playbook run failed | A stage didn't write its declared outputs, or hit the stage iteration cap | Look in `MINO_HOME/playbooks/<name>/runs/<timestamp>/stages/` for what the failing stage actually wrote, then ask Mino to "check the playbook run" |

When in doubt: retry with a clearer instruction, check the provider key still works, and check `mino update` for a newer build.

## Power tuning

The defaults are deliberately conservative. If tasks keep hitting limits, raise these in your environment. The reference VPS deployment runs `MINO_MAX_ITERATIONS=30`, `MINO_CONTEXT_CHARS=1000000`, and `MINO_MAX_HISTORY_TURNS=10`.

| Env | Default | What it limits | When to raise |
|-----|---------|----------------|---------------|
| `MINO_MAX_ITERATIONS` | 25 | Tool calls per turn — the `(stopped after N iterations)` wall | Long multi-step tasks end with the iteration message |
| `MINO_MAX_TOKENS` | 16384 | Output tokens per model call | Replies truncate or the model stalls |
| `MINO_CONTEXT_CHARS` | 100000 | Context window budget for history + tool output | Long sessions or playbooks lose early context |
| `MINO_MAX_HISTORY_TURNS` | 5 | Conversation turns kept in context | Mino forgets earlier turns in long chats |
| `MINO_BASH_TIMEOUT` | 2m | `bash` tool runs | Slow commands time out |
| `MINO_CODING_TIMEOUT` | 2m | Coding tool runs | Big repos time out |
| `MINO_SYNC_TIMEOUT` | 5m | File syncs | Large file transfers time out |

Playbook stages have their own iteration cap (50) — if a playbook run burns through it, the fix is usually in the playbook's stage contract, not the env.

## Architecture

```
mino (single Go binary)

main.go              — entry point, CLI routing
app.go               — Core struct, Respond, session wiring
loop.go              — agent loop: observe → reason → act → repeat; interruption, snapshots
session.go           — SOUL.md, system prompt, context assembly
memory.go            — consolidation bridge and operational state
graph_memory.go      — Markdown-authoritative graph memory: facts, edges, judgment, communities, migration
nerves.go            — loop snapshots, interrupts, mid-flight signals
cost.go              — run/month spend from usage.jsonl, review triggers
tools.go             — tool registry (file, bash, calendar, notes, search, image) + schema caps
coding_tools.go      — read, write, edit, grep, glob, git, graphify, codegraph
provider.go          — OpenAI + Anthropic + Codex clients, SSE streaming
provider_manager.go  — priority, retry, fallback, circuit breaking, routing pins
codex.go             — ChatGPT-subscription (Codex) transport
oauth.go             — PKCE + device-code OAuth, embedded provider configs
dashboard.go         — web UI + REST API + SSE streaming
dashboard_universe.go— universe graph topology (Living Field)
telegram.go          — Telegram bot gateway (threaded section-split delivery)
mcp.go               — MCP bridge (stdio + HTTP servers)
skill.go             — skill loader (SKILL.md files)
extensions.go        — HTTP extension protocol
playbook.go          — playbook management, capture_playbook (teach → compile), scheduling
playbook_workspace.go— playbook run engine: durable runs, write-attributed verification, retry policy
post_mortem.go       — failure-evidence extraction for failed runs (CTX-017)
audit_playbook.go    — design-time contract risk-flags (CTX-018)
adapters.go          — working memory and patterns
reminder.go          — persistent one-time reminders with Telegram delivery
db.go                — SQLite schema and migrations (FTS5, schema v7+)
config.go            — environment variable → Settings
update.go            — self-update from GitHub releases (the only production deploy path; see docs/emergency-deploy.md for the manual lane)
deploy.sh            — VPS bootstrap only (user, oauth, units, DB backup); never pushes binaries
```

Playbooks are filesystem workspaces with `stages/NN-name/CONTEXT.md` contracts
(`Inputs`, `Process`, `Tools`, `Outputs`). The recommended way to author one is
**capture_playbook**: run a task once, then compile it into a playbook from the
audit evidence — real tool calls, real outputs, nothing improvised. Each run
gets an isolated workspace with verified outputs: a stage only passes when its
declared outputs were written by that stage's own tool calls. Read-only stages
retry on failure; destructive stages fail loud and never double-execute.
Playbooks are autonomous-only — if a task needs the owner mid-way, it is a
conversation, not a playbook. See [docs/playbooks-design.md](docs/playbooks-design.md).

## Free AI stack

Mino can run entirely on free tiers:

- **LLM**: Any free model via OpenRouter or OpenCode Zen
- **Image gen**: Cloudflare Workers AI (free tier, ~10k images/day) with [Pollinations.ai](https://pollinations.ai) fallback (free, no key)
- **Web search**: Tavily (free tier: 1000 searches/month)
- **URL fetch**: pipes HTML through readability extraction

## Extensions

External tools connect via HTTP. Create `~/.mino/extensions.json`:

```json
[
  {"name": "my-tool", "url": "http://localhost:9100"}
]
```

Mino discovers tools via `GET /tools` and proxies calls via `POST /execute`.

### cost-watch — the price guardian

Mino watches its own wallet. Fetches the OpenRouter endpoints price catalogue
hourly (model-agnostic — derived from your own `providers.json`, persisted to
`cost-catalogue.json`), and a `--check` timer pages Telegram when a
promotional price expires. **Alert-only by policy (REL-01): it pages the
owner — it never changes the brain.** Ask mino *"how much am I paying?"* and
it answers with live prices via `system_check`'s cost block or
`cost_watch_status` / `cost_watch_check` / `cost_watch_refresh`.
Install: see `extensions/cost-watch/README.md`.

### minowrap — universal tool adapter

Add any CLI command as a tool in one JSON line:

```json
[
  {"name": "disk_usage", "description": "Show disk usage for a path", "run": "df -h {path}"},
  {"name": "deploy", "description": "Deploy the app", "run": "curl -X POST https://api.example.com/deploy"}
]
```

Template args like `{path}` auto-generate JSON Schema. Mino discovers them on next `reload_plugins` call.

## MCP servers

Drop JSON configs in `~/.mino/mcp.d/`:

```json
{"name": "fs", "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/dir"]}
```

Tools are prefixed as `MCP_<server>_<tool>`.

### mino-memory — expose the graph to any agent (CTX-022 A)

Build from `extensions/mino-memory` and register:

```json
{"name": "mino-memory", "command": "/path/to/mino-memory"}
```

Exposes `memory_remember` (identical output to the in-loop `remember` tool:
intent-ranking, BFS expansion, provenance and conflict flags) and
`memory_path`. Read-only bridge to the dashboard's `/api/memory/remember`,
localhost-bound — any external agent gets the same retrieval quality as mino
itself, no memory-file scraping.

## License

MIT

---

Built with [Mino](https://github.com/H4fizWasabie/mino-agent) — the same agent that wrote parts of this README.
