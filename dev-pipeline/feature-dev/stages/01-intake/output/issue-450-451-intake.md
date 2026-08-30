# Intake: checkpoint tracking and output verification without a dedicated stage-execution path

Issues: #450 (resumable run-state and crash recovery), #451 (declared-output verification)
Grilled and grouped: #442 map comment (2026-08-30), "which declared step is Mino currently
on" is the shared question both reduce to.

## Problem

Today `runWorkspacePlaybook`'s stage-loop wrapper (`playbook_workspace.go`) is the only thing
that knows which playbook stage is in progress, persists resumable run state, and checks
declared outputs were actually produced. The parent map (#442) wants Mino to navigate a
playbook's files as part of its one normal loop instead of a separate `run_playbook`
execution pipeline for chat-triggered runs. Once that wrapper stops running the LLM turn for
a stage, nothing tracks progress, nothing prevents an unsafe resume after a crash, and
nothing verifies a stage's declared outputs without the wrapper's loop-exit to hang the
check on.

## Decision (from grilling session)

- Chat-triggered runs switch to navigation: `run_playbook` stops executing the stage loop and
  instead creates/resumes a `PlaybookRun` (unchanged struct and `state.json` file, reusing
  `loadOrCreatePlaybookRun`/`latestResumablePlaybookRun` as-is) and returns which stage to
  navigate to, in plain text the model reads.
- `schedule_playbook`/the scheduler stay on the existing dedicated stage-loop
  (`runWorkspacePlaybook`) until #452 designs the scheduler's own unified-loop entry point.
  Two entry points, one execution model transiently.
- Checkpoint tracking is mechanical and path-based: a write under
  `<home>/playbooks/<pb>/runs/<run>/stages/<NN-name>/...` is attributed to that run/stage by
  parsing the path (the same resolution `playbookWriteGuard` already does), not by a
  context-tag set by a loop wrapper that no longer exists.
- Non-idempotent resume safety needs no new mechanism: `latestResumablePlaybookRun` already
  refuses to resume a stage whose next status is `running`/`failed` with a non-retry-safe
  tool set (`stageRetrySafe`). `run_playbook` relays "no resumable run, needs manual review"
  instead of silently starting a new run over an unsafe one.
- Output verification reuses #447's existing mechanism (`verifyWorkspaceStageOutputs`,
  `stageDeviationFlags`, `reportStageDeviations`) unchanged, triggered when Mino reads the
  next stage's `CONTEXT.md` (or calls `run_playbook` again to advance) instead of when a loop
  attempt exits. Silent while a stage's outputs are still incomplete — no noise for
  in-progress work.
- Script-backed stages (#304, zero-inference) keep a dedicated tool call for the actual
  script dispatch; the tool executes and updates run state atomically in one call, same as
  today, no LLM turn involved.
- Trace tagging for dashboard grouping is derived the same path-parsing way at the point each
  trace event is logged, replacing the `stageCtx`-injected `traceTagKey` the wrapper set per
  attempt.

## Non-goals

- Removing the dedicated stage-loop or `runWorkspacePlaybook` entirely — it stays live for
  `schedule_playbook` until #452.
- Redesigning how the scheduler enters the unified loop (#452's scope).
- Changing `PlaybookRun`'s JSON schema, `ReconcileInterruptedRuns`, or `prunePlaybookRuns` —
  all reused unchanged.
- True concurrent tool-call batching, retry-policy redesign, or paging changes (#444/#445/#446,
  separate tickets).

## Surfaces touched

- `playbook.go` (`makeRunPlaybookTool`): behaviour change, chat entry point.
- `playbook_workspace.go`: new path-parsing helper, checkpoint/verification hook, trace
  tagging without `stageCtx`.
- Script-stage dispatch: exposed as its own tool call.

## Acceptance criteria

1. Calling `run_playbook` on a fresh playbook creates a `PlaybookRun` and returns stage-1
   navigation instructions, without running an LLM stage loop internally.
2. Calling `run_playbook` again after a crash mid-stage (retry-safe tools) resumes at the
   same stage with the prior state intact.
3. Calling `run_playbook` after a crash mid-stage with a non-retry-safe tool set refuses to
   resume and says so, leaving the unsafe run untouched on disk.
4. Writing a stage's declared output file under its run/stage path, then reading the next
   stage's `CONTEXT.md`, triggers the same deviation/verification reporting #447 already
   ships (owner outbox, audit log, trace) without blocking navigation.
5. `schedule_playbook`-triggered runs are unaffected: they still run through
   `runWorkspacePlaybook`'s existing dedicated loop end to end.
6. `go build ./...` and the full test suite pass; no invariant is broken.
