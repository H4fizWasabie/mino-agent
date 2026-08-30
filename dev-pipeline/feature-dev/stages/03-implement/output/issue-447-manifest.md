# Implementation manifest: after-the-fact guardrail-deviation detection

Issue: #447
Branch: `fix/issue-447-guardrail-deviation`

## Files changed

- `playbook_workspace.go`: compares each LLM stage attempt with declared tools
  and output paths, then records/pages deviations through existing sinks without
  changing stage status or retry behavior.
- `playbook_test.go`: covers clean attempts, combined mechanical flags and
  outbox reporting, and the non-blocking stage boundary.
- `CHANGELOG.md`: records #447 under Unreleased.
- `README.md`: documents where deviation evidence is inspected.
- `docs/architecture-series.md`: documents advisory after-the-fact detection.
- `dev-pipeline/feature-dev/stages/02-design/output/issue-447-design.md`:
  records the locked design and invariant checks.

## New interfaces

- `stageDeviationFlags(pb, run, stage, calls, verificationErr) []string`
- `reportStageDeviations(core, sessionID, pb, run, stage, flags)`

No configuration keys, migrations, dependencies, or provider-specific paths.

## Tests and checks

- `TestStageDeviationFlagsAndReports`
- `TestStageDeviationFlagsCleanAttempt`
- `TestWorkspaceReportsDeviationWithoutBlocking`
- Focused deviation/workspace tests: PASS
- `go test ./... -count=1`: PASS (`258.165s`)
- `go vet ./...`: PASS
- `go build ./...`: PASS
- `git diff --check`: PASS
- `graphify update .`: PASS
- `codegraph sync`: PASS

## Invariants and scope

All shared harness invariants remain held. Detection is mechanical and
after-the-fact. It does not judge Process prose, block a loop, alter retry
policy, parse shell commands, or redesign the playbook execution path. Shell
commands remain opaque; future checkpoint work (#449/#450/#451) can extend the
comparison boundary without duplicating this reporting sink.
