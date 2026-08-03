# Mino — personal AI agent

One binary. One SQLite file. Your own AI assistant.

[![DeepWiki](https://img.shields.io/badge/DeepWiki-Architecture%20Docs-blue)](https://deepwiki.com/H4fizWasabie/mino-agent)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/H4fizWasabie/mino-agent)
![Version](https://img.shields.io/badge/version-v2.1.0-blue)
![License](https://img.shields.io/badge/license-MIT-green)

- **Dashboard** — chat, memory, tools, database, ops
- **Telegram** — same agent, any device
- **Playbooks** — autonomous repeatable workflows: teach once, compile from evidence, schedule daily, verified outputs, Telegram reports
- **Coding tools** — read, write, edit, grep, glob, git, graphify, codegraph, bash
- **Memory** — Markdown-authoritative graph memory with self-maintenance (edge judgment, community detection, 6-hourly maintenance), SQLite operational state, auto-consolidation, one-time reminders
- **Multi-provider** — priority, fallback, circuit breaking, and OpenRouter provider routing (including Exacto tool-call routing)
- **OAuth login** — Claude, Codex (ChatGPT), GitHub Copilot, xAI/Grok
- **Web search** — Tavily API (free tier available)
- **Guardrails** — prefers specialized tools over bash, workspace boundary enforcement
- **MCP + Extensions** — plug in external tools via HTTP or stdio

📋 **[DECISIONS.md](DECISIONS.md)** — architecture decisions, philosophy, and what NOT to build.
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
| `mino version` | Show version |
| `mino update` | Self-update from GitHub releases |

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
| `MINO_MAX_TOKENS` | `2048` | Max output tokens per call |
| `MINO_CONTEXT_CHARS` | `100000` | Context window budget |
| `TELEGRAM_BOT_TOKEN` | — | Optional Telegram bot token |
| `TAVILY_API_KEY` | — | Web search (free key at tavily.com) |
| `MINO_OPENROUTER_KEY` | — | OpenRouter API key for providers and embeddings |

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

For OpenRouter, use the OpenRouter base URL and model slug. `:exacto` asks
OpenRouter to prefer providers with stronger tool-calling reliability; the
optional `provider_routing` list can pin a discounted provider route.

```json
{
  "providers": [
    {
      "name": "openrouter",
      "priority": 1,
      "base_url": "https://openrouter.ai/api/v1",
      "api_key_env": "MINO_OPENROUTER_KEY",
      "model": "xiaomi/mimo-v2.5:exacto",
      "small_model": "deepseek/deepseek-v4-flash",
      "provider_routing": ["GMICloud"]
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

## Architecture

```
mino (single Go binary)

main.go              — entry point, CLI routing
app.go               — Core struct, Respond, session wiring
loop.go              — agent loop: observe → reason → act → repeat
session.go           — SOUL.md, system prompt, context assembly
memory.go            — operational SQLite state and consolidation bridge
graph_memory.go      — Markdown-authoritative semantic graph memory
tools.go             — built-in tools (file, bash, calendar, notes, search, image)
coding_tools.go      — read, write, edit, grep, glob, git, graphify, codegraph
provider.go          — OpenAI + Anthropic + Codex clients, SSE streaming
provider_manager.go  — priority, retry, fallback, circuit breaking
oauth.go             — PKCE + device-code OAuth, embedded provider configs
dashboard.go         — web UI + REST API + SSE streaming
telegram.go          — Telegram bot gateway
mcp.go               — MCP bridge (stdio + HTTP servers, flat companions for nested-executor tools)
skill.go             — skill loader (SKILL.md files)
extensions.go        — HTTP extension protocol
playbook.go          — playbook management, capture_playbook (teach → compile), scheduling
playbook_workspace.go— playbook run engine: durable runs, write-attributed verification, retry policy
memory.go            — operational SQLite state and consolidation bridge
graph_memory.go      — Markdown-authoritative semantic graph memory with self-maintenance (edge judgment, communities, 6-hourly maintenance)
adapters.go          — working memory, patterns, embeddings
reminder.go          — persistent one-time reminders with Telegram delivery
eval.go              — evaluation harness for automated correctness
db.go                — SQLite schema and migrations
config.go            — environment variable → Settings
update.go            — self-update from GitHub releases
```

Playbooks are filesystem workspaces with `stages/NN-name/CONTEXT.md` contracts
(`Inputs`, `Process`, `Tools`, `Outputs`). The recommended way to author one is
**capture_playbook**: run a task once, then compile it into a playbook from the
audit evidence — real tool calls, real outputs, nothing improvised. Each run
gets an isolated workspace with verified outputs: a stage only passes when its
declared outputs were written by that stage's own tool calls. Read-only stages
retry on failure; destructive stages fail loud and never double-execute.
Playbooks are autonomous-only — if a task needs the owner mid-way, it is a
conversation, not a playbook. See [PLAYBOOKS_DESIGN.md](PLAYBOOKS_DESIGN.md).

## Free AI stack

Mino can run entirely on free tiers:

- **LLM**: Any free model via OpenRouter or OpenCode Zen
- **Image gen**: Cloudflare Workers AI (free tier, ~10k images/day) with [Pollinations.ai](https://pollinations.ai) fallback (free, no key)
- **Embeddings**: via OpenRouter (free tier available)
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

## License

MIT

---

Built with [Mino](https://github.com/H4fizWasabie/mino-agent) — the same agent that wrote parts of this README.
