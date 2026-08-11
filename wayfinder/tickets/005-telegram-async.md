# Telegram Async Layer

## Question

Wire up interrupt/async messaging to Telegram. Mino's Telegram gateway must support:
- Async `/btw`-style messages that interrupt the loop mid-flight
- Mino can proactively send messages (not just respond) — e.g., "Owner, I noticed I'm looping"
- Messages from interrupt path vs normal response path are distinguishable

Build on existing `telegram.go`.

## Type

wayfinder:task

## Resolution

### What was built
- **Proactive notifications**: Telegram observer handles `"loop"` events → sends `🔄` message to the owner mid-task ("Detected 3 repeated calls to X — continuing but trying to self-correct.")
- **Distinguishable replies**: Interrupt responses prefixed with `⚡` to visually separate from normal responses
- **Async interrupt** (from ticket 002): `isInterrupt()` prefix matching + `go handleInterrupt()` goroutine for non-blocking `/btw` queries

### Behavior
- the owner sends "btw status" mid-loop → Mino responds with `⚡ <status>` asynchronously
- Mino detects a loop → proactively sends `🔄 Detected 3 repeated calls to X...`
- Interrupt replies never block the main loop

## Blocks

_none_
