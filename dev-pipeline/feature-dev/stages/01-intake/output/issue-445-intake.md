# Intake: true parallel/concurrent tool-call batching

Issue: #445. Grilled 2026-08-31, scoped down and agreed.

## Problem

Today the model can only have one tool call in flight per turn, executed and awaited before
the next is requested — even for independent, unrelated calls, wasting wall-clock time.

## Correction found during grilling

The premise needed a correction: `loop.go`'s tool-execution loop already handles multiple
`tool_use` blocks in one model response (`for _, tc := range toolUses`) — batching already
exists. What's missing is concurrency: each call in that loop still runs synchronously, one
after another.

## Decision

Scoped concurrency, not full concurrency. A batch of `tool_use` blocks runs concurrently only
when every call in it is `BehaviorObserve` and none is `view_image` (a separate synchronous
vision step out of scope here). Any batch containing a `BehaviorMutate` call, or a single
call, stays fully sequential — unchanged from today.

Full concurrency (including mutating/approval-gated calls) was considered and rejected: no
incident motivates it, and it would add real new failure surface — approval-gate contention,
mutation-ordering risk if a model emits two dependent calls in one response, non-deterministic
trace ordering — for a latency win nothing has asked for at that scope.

## Non-goals

- Concurrent execution of `BehaviorMutate` or approval-gated tool calls.
- Parallelizing `view_image`'s vision-description step.
- Any change to message-history, trace, or audit *ordering* — concurrency must only affect
  wall-clock time, never observable ordering (verified: results are always processed in the
  model's original emission order, never completion order).

## Acceptance criteria

1. A batch of two or more `BehaviorObserve` calls (no `view_image`) executes concurrently —
   demonstrated by wall-clock time closer to the slowest single call than the sum of all
   calls.
2. A batch containing any `BehaviorMutate` call stays fully sequential — wall-clock time
   reflects the sum of all calls' durations, unchanged from today.
3. Regardless of actual completion order, `result.ToolCalls` and the tool-result messages fed
   back to the provider are always in the model's original emission order.
4. A panicking tool call in a concurrent batch is isolated — it reports as an error output for
   that one call, other calls in the batch still complete, and the process doesn't crash.
5. A new `tool_batch` trace event records when a batch ran concurrently, with its size and
   tool names.
6. `go build ./...`, `go vet ./...`, and the full test suite pass, including under `-race`.
