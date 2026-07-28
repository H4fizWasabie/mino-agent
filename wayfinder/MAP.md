# Mino Nervous System — Wayfinder Map

## Destination

Working implementation in Mino of a cognitive nervous system — the connective tissue that makes Mino self-aware. Three capabilities, two surfaces:

1. **Live introspection** — Mino can respond mid-loop without waiting for tool to finish (`/btw` equivalent)
2. **Loop detection** — Mino spots repetition, self-corrects, asks for guidance
3. **Self-audit** — persistent, queryable trail of what happened and why

Surfaces: **Telegram** (primary, with async/interrupt) + **Dashboard** (web UI, live state)

Leads directly to working code — no handoff spec.

## Notes

- **Language:** Go, flat in Mino root
- **Existing pieces:** `loop.go`, `session.go`, `memory.go`, `telegram.go`, `dashboard.go`
- **Not building:** system monitoring daemon, file watchers, crash recovery — that was the old map
- **Skills:** domain-modeling for terminology; executing-plans, tdd when building

## Decisions so far

- [Nervous System Architecture](tickets/001-architecture.md) — Two files (`nerves.go` + `audit.go`), separate goroutine interrupt with read-only scope, `LoopSnapshot` on `Conversation` (ephemeral), prefix-based routing, surface-agnostic reply callback.
- [Dashboard Live State](tickets/006-dashboard-live.md) — `/api/nerves` endpoint, interrupt + loop detection events pushed to SSE event stream.

## Not yet specified

_Map complete — all tickets resolved. Fog cleared._

## Out of scope

- System-level health monitoring (disk, RAM, process crashes)
- External service monitoring (procura-api, etc.)
- Separate Python processes — everything lives in Mino's Go binary
