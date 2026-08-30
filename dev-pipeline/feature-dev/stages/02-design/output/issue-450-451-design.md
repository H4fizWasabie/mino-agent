# Design: checkpoint tracking and output verification without a dedicated stage loop

Issues: #450, #451; coordinated with #442's parent map and #447/#449, already shipped.

## Chosen approach

`run_playbook` stops running a dedicated per-stage LLM loop for chat-triggered runs. Instead
it advances a `PlaybookRun` by one mechanical step per call:

1. If the current stage is `"running"` (a previous call handed it to Mino), verify its
   declared outputs — the advance signal Mino gives just by calling `run_playbook` again,
   since there is no loop-attempt exit left to hang the check on.
2. Drive any zero-inference script-backed stage (#304) straight through with no model
   involvement, then keep advancing.
3. Hand back the next LLM stage's contract (same prompt `buildWorkspaceStagePrompt` already
   renders) for Mino to act on with its own `read_file`/`write_file` calls, and return.

`schedule_playbook` is unaffected: the scheduler calls `RunPlaybook`/`runWorkspacePlaybook`
directly (`runScheduleDispatcher` → `catchUpSchedulesAt(core, now, RunPlaybook)`), never
through the `run_playbook` tool, so it keeps the dedicated stage loop unchanged until #452.

## Interfaces

- `navigatePlaybookRun(ctx, core, name, request, sessionID) (*PlaybookResult, error)` —
  playbook_workspace.go. The chat-mode advance-by-one-step function.
- `NavigatePlaybookRun(ctx, core, name, request, sessionID, obs) (*PlaybookResult, error)` —
  playbook.go. Wraps `navigatePlaybookRun` with the same run-lock, memory-artifact recording,
  and trace logging `RunPlaybook` already does for the dedicated loop. Passed to
  `runPlaybookWithResponsibility` in place of `RunPlaybook` inside `makeRunPlaybookTool`.
- `validatePlaybookPreRun(core, pb) error` and
  `bindOrRefreshRunContract(pb, registry, run, request, sessionID, now) (*PlaybookRun, error)` —
  extracted from `runWorkspacePlaybook` so both entry points share the exact same pre-run
  validation and #310 franken-resume contract-hash check.
- `abandonedUnsafeRun(pb, registry) (*PlaybookRun, bool)` — mirrors
  `latestResumablePlaybookRun`'s traversal, returning the newest run skipped for resume
  safety so navigation can say a run was deliberately left untouched instead of silently
  starting fresh with no explanation.
- `playbookNavPointer{Playbook, RunID string}` and `setSessionNav`/`clearSessionNav`/
  `sessionNav(sessionID)` — playbook_nav.go. In-memory, mutex-guarded per-session pointer
  recording which run a session is currently authorized to touch, filling the gap left by
  the stageCtx-injected `traceTagKey` that the dedicated loop no longer sets for chat runs.
  Same pattern as `run_registry.go`'s existing cancellation map.
- `interruptRunOnDisk(home, id, reason) bool` — playbook_workspace.go. Marks a run
  interrupted directly on `state.json` when it has no live entry in `run_registry.go` (a
  navigation-mode run has no long-lived call to cancel between `run_playbook` calls).
  `cancel_run` falls back to this when `cancelRun(id)` reports no live run.

## Config surface

None. No new config key, provider adapter, or dependency (matches #449's precedent).

## Failure behaviour

- **Crash mid-stage, retry-safe tools**: the run's `state.json` still shows the stage
  `"running"`. The next `run_playbook` call re-verifies via the existing mtime-based fallback
  in `verifyWorkspaceStageOutputs` (already tool-agnostic since #460) and either completes or
  retries in place, up to `maxStageAttempts`.
- **Crash mid-stage, non-retry-safe tools**: `latestResumablePlaybookRun` already refuses to
  resume it (unchanged); `navigatePlaybookRun` starts a fresh run and names the abandoned run
  ID in its reply via `abandonedUnsafeRun`.
- **Cancellation**: `cancel_run` sets the interrupt reason and either cancels a live
  dedicated-loop call (unchanged) or, for a navigation-mode run with nothing live to cancel,
  marks `state.json` interrupted via `interruptRunOnDisk`. An interrupted run is terminal —
  never resumed, matching today's cancellation behavior on the dedicated loop — the next
  `run_playbook` call for that playbook starts a fresh run.
- **Provider/model timeout or malformed response**: unaffected — those happen inside Mino's
  normal loop turn, outside `navigatePlaybookRun` entirely, and are handled by the loop's
  existing failure paths.

## Invariant check

- **Model agnosticism**: held. No provider-specific code; navigation is plain Go
  orchestration around existing tool-agnostic verification.
- **Loop termination**: held. `navigatePlaybookRun`'s `for` loop only continues on a
  `nextPlaybookStage` transition (bounded by `len(pb.Stages)`) or a completed script stage;
  every other branch returns.
- **Context is managed, never assumed**: unaffected — navigation does not read stage
  history into context itself; Mino's own turns do, under the same context management as
  any other file it reads.
- **Guardrails are not optional**: `playbookWriteGuard` still gates every write into a run
  directory; the session-nav fallback is additive, not a bypass — writes still require
  either the old context-tag path or a matching nav pointer.
- **Failure is explicit**: held (see Failure behaviour above); every branch in
  `navigatePlaybookRun` sets an explicit `result.Status` and saves state before returning.
- **State stays local and inspectable**: held. `PlaybookRun`/`state.json` schema unchanged;
  the new nav pointer is in-memory only and reconstructable from `state.json` on restart
  (the harness never trusts it as the source of truth, only as an authorization cache).
- **Single binary, no framework**: held. No new dependency.

## Known limitations (carried to the ship note)

1. `stageDeviationFlags`'s "undeclared `write_file` target" check needs a captured
   per-attempt tool-call list, which navigation mode does not have (Mino acts across many
   ordinary turns, not one bounded loop). Only the contract-verification flag applies in
   navigation mode; the write_file-target flag still applies in full to the dedicated loop.
2. `RunPlaybook`'s #309 self-update-deferral flock covers a whole run's duration.
   `NavigatePlaybookRun`'s flock only covers each individual bookkeeping call — it does not
   defer the self-updater during the side-effecting work Mino does between `run_playbook`
   calls while navigating a stage. Left for a later map ticket.
3. Verification against two live providers, and a live VPS crash-recovery drill, were not
   possible in this session — carried into the stage 04 verification report as an explicit
   gap rather than claimed.

## Files to touch

`playbook_nav.go` (new), `playbook_workspace.go`, `playbook.go`, `app.go`,
`playbook_test.go`, `playbook_navigate_test.go` (new).
