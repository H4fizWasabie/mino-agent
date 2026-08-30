# Project Identity

What Mino is, what it runs on, and how to build and test it. Canonical source for stack
facts. Other files point here rather than repeating them.

## One Sentence

A model-agnostic agent harness with loops, context management, guardrails, playbooks, and a Telegram interface, built as a single Go binary with local SQLite state.

## What Mino Is

Mino is a model-agnostic agent harness. The harness owns the loop, the context, and the
boundaries. Models are interchangeable parts behind an adapter, never the centre of the
design.

Current capability areas:

- Agent loops
- Context management (memory consolidation, prompt assembly)
- Guardrails
- Telegram interface
- Playbooks (autonomous repeatable workflows)
- Memory (Markdown-authoritative graph memory with episodic/semantic split)
- An extension protocol (HTTP-based, for capabilities not every owner needs)
- A web dashboard

## What Mino Is Not

Mino is not tied to one provider, not a chat wrapper, and not a framework that requires a
server to run. A feature that only works with one model vendor is a feature in the wrong
place.

## Stack

| Item | Value |
|------|-------|
| Primary language | Go |
| Base framework | none — single Go binary, stdlib-first, no framework |
| State storage | Local SQLite file (`~/.mino/state.db`, WAL mode, single connection) |
| Target providers | Anthropic (Claude, OAuth), OpenAI/Codex (OAuth), GitHub Copilot, xAI/Grok, OpenRouter (fallback + routing) |
| Interface surfaces | CLI, web dashboard, Telegram |

## Commands

| Purpose | Command |
|---------|---------|
| Build | `go build ./...` |
| Test | `go test ./...` |
| Lint | `go vet ./...` |
| Run locally | `./mino` |

## Repository Paths

| Item | Path |
|------|------|
| Source root | repository root (flat package structure, no `cmd/` or `internal/`) |
| Changelog | `CHANGELOG.md` |
| User documentation | `docs/` |
| Repo navigation | [`AGENTS.md`](../../../AGENTS.md) (rules), [`CONTEXT.md`](../../../CONTEXT.md) (task-to-area routing) |
| Architecture proposals / open decisions | [`wayfinder/MAP.md`](../../../wayfinder/MAP.md), `wayfinder/tickets/` |
