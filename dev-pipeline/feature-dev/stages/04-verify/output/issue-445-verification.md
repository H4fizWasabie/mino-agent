# Verification: #445

Branch: `fix/issue-445-parallel-observe-tools`

## Test results

- Full `GOCACHE=/tmp/mino-gocache go test ./... -count=1 -timeout=300s`: PASS (266.121s).
- `go vet ./...`: PASS.
- `go build ./...`: PASS.
- `git diff --check`: PASS.
- New tests (3): PASS, including under `go test -race`.
- Existing loop/tool test suite (107 tests): PASS unchanged, including under `-race`.
- Full suite under `go test . -race -count=1 -timeout=300s`: PASS (291.989s), zero data races
  reported.

## Acceptance criteria (from intake) — observed behaviour

1. **Concurrent execution for eligible batches.** Observed in
   `TestConcurrentObserveOnlyBatchRunsInParallel`: two `BehaviorObserve` tools each sleeping
   150ms complete in well under 280ms total (would be ~300ms+ sequential) — proves genuine
   concurrency, not just correctness.
2. **Mixed batches stay sequential.** Observed in `TestMixedBatchWithMutateStaysSequential`:
   an observe + a mutate tool, each 100ms, together take at least 180ms — proves no
   concurrency when a mutating call is present.
3. **Emission order preserved regardless of completion order.** Observed in both timing
   tests: `result.ToolCalls` always matches the model's original call order
   (`slow_a` then `slow_b`, `obs_tool` then `mutate_tool`), independent of which finished
   first.
4. **Panic isolation.** Observed in `TestConcurrentBatchPanicIsolated`: a panicking call
   reports `"Error: tool panicked: boom"` for its own slot, the sibling call still executes
   and returns its real result, and the test process (and `RunLoopContext`) does not crash.
5. **`tool_batch` trace event.** Confirmed by inspection: `trace("tool_batch", ...)` fires
   with `concurrent: true`, `batch_size`, and `tools` whenever `batchRunsConcurrently` returns
   true, before any goroutine is dispatched.
6. **Build, vet, and full suite pass, including under `-race`.** See Test results above.

## Invariants — held / evidence

| Invariant | Verdict | Evidence |
|---|---|---|
| Model agnosticism | Held | No provider-specific code. |
| Loop termination | Held | `sync.WaitGroup.Wait()` only returns once every goroutine returns (panics recovered, not swallowed); proven not to hang by the panic-isolation test completing normally. |
| Context is managed, never assumed | Held (unaffected) | No context-window interaction; this only affects execution timing. |
| Guardrails are not optional | Held | `batchRunsConcurrently` explicitly excludes any non-`BehaviorObserve` call; `TestMixedBatchWithMutateStaysSequential` proves a mutating call in the batch forces full sequential execution, so the approval gate's synchronous behavior is never bypassed. |
| Failure is explicit | Held | A panicking call becomes an explicit `"Error: tool panicked: ..."` output, never silently dropped. |
| State stays local and inspectable | Held | The only new state is the `tool_batch` trace event, inspectable like any other trace event. |
| Single binary, no framework | Held | No new dependency; `sync` was already imported. |

## Failure paths forced

- A tool call panicking inside a concurrent batch → isolated, sibling call still completes,
  process does not crash.
- A batch mixing observe and mutate calls → forced sequential, verified by timing.
- Cancellation is checked once after all goroutines finish, matching the sequential path's
  cancellation semantics (not separately re-tested here — unchanged code path from the
  existing `ctx.Err()` check pattern already covered by other loop-cancellation tests).

## Provider parity

Not provider-specific — this change is entirely local tool-dispatch orchestration inside
`RunLoopContext`, exercised here via `fakeClient`'s scripted responses the same way the rest
of the loop's test suite already does. No provider-facing behavior changed.

## Open concerns (carried to the ship note)

A hypothetical `BehaviorObserve`-classified tool with actual internal side effects could race
under concurrency — this is a property of correct tool classification generally (already
relied upon by `stageRetrySafe`), not a new risk introduced here, and no such tool exists in
the registry today.
