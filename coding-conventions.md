# Mino — Coding Conventions

> How Mino code is written and structured. Every line should trace to a request
> and follow these conventions. See `rules.md` for process rules.

## Simplicity (the prime directive)

- **Less code = less bugs.** Prefer one-liners. If you can solve it in 10 lines, don't write 50.
- **Fewest files possible.** New files are a last resort. Group related logic.
- **Flat over nested.** Minop directory trees hide bugs. Keep it visible.
- **Read Mino's source first.** If Mino did it in 50 lines, Mino should too.

## Project structure (core first, extensions last)

```
mino/
  main.go          # entry point, wire everything
  go.mod           # module: github.com/H4fizWasabie/mino-agent
  config.go         # env vars → Settings
  db.go             # SQLite schema and connection
  loop.go           # canonical reasoning and tool runtime
  memory.go         # SQLite operational state + semantic graph bridge
  tools.go          # tool registry + built-in tools
  session.go        # session, history, context assembly
  provider.go       # LLM protocol adapters
  provider_manager.go # provider priority, fallback, and reasoning settings
  playbook.go       # optional filesystem state machines
  telegram.go       # primary gateway
  dashboard.go      # secondary web UI + SSE
  extensions.go     # external HTTP tools
```
**Core path:** `app.go` assembles context, `loop.go` runs reasoning and tools,
and `session.go` records the result. Playbooks may be selected by that runtime;
they never replace it with a second agent loop. Extensions remain separate
services managed by systemd.

## Code quality

- **Go stdlib first.** No external dependency without explicit discussion.
- **Readable in an afternoon.** The entire codebase should be understandable in one sitting.
- **~100 lines per file target** for core modules (loop, tools, memory). Split new growth where practical; the current GraphMemory migration is a deliberate transitional exception and must remain covered by behavior tests.
- **No frameworks.** Stdlib HTTP, stdlib SQL, stdlib templates. No gin, no echo, no gorm.
- **Single binary.** Everything embedded via `embed.FS`. One `go build`, one deploy.

## Code patterns

- Flat project structure (see above). No `cmd/`, `internal/`, `pkg/` — that's premature layering.
- Error handling: explicit, never panic in library code
- Logging: `log/slog` (structured, levels)
- Config: environment variables + `~/.mino/config.json`
- SQLite: `modernc.org/sqlite` with WAL mode
