# Loop Detection

## Question

Build self-awareness of cognitive loops. Mino detects when it's repeating the same pattern and self-corrects.

What to build:
- Pattern detection: same tool + same args N times? same error pattern? configurable threshold?
- Action on detection: pause, self-audit entry, optionally escalate to the owner via Telegram
- Integration: does this live inside loop.go as a guard, or as a separate watcher goroutine?

## Type

wayfinder:task

## Resolution

### What was built
- **`detectLoop()`** in `nerves.go` — checks last N tool calls for exact repetition (threshold: 3)
- **Wired into `loop.go`** — after each iteration, builds history from `result.ToolCalls`, calls `detectLoop()`
- **Action on detection:** injects a `[System: loop detected — ...]` message into the conversation so the LLM self-corrects
- **Dedup:** tracks `lastLoopDetected` string, only injects once per distinct loop pattern
- **Observer:** fires `obs("loop", ...)` so surfaces can react
- **Test:** `nerves_test.go` with 6 table-driven cases (empty, below threshold, exact, above, mixed, interleaved)

### Key decisions
- Detection: exact tool+args match, 3+ repetitions
- Self-correction: inject system message (LLM is smart enough to adjust)
- No auto-cancellation — lets LLM self-correct first
- Deduplication: one injection per distinct pattern, not every iteration

## Blocks

_none_
