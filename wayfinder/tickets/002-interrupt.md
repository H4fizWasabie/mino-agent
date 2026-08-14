# Interrupt Mechanism

Status: **RESOLVED** (historic — decided/shipped; predates the Status-line format)

## Question

Build the mid-loop response system. When the owner sends `/btw` (or equivalent) via Telegram, Mino must respond *without* waiting for the current tool call or LLM call to finish.

What to build:
- Interrupt channel/listener that lives alongside the main loop
- Mechanism to pause/signal the loop, inject the user query, get a response, then resume
- The loop must be aware an interrupt happened (audit trail)

## Type

wayfinder:task

## Resolution

### Files changed
- **`nerves.go`** (new, ~170 lines): `LoopSnapshot` struct, `isInterrupt()` prefix matcher, `handleInterrupt()` sync LLM call, `ObserveOnly()` tool filter, `detectLoop()` placeholder
- **`app.go`**: added `snapshots sync.Map` to Core; `startLoop`/`endLoop`/`snapshotUpdater` wired into `RespondForContext`
- **`loop.go`**: snapshot updates at iteration start ("thinking"), before tool ("running_tool"), after tool (result + history)
- **`telegram.go`**: interrupt routing via `go w.handleInterrupt(...)`
- **`dashboard.go`**: interrupt routing for `/api/chat` (sync) and `/api/chat/stream` (async SSE)

### How it works
1. Loop starts → `startLoop()` creates snapshot on `Core.snapshots`
2. Each iteration: loop updates snapshot via context callback
3. Message arrives with interrupt prefix ("btw", "status", etc.) → `isInterrupt()` matches
4. If snapshot exists (loop running) → `handleInterrupt()` makes separate LLM call with read-only tools
5. Response delivered via `replyFunc` (Telegram message, dashboard JSON, or SSE event)

### Key decisions
- Snapshot is ephemeral (`sync.Map`, not persisted)
- `handleInterrupt` is synchronous; callers choose async (`go` for Telegram/SSE, direct for dashboard non-stream)
- Interrupt LLM: same model, 30s timeout, 1024 max tokens, `_intr` suffix session ID
- Read-only tools via `ObserveOnly()` — filters `BehaviorObserve` tools from registry
- Loop detection threshold: 3 repeated tool calls (placeholder for ticket 003)

## Blocks

- Telegram Async Layer
- Dashboard Live State (partial)
