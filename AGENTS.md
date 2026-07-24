# Mino — Agent Rules

> Every AI coding agent working on this project MUST follow these rules.
> Violations = rejected PR. No exceptions.

## First Steps (mandatory)

1. **Check `CHANGELOG.md`** to understand recent changes and patterns.
2. **Understand the philosophy**: Mino is a Go rewrite synthesizing Mino (architecture), Mino (capabilities), and Mino (context). Keep it simple. If Mino doesn't have it, question why Mino needs it.

## Rules

### Simplicity (the prime directive)
- **Less code = less bugs.** Prefer one-liners. If you can solve it in 10 lines, don't write 50.
- **Fewest files possible.** New files are a last resort. Group related logic.
- **Flat over nested.** Minop directory trees hide bugs. Keep it visible.
- **Read Mino's source first.** If Mino did it in 50 lines, Mino should too.

### Project structure (core first, extensions last)
```
mino/
  main.go          # entry point, wire everything
  go.mod           # module: github.com/H4fizWasabie/mino-agent
  config.go         # env vars → Config struct
  loop.go           # THE loop (~100 lines)
  memory.go         # SQLite + FTS5 + consolidation
  tools.go          # tool registry + built-in tools
  session.go        # session, history, context assembly
  provider.go       # LLM provider manager + adapters
  telegram.go       # telegram gateway (phase 1)
  dashboard.go      # web UI + SSE (phase 2)
  scheduler.go      # cron engine (phase 2)
  checkpoint.go     # task survival (phase 2)
  memory/           # adapters, working memory, patterns (phase 3+)
```
**Build order:** Phase 1 = `main.go` + `loop.go` + `session.go` + `tools.go` + `config.go` + `provider.go`.
Everything else comes after the core loop works. Extensions are separate repos/services — never embedded.

### Code quality
- **Go stdlib first.** No external dependency without explicit discussion.
- **Readable in an afternoon.** The entire codebase should be understandable in one sitting.
- **~100 lines per file max** for core modules (loop, tools, memory). If it's growing, split it.
- **No frameworks.** Stdlib HTTP, stdlib SQL, stdlib templates. No gin, no echo, no gorm.
- **Single binary.** Everything embedded via `embed.FS`. One `go build`, one deploy.

### Version control
- **Commit at every working milestone.** Subject says what, body says WHY.
- **Update `CHANGELOG.md` with every commit.** No changelog = no merge. Format:
  ```
  ## [Unreleased]
  ### Added
  - Feature X (reason)
  ### Changed
  - Refactored Y (why)
  ```
- **Push after commit.** Don't let commits accumulate locally.
- **Branch naming:** `feat/short-description`, `fix/short-description`, `refactor/short-description`

### Testing
- **Tests pass before push.** `go test ./...` must succeed.
- **If you fix a bug, add a test for it.** No exceptions.
- **Table-driven tests** — Go convention, follows stdlib patterns.

### Scope discipline
- **No feature creep.** Check DECISIONS.md §9 (What NOT to build) before proposing anything new.
- **Phase-gated.** We're building in 5 phases. Don't build Phase 4 features in Phase 2.
- **One task per PR.** If it takes more than an afternoon, split it.

### Architecture
- **SQLite only.** Single file. Never share across processes (Mino corruption lesson).
- **No Apple-specific code.** Mino runs on Linux VPS.
- **Telegram is the primary interface.** Dashboard is secondary.
- **Extensions are separate processes** (HTTP, not embedded). Systemd manages lifecycle.
- **Tool results compacted inline:** `[tools used: name(args) -> summary]`.
- **No sliding window for chat history.** Unlimited + compaction + consolidation.

### Code patterns
- Flat project structure (see above). No `cmd/`, `internal/`, `pkg/` — that's premature layering.
- Error handling: explicit, never panic in library code
- Logging: `log/slog` (structured, levels)
- Config: environment variables + `~/.mino/config.json`
- SQLite: `mattn/go-sqlite3` with WAL mode
