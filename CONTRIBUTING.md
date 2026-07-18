# Contributing

Mino keeps it simple. Less code = less bugs.

## Rules

1. **Go stdlib first.** No external dependency without discussion.
2. **~100 lines per file max.** If it's growing, split it.
3. **Table-driven tests.** `go test ./...` passes before push.
4. **Update CHANGELOG.md** with every PR.
5. **One feature per PR.** If it takes more than a day, split it.

## Setup

```bash
git clone https://github.com/H4fizWasabie/mino-agent
cd mino-agent
go build -tags sqlite_fts5
go test ./...
```

## Project layout

```
main.go          — entry point
loop.go          — agent loop (~100 lines)
session.go       — system prompt, context
memory.go        — SQLite + FTS5
tools.go         — built-in tools
provider.go      — LLM client
dashboard.go     — web UI + API
telegram.go      — Telegram bot
```

Send PRs to `master`. Keep diffs small.
