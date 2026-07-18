# Mino — personal AI agent

One binary. One SQLite file. Your own AI assistant.

- **Dashboard** — chat, memory, tools, file browser
- **Telegram** — chat on the go
- **Tools** — file ops, calendar, notes, web search, image generation, recall
- **Extensions** — plug in external tools via HTTP
- **MCP** — Model Context Protocol servers (filesystem, databases, etc.)
- **Skills** — save repeatable workflows as markdown files

## Quickstart

```bash
# Build (requires Go 1.25+, SQLite with FTS5)
go build -tags sqlite_fts5

# Set your API key
export MINO_API_KEY=sk-...

# Run (dashboard on :7777)
./mino

# Or with Telegram
export TELEGRAM_BOT_TOKEN=...
./mino
```

Open `http://localhost:7777` — fill in your provider details, done.

## Configuration

| Env | Default | Description |
|-----|---------|-------------|
| `MINO_HOME` | `~/.mino` | State directory (DB, config, traces) |
| `MINO_API_KEY` | — | OpenAI-compatible API key |
| `MINO_BASE_URL` | `https://api.openai.com/v1` | API base URL |
| `MINO_MODEL` | `gpt-4o-mini` | Main model |
| `MINO_SMALL_MODEL` | `gpt-4o-mini` | Model for background tasks |
| `MINO_DASHBOARD_PORT` | `7777` | Dashboard port |
| `MINO_DASHBOARD_HOST` | (all interfaces) | Bind address (e.g. `127.0.0.1` or Tailscale IP) |
| `MINO_MAX_ITERATIONS` | `10` | Max tool calls per turn |
| `MINO_MAX_TOKENS` | `4096` | Max output tokens |
| `MINO_CONTEXT_CHARS` | `100000` | Context window budget in chars |
| `TELEGRAM_BOT_TOKEN` | — | Optional Telegram bot token |
| `HF_TOKEN` | — | HuggingFace token (for FLUX.1-schnell, optional) |
| `TAVILY_API_KEY` | — | Tavily web search API key |
| `MINO_OPENROUTER_KEY` | — | OpenRouter key (for embeddings, fallback search) |

See `.env.example` for a copy-paste template.

## Architecture

```
Mino is ~2000 lines of Go:

main.go          — entry point, wires everything
loop.go          — agent loop: reason → act → observe
session.go       — SOUL.md, system prompt, context assembly
memory.go        — SQLite + FTS5 retrieval, consolidation
tools.go         — built-in tools (file, calendar, notes, search, image gen)
provider.go      — OpenAI-compatible client + SSE streaming
provider_manager.go — priority, retry, fallback, circuit breaking
telegram.go      — Telegram bot gateway
dashboard.go     — web UI + REST API
mcp.go           — MCP bridge (stdio-based servers)
skill.go         — skill loader (SKILL.md files)
extensions.go    — HTTP extension protocol
checkpoint.go    — task survival across restarts
scheduler.go     — cron engine for proactive tasks
artifacts.go     — large output management
```

## Free AI stack

Mino can run entirely on free tiers:

- **LLM**: [Google Gemma 4](https://openrouter.ai/google/gemma-4-31b-it) (free on OpenRouter, no cost)
- **Image gen**: [Pollinations.ai](https://pollinations.ai) (free, no key)
- **Embeddings**: via OpenRouter (free tier available)
- **Web search**: Tavily (free tier, 1,000 req/month)

## Extensions

External tools connect via HTTP. Create `~/.mino/extensions.json`:

```json
[
  {"name": "my-tool", "url": "http://localhost:9100"}
]
```

Mino discovers tools via `GET /tools` and proxies calls via `POST /execute`. See the [extension protocol](extensions.go) for details.

## MCP servers

Drop JSON configs in `~/.mino/mcp.d/`:

```json
{"name": "fs", "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/dir"]}
```

Tools are prefixed as `MCP_<server>_<tool>`.

## License

MIT
