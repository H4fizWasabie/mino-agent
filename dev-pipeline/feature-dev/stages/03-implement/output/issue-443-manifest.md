# Implementation manifest: Progress-based iteration extension

## Files changed

- `loop.go`: extended the existing repetition signal with a hashed tool-result
  identity, added the three/six no-progress behavior, raised the outer bound to
  the fixed 60-iteration ceiling, and added distinct cap-reply reasons.
- `loop_regression_test.go`: covered no-progress stopping, continuation beyond
  the base budget, the hard ceiling, and updated cap-reply callers.
- `context_test.go`: changed the legacy same-tool detector integration test to
  verify it remains advisory rather than pre-empting progress-based termination.
- `dev-pipeline/feature-dev/stages/02-design/output/issue-443-design.md`:
  stage-02 design contract.
- `dev-pipeline/feature-dev/stages/04-verify/output/issue-443-verification.md`:
  verification evidence.
- `dev-pipeline/feature-dev/stages/05-ship/output/issue-443-release-note.md`:
  release record.
- `README.md`, `docs/architecture-series.md`, and `CHANGELOG.md`: updated
  user-facing iteration-limit behavior.

## Interfaces

- `RunLoop` and `RunLoopContext` signatures remain unchanged.
- `iterationCapReply` now accepts a stop reason.
- Added internal `loopProgressSignature` using the existing call signature plus
  a SHA-256 result identity.

## Config keys

None. The caller's existing `maxIter` remains the base budget; the harness has
a fixed hard ceiling of 60.

## Tests added or changed

- `TestLoopStopsAfterNoProgressNudge`
- `TestLoopExtendsBaseBudgetWhileProgressing`
- `TestLoopStopsAtHardIterationCeiling`
- `TestLoopLegacyDetectionAdvisesWithoutHardStop`
- Existing `iterationCapReply` tests updated for the required reason argument.

## Verification performed

- Focused loop/checkpoint tests: passed.
- Full `GOCACHE=/tmp/mino-443-go-build go test ./... -count=1`: passed with
  socket-enabled test execution; the restricted run could not open the
  repository's existing IPv6 localhost `httptest` listener.
- `GOCACHE=/tmp/mino-443-go-build go vet ./...`: passed.
- `git diff --check`: passed.
- Graphify update and CodeGraph sync: passed.

## Invariants

All harness invariants remain held. Every loop now has the fixed 60-iteration
outer bound; malformed responses still hit the existing parse-failure breaker;
provider behavior is not named; checkpoint state remains local and inspectable.

## Deferred

- #448 self-repair, #447 mechanical deviation detection, and the grouped
  #449/#450/#451 checkpoint design remain separate work.
- No release, deployment, scheduler, or production-state mutation performed.
