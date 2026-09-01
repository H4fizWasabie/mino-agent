# Verification report: remove dead ponytail-audit paths

## Result

The issue-502 change passes the relevant fresh verification. The full package
run has one pre-existing, unrelated timeout failure in
`TestVerifyNewBinaryTimeout`; it is isolated below and was not changed.

## Commands and evidence

- `GOCACHE=/tmp/mino-ponytail-go-build rtk go vet ./...` — passed.
- `rtk git diff --check` — passed.
- `GOCACHE=/tmp/mino-ponytail-go-build rtk go test -count=1 -timeout 180s
  -skip '^TestVerifyNewBinaryTimeout$' ./...` — 843 passed, 1 skipped in 2
  packages.
- `GOCACHE=/tmp/mino-ponytail-go-build rtk go test -count=1 -timeout 180s ./...`
  — 843 passed before the package timed out at 180 seconds on
  `TestVerifyNewBinaryTimeout`, with 1 skipped. The test launches a shell
  `sleep 60` command and calls `verifyNewBinary` with a one-second timeout
  (`rollback_test.go:91-103`); this is outside the issue-502 diff.
- `rtk graphify update . --force` and `rtk codegraph sync` completed after the
  edits. The deleted dedicated-runner, streaming-wrapper/parser, registry-only,
  duplicate-sorter, and time-sentinel symbols are absent from the indexes.

## Acceptance checks

| Check | Result |
|---|---|
| One canonical playbook execution path remains | Pass — navigator and scheduler paths remain; old runner symbols are removed |
| Unused streaming API surface is removed | Pass — loop/provider clients use `CreateContext`; Codex internal Responses SSE remains |
| Provider request and JSON behavior remains covered | Pass — OpenAI-compatible, Anthropic, and Codex tests pass |
| MCP startup/reload behavior is unchanged | Pass — `Start` delegates to the existing `Reload` implementation |
| Shared helpers are reused instead of duplicated | Pass — community labels use `sortedInts`; `Registry.Only` is removed |
| No config, dependency, endpoint, or persistent-state change | Pass |
| Full repository suite | Known unrelated timeout above; scoped fresh suite passes |

## Scope decision

No code or test failure caused by issue #502 remains. The rollback timeout test
should be repaired separately if the environment continues to leave its child
process running past the context deadline; it is not part of this refactor.

