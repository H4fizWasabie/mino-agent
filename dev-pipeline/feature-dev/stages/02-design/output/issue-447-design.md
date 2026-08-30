# Design: after-the-fact guardrail-deviation detection

Issue: #447
Status: design locked from the GitHub grilling decision

## Problem

Playbook stages already scope the registry and verify declared outputs, but a
turn that attempts an undeclared tool or writes an undeclared run artifact is
not surfaced as a contract deviation when the run otherwise continues. The
harness should record and page these mechanical signals without adding a
second model judge or blocking the loop.

## Approaches considered

### A. Inspect the completed stage turn at the existing stage boundary

Compare the stage's declared tool names and output paths with the actual
`ToolCall` trail returned by the existing stage loop. Reuse the existing output
verification error for missing outputs, then record one structured deviation
event and queue one owner alert for each affected stage attempt.

Cost: one small comparison seam at the current boundary; path-bearing shell
commands remain intentionally opaque rather than being parsed heuristically.

### B. Add declaration metadata to `Registry`

Carry the stage contract inside the registry and have `ExecuteContext` emit
deviations while tools execute.

Cost: couples the generic tool registry to playbook contracts and spreads
stage-specific alerting into the universal execution boundary.

### C. Ask a model to judge prose compliance

Send the Process text and tool trail to an LLM after each turn.

Cost: adds latency and provider dependence, and repeats the false-positive
class the map explicitly defers after #436.

Recommendation: A. It observes the existing trail at the narrowest existing
stage boundary and keeps the registry and loop canonical.

## Interface and data flow

Add a deterministic comparison seam:

`stageDeviationFlags(pb, run, stage, calls, verificationError) []string`

It reports only:

- a called tool absent from a non-empty stage `Tools` list;
- a `write_file` path that is not one of the stage's resolved declared output
  paths;
- an existing deterministic stage verification error, including a missing
  declared output or failed declared success outcome.

Add an alert seam:

`reportStageDeviations(core, sessionID, pb, run, stage, flags)`

It writes a `stage_deviation` trace event, an audit event, and an owner outbox
message. It does not change `LoopResult.Status`, stop the loop, retry the stage,
or modify the playbook contract. Existing stage verification and retry rules
remain unchanged.

The report runs after every LLM stage attempt, after output verification has
collected the attempt's deterministic result. Script stages are already
zero-inference and are outside this turn-level comparison.

## Configuration

No new configuration keys. The existing stage Tools and Outputs declarations
are the source of truth; absent optional tool declarations continue to mean
the existing unrestricted registry behavior.

## Failure behaviour

- Unknown or undeclared tool calls are recorded as deviations even when the
  restricted registry returns an error and no tool side effect occurs.
- Missing outputs retain the existing failure/retry behavior and are also
  reported as deviations.
- Trace, audit, or outbox write failure is non-fatal; the stage result is not
  changed because detection is advisory after the fact.
- Cancellation, provider errors, and hard loop limits retain existing loop and
  run-state behavior. A completed attempt is inspected; an attempt that never
  returns cannot produce a turn report.
- No provider or model is named; all behavior is based on the common tool-call
  trail.

## Invariant check

- Model agnosticism: held — only generic `ToolCall`, stage declarations, and
  existing host sinks are used.
- Loop termination: held — no new loop; existing bounded stage loop remains.
- Context management: held — alerts contain bounded flag strings, not tool
  output bodies.
- Guardrails: held — the existing restricted registry and output verifier stay
  at their current boundaries; detection cannot bypass them.
- Failure explicit: held — deviations are trace/audit/outbox visible, while
  sink failures remain non-fatal and do not masquerade as stage failure.
- State local and inspectable: held — trace, SQLite audit, and local outbox are
  existing owner-readable stores.
- Single binary/no framework: held — stdlib and existing sinks only.

## Files to touch

- `playbook_workspace.go`: comparison and report seams; stage boundary hook.
- `playbook_test.go`: undeclared tool, undeclared output path, and alert/report
  regression coverage.
- `CHANGELOG.md`: Unreleased entry for #447.
- `README.md`, `docs/architecture-series.md`: user-facing behavior note.
- Feature pipeline outputs for implementation, verification, and ship notes.
