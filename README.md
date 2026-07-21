# Mino — personal AI agent

One binary. One SQLite file. Your own AI assistant.

[![DeepWiki](https://img.shields.io/badge/DeepWiki-Architecture%20Docs-blue)](https://deepwiki.com/H4fizWasabie/mino-agent)
![Version](https://img.shields.io/badge/version-v1.0.0-blue)
![License](https://img.shields.io/badge/license-MIT-green)

- **Dashboard** — chat, memory, tools, database, ops
- **Telegram** — same agent, any device
- **10 coding tools** — list_files, grep, glob, git, graphify, codegraph, read/write/edit, bash
- **OAuth login** — Claude, Codex (ChatGPT), GitHub Copilot, xAI/Grok
- **Web search** — Tavily API (free tier available)
- **Memory** — SQLite + FTS5 semantic search, auto-consolidation
- **Guardrails** — prefers specialized tools over bash, verifies file claims before completion
- **MCP + Extensions** — plug in external tools via HTTP or stdio

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
| `MINO_MODEL` | `deepseek-v4-flash-free` | Main model |
| `MINO_SMALL_MODEL` | `deepseek-v4-flash-free` | Model for background tasks |
| `MINO_DASHBOARD_PORT` | `7779` | Dashboard port |
| `MINO_DASHBOARD_HOST` | (all interfaces) | Bind address |
| `MINO_MAX_ITERATIONS` | `25` | Max tool calls per turn |
| `MINO_MAX_TOKENS` | `2048` | Max output tokens per call |
| `MINO_CONTEXT_CHARS` | `100000` | Context window budget |
| `TELEGRAM_BOT_TOKEN` | — | Optional Telegram bot token |
| `TAVILY_API_KEY` | — | Web search (free key at tavily.com) |
| `MINO_OPENROUTER_KEY` | — | Embeddings (free tier available) |

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
mino (~2500 lines of Go)

main.go              — entry point, CLI routing
loop.go              — agent loop: observe → reason → act → repeat
session.go           — SOUL.md, system prompt, context assembly
memory.go            — SQLite + FTS5 retrieval, consolidation
tools.go             — built-in tools (file, bash, calendar, notes, search, image)
provider.go          — OpenAI + Anthropic + Codex clients, SSE streaming
provider_manager.go  — priority, retry, fallback, circuit breaking
oauth.go             — PKCE + device-code OAuth, embedded provider configs
dashboard.go         — web UI + REST API
telegram.go          — Telegram bot gateway
mcp.go               — MCP bridge (stdio-based servers)
skill.go             — skill loader (SKILL.md files)
extensions.go        — HTTP extension protocol
checkpoint.go        — task survival across restarts
scheduler.go         — cron engine for proactive tasks
artifacts.go         — large output management
adapters.go          — working memory, patterns, embeddings
```

## Free AI stack

Mino can run entirely on free tiers:

- **LLM**: Any free model via OpenRouter or OpenCode Zen
- **Image gen**: [Pollinations.ai](https://pollinations.ai) (free, no key)
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
