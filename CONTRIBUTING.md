# Contributing

Mino keeps it simple. Less code = less bugs.

## Process rules

All process rules live in **[docs/rules.md](docs/rules.md)** — issue-first, release gating, version control, public-facing discipline, testing. Read it before opening a PR. Coding style lives in **[docs/coding-conventions.md](docs/coding-conventions.md)**.

The short version:

1. **No code change without a GitHub issue.** Create it first, reference it everywhere.
2. **Table-driven tests.** `go test ./...` passes before push; a bug fix ships with its test.
3. **Update `CHANGELOG.md`** with every PR — generic why-notes, never the owner's incidents.
4. **One feature per PR.** If it takes more than a day, split it.
5. **Branches:** `fix/issue-<N>-short-name`, `feat/...`, `refactor/...`.
6. **Commit at every milestone** — subject says what, body says WHY. Push after commit.

## Setup

Read **[docs/decisions.md](docs/decisions.md)** before contributing. It explains the architecture, philosophy, and what NOT to build.

```bash
git clone https://github.com/H4fizWasabie/mino-agent
cd mino-agent
go build -trimpath -buildvcs=false -ldflags="-buildid="
go test ./...
```

## Project layout

```
main.go          — entry point
loop.go          — agent loop (~100 lines)
session.go       — system prompt, context
memory.go        — Markdown graph facts + SQLite chat log/consolidation
tools.go         — built-in tools
provider.go      — LLM client
dashboard.go     — web UI + API
telegram.go      — Telegram bot
```

Send PRs to `master`. Keep diffs small.
