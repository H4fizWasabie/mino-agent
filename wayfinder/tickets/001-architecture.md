# Nervous System Architecture

## Question

How does the nervous system fit into Mino's existing Go architecture?

Mino has: `loop.go` (LLM reasoning loop), `session.go` (history, context), `memory.go` (SQLite+FTS5), `telegram.go` (bot gateway), `dashboard.go` (web UI+SSE), `tools.go` (tool registry).

Key decisions:
- **New files?** What gets added flat in root? (`nerves.go`? `interrupt.go`? `audit.go`?)
- **Data flow:** When a Telegram `/btw` message arrives mid-loop, what path does it take through the code?
- **Goroutine model:** Who owns the main loop goroutine? Who owns the interrupt listener? How do they communicate? (channel? context cancellation?)
- **Audit storage:** New SQLite table? Extend existing schema? Separate file?
- **Loop detection placement:** Inside loop.go or separate watcher goroutine?

## Type

wayfinder:grilling

## Resolution

### New files
- `nerves.go` — runtime awareness: `LoopSnapshot` struct, interrupt goroutine, prefix matching, loop detection (~100 lines)
- `audit.go` — historical record: schema, writer, query tool (~80 lines)

### Interrupt mechanism
- **Separate goroutine** per session — second LLM call runs independently alongside main loop
- Interrupt scope: **read-only** — queries state, uses read-only tools, does not modify session/memory
- Interrupt LLM: **same model** as main loop — separate API call, simpler config
- Prefix-based routing: `btw`, `by the way`, `status`, `what are you doing` etc.
- Request channel: surface pushes `{query, replyFunc}` onto channel; interrupt calls `replyFunc(response)` — surface-agnostic

### LoopSnapshot (on Conversation, ephemeral)
```go
type LoopSnapshot struct {
    Iteration   int
    Status      string        // "thinking", "running_tool", "waiting"
    CurrentTool string
    LastOutput  string        // truncated
    ToolHistory []string      // last N calls: "read_file(notes.md) -> ok"
    StartedAt   time.Time
}
```
- Lives on `Conversation` (reuses existing mutex)
- Main loop updates each iteration
- Interrupt goroutine reads under lock
- Ephemeral — no persistence, no context pollution

### Interrupt tools
- Read-only subset: `read_file`, `recall`, `bash` (read-only commands)
- Enables investigation: "what happened?" → Mino inspects real errors

### Loop detection placement
- Inside `nerves.go` alongside snapshot — same goroutine or called from loop guard

### Audit storage
- New SQLite table(s) — reuse existing DB connection. Exact schema deferred to ticket 004.

## Blocks

- Interrupt Mechanism
- Loop Detection
- Self-Audit System
- Telegram Async Layer
- Dashboard Live State
