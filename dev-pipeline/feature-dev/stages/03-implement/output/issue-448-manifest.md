# Implementation manifest: Mid-flight self-repair

## Files changed

- `loop.go`: added one-shot self-repair at two shared no-progress outcomes,
  with trace evidence and reset on progress.
- `loop_regression_test.go`: covered repair injection, one-shot behavior, and
  reset after progress; existing #443 interactions remain covered.
- `README.md`, `docs/architecture-series.md`, `CHANGELOG.md`: documented the
  new user-visible repair prompt.
- `dev-pipeline/feature-dev/stages/02-design/output/issue-448-design.md`:
  design contract.

## Interfaces and config

No exported signatures or configuration keys changed. The fixed two-outcome
trigger uses the existing `loopProgressSignature` state.

## Verification

- Focused loop/self-repair tests: passed.
- Full `GOCACHE=/tmp/mino-448-go-build go test ./... -count=1`: passed.
- `go build ./...`: passed.
- `GOCACHE=/tmp/mino-448-go-build go vet ./...`: passed.
- `git diff --check`: passed.
- Graphify update and CodeGraph sync: passed.

## Deferred

#447 guardrail-deviation detection remains separate. No release, deployment,
scheduler, or production-state mutation was performed.
