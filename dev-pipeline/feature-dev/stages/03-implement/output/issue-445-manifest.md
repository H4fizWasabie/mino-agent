# Implementation manifest: #445

Branch: `fix/issue-445-parallel-observe-tools`

## Files changed

- `loop.go`: added `batchRunsConcurrently`/`toolUseNames`; the tool-execution block now
  pre-computes `raw` output for an eligible batch concurrently (goroutine per call, panic
  recovered per call, `sync.WaitGroup`), then feeds the existing per-call bookkeeping loop
  from a `precomputedRaw` map when present, live execution otherwise. No other line in the
  bookkeeping loop changed.
- `loop_concurrent_tools_test.go` (new): concurrent-batch timing proof, mixed-batch stays
  sequential (timing proof), panic isolation, emission-order preservation.

## New interfaces

See `../02-design/output/issue-445-design.md`'s Interfaces section — unchanged from design.

## New config keys

None.

## Tests added

`TestConcurrentObserveOnlyBatchRunsInParallel`, `TestMixedBatchWithMutateStaysSequential`,
`TestConcurrentBatchPanicIsolated`.

## Build and test results

- `go build ./...`: PASS.
- Focused new tests (3): PASS, including under `go test -race`.
- Full existing loop/tool test suite (107 tests): PASS unchanged, including under `-race`.
- Full suite: see `../04-verify/output/issue-445-verification.md`.

## Deferred / known limitations

Carried from the design note: a hypothetical misclassified `BehaviorObserve` tool with
internal side effects could race under concurrency — a property of correct classification
generally, not new here; no such tool exists in the registry today.
