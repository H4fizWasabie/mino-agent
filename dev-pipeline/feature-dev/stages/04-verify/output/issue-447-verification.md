# Verification report: after-the-fact guardrail-deviation detection

Issue: #447

## Results

- Focused tests for clean flags, combined flags/reporting, and the non-blocking
  stage boundary: PASS.
- Full `GOCACHE=/tmp/mino-447-go-build go test ./... -count=1`: PASS
  (`258.165s`).
- `go vet ./...`: PASS.
- `go build ./...`: PASS.
- `git diff --check`: PASS.
- `graphify update .`: PASS.
- `codegraph sync`: PASS.

## Acceptance criteria observed

- An undeclared tool in a non-empty stage whitelist produced a structured
  deviation flag and an owner outbox alert.
- A `write_file` target outside the stage's resolved declared outputs produced a
  structured deviation flag.
- A deterministic output-verification error was included in the same report.
- A clean attempt produced no flags.
- A stage with an undeclared attempted tool still completed successfully; the
  report path did not alter status or retry behavior.
- The report was written to the local outbox and trace path. SQLite audit
  persistence is exercised by the existing audit seam when a database is
  configured.

## Failure paths

- Unknown/undeclared tool: forced in `TestStageDeviationFlagsAndReports`; it
  was reported without executing the missing tool.
- Missing/invalid output verification: forced with a verification error; it was
  reported while existing stage failure behavior remained unchanged.
- Report sink unavailable: report helpers return without changing stage state
  when core/settings are absent; queue/audit/trace failures remain non-fatal by
  existing sink contracts.
- Cancellation/provider failure/hard loop limit: no new path was introduced;
  existing loop and run-state behavior remains covered by the full suite.

## Invariants

All seven shared invariants held. The change adds no loop, provider adapter,
configuration, dependency, or remote state. Guardrails remain enforced by the
existing restricted registry and output verifier; the new comparison is
after-the-fact observation only.

## Provider parity and concerns

Provider parity was not run because this change does not call or alter a model
provider; it consumes the common `ToolCall` trail after the provider boundary.
The mechanical scope check intentionally does not parse shell commands, so
paths written only inside an opaque `bash` command remain outside this v1
signal. The existing stage output mtime verifier still covers declared outputs.
