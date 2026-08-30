# Design: scheduled playbook triggering in the unified loop

Issue: #452; builds directly on #450/#451 (merged, PR #469).

## Chosen approach

`fireSchedule`'s `run scheduledPlaybookRunner` parameter switches from `RunPlaybook` to a new
`NavigateScheduledPlaybook`, at the two top-level production call sites
(`catchUpSchedulesAt`/`dispatchDueSchedulesAt` inside `runScheduleDispatcher`/
`dispatchDueSchedules`). Everything else in the scheduler (`classifySchedule`,
`dispatchDueSchedulesAt`'s slot-claiming, `claimIterationRetry`, `alertScheduleHealth`,
`finishRoutine`) is unchanged — they already take the runner as an injected dependency, so
existing tests that inject their own fake runner are unaffected.

`NavigateScheduledPlaybook` fires `scheduledPlaybookInstruction(name)` through
`respondForScheduledPlaybook` (an internal seam wrapping `core.RespondForContext(ctx,
sessionID, instruction, "schedule", obs, false)`, same pattern as the existing
`runPlaybookStageLoop` seam). Mino's normal loop then calls `run_playbook` and does the real
work, the same as a chat-triggered navigated run. After the turn returns, `
NavigateScheduledPlaybook` reads the playbook's newest run (`latestPlaybookRun`) to build the
`*PlaybookResult` the scheduler's existing health-tracking expects.

## Interfaces

- `NavigateScheduledPlaybook(ctx, core, name, request, sessionID, obs) (*PlaybookResult,
  error)` — playbook.go. Matches `scheduledPlaybookRunner`'s existing signature exactly.
- `respondForScheduledPlaybook = func(core, ctx, sessionID, instruction, obs) *LoopResult` —
  playbook.go, a package var seam for tests.
- `scheduledPlaybookInstruction(name string) string` — the synthetic message.
- `session.go`'s `buildSystem`: two new conditions appending `playbookRails` — `source ==
  "schedule"`, or `sessionNav(s.sessionID)` has an active pointer.
- `app.go`'s `RespondForContext`: `AddExchange` call gated on `source != "schedule"`.

## Config surface

None.

## Failure behaviour

- **Turn ends with no run on disk at all** (model never called `run_playbook`, or the
  playbook failed to load): `PlaybookResult.Status = "failed"`, reply prefixed to say so —
  same alerting path as any other scheduled failure.
- **Turn ends with the run `"running"`** (iteration cap, provider error mid-turn, or the model
  simply stopped early): treated as `"iteration_limit"` — the scheduler's existing one-time
  retry fires, then the normal failure-alert path if the retry also doesn't finish it.
- **Turn ends with the run `"interrupted"`** (owner cancelled via `cancel_run` mid-fire):
  `PlaybookResult.Status = "interrupted"`, which `alertScheduleHealth` already treats as
  neither success nor failure — no alert, no streak change, same as a cancelled run today.
- **Provider timeout/malformed response inside the turn**: unchanged — `RunLoopContext`'s
  existing per-provider failure handling applies; the outer effect is just "the run stayed
  non-terminal," folded into the `iteration_limit` bucket above.

## Invariant check

- **Model agnosticism**: held — no provider-specific code; `RespondForContext` is the same
  entry point every provider already goes through.
- **Loop termination**: held — bounded by the existing `hardIterationCeiling`, unchanged.
- **Context is managed, never assumed**: held — scheduled fires explicitly skip `AddExchange`
  so their own session's context never grows unbounded across fires (this was the actual risk
  identified during grilling: reusing the normal chat-persistence path unmodified would have
  billed and re-read a growing tool-call trail on every future fire, forever).
- **Guardrails are not optional**: held — `playbookWriteGuard`'s session-nav/context-tag
  authorization is unchanged; a scheduled turn's writes go through the same `run_playbook` →
  `navigatePlaybookRun` → `setSessionNav` path as a chat turn.
- **Failure is explicit**: held — every branch in `NavigateScheduledPlaybook` sets an explicit
  `result.Status`.
- **State stays local and inspectable**: held — no new persisted format; the run's existing
  `state.json` is the source of truth for the mapped status.
- **Single binary, no framework**: held — no new dependency.

## Known limitations (carried to the ship note)

1. A scheduled fire that needs materially more than 60 total tool-call iterations to complete
   a multi-stage playbook (previously ~50 per stage under the dedicated loop, so a 3-stage
   playbook could use up to ~150) now shares one 60-iteration budget for the whole run per
   fire. The existing one-time retry gives a second 60-iteration attempt (which resumes
   in-progress stages, not from scratch), but a playbook that genuinely needs more than two
   fires' worth of iterations to finish will alert as failing even though each individual
   stage is making real progress. Not addressed here; flagged for whoever tunes
   iteration-budget policy next (touches #444's retry-policy scope).
2. Live crash-recovery and two-provider parity verification deferred to post-deploy testing,
   per the owner's direction, same as #450/#451.

## Files to touch

`playbook.go`, `app.go`, `session.go`, `playbook_schedule_navigate_test.go` (new).
