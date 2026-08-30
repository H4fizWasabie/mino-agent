# Design: scoped concurrent tool-call batching

Issue: #445.

## Chosen approach

`RunLoopContext`'s tool-execution block (`loop.go`) gains a pre-pass: before the existing
per-call bookkeeping loop, `batchRunsConcurrently(tools, toolUses, toolArgs)` decides whether
every call in the batch is `BehaviorObserve` and none is `view_image`. If eligible, each
call's `tools.ExecuteContext` (and its malformed-`__raw_arguments__` handling) runs in its own
goroutine, writing into a pre-sized `raws []string` indexed by emission order; the goroutines
do nothing else — no shared-state mutation, no trace/snapshot/notify calls. Once all goroutines
finish (`sync.WaitGroup`), a single `ctx.Err()` check preserves the existing
cancel-mid-execution behavior, then results are copied into a `precomputedRaw map[int]string`.

The original per-call loop is otherwise untouched: it now reads `raw` from
`precomputedRaw[idx]` when present, or executes live (today's exact code path) when not. Every
line of bookkeeping after `raw` is obtained — provenance-gate check, snapshot updates, trace,
`readsSinceWrite` accounting, `toolResults` append — runs exactly as before, in the same loop,
in the same order, regardless of which path supplied `raw`. This is what guarantees zero
observable-ordering change: only the wall-clock timing of the execution phase differs.

## Interfaces

- `batchRunsConcurrently(registry *Registry, toolUses []ContentBlock, args []map[string]any)
  bool` — loop.go.
- `toolUseNames(toolUses []ContentBlock) []string` — loop.go, for the trace event payload.
- No change to `Tool`, `ToolCall`, `LoopResult`, or any provider-facing type.

## Config surface

None.

## Failure behaviour

- **A tool call panics inside a goroutine**: `recover()` inside the goroutine turns it into
  `"Error: tool panicked: <value>"` for that call's slot only; `sync.WaitGroup` still
  completes normally, other calls in the batch are unaffected.
- **Context cancelled mid-batch**: checked once after all goroutines finish (matching the
  granularity a concurrent batch can offer — individual `ExecuteContext` calls may or may not
  observe cancellation internally, same as today's sequential calls); the loop returns
  `"cancelled"` exactly as the sequential path does.
- **Malformed native tool-call args** (`__raw_arguments__`): handled identically whether
  concurrent or sequential — the goroutine (or the sequential branch) short-circuits to the
  same error string without calling `ExecuteContext` at all.

## Invariant check

- **Model agnosticism**: held — no provider-specific code.
- **Loop termination**: held — `sync.WaitGroup.Wait()` only returns once every goroutine has
  returned (including via `recover()`), so the batch can't hang the turn indefinitely any
  differently than a sequential call already could.
- **Context is managed, never assumed**: held (unaffected).
- **Guardrails are not optional**: held — eligibility explicitly excludes any
  `BehaviorMutate`/approval-gated call from the concurrent path; the approval gate's existing
  synchronous behavior for mutating calls is completely untouched.
- **Failure is explicit**: held — a panicking call surfaces as an explicit error output, never
  silently dropped or silently crashing the loop.
- **State stays local and inspectable**: held — no new persisted state; the new `tool_batch`
  trace event is inspectable the same way every other trace event is.
- **Single binary, no framework**: held — no new dependency; `sync` is already imported.

## Known limitations (carried to the ship note)

1. Two calls to the *same* read-only tool with dependent semantics (e.g., a tool that reads
   then writes a counter internally, misclassified as `BehaviorObserve`) could race if such a
   tool existed — no such tool exists in the registry today; this is a property of correct
   `BehaviorObserve` classification generally, not new to this change.
2. `view_image` batches and any batch containing a single call see no benefit — intentionally
   out of scope per the design's narrow eligibility.

## Files to touch

`loop.go`, `loop_concurrent_tools_test.go` (new).
