# Verification: #452

Branch: `fix/issue-452-scheduled-navigate`

## Test results

- Full `GOCACHE=/tmp/mino-gocache go test ./... -count=1 -timeout=300s`: PASS (265.101s).
- `go vet ./...`: PASS.
- `go build ./...`: PASS.
- `git diff --check`: PASS.
- New tests (9): PASS — `playbookRails` injection (schedule source + active nav pointer),
  `AddExchange` suppression for schedule-source turns (end-to-end via a scripted HTTP
  provider), `NavigateScheduledPlaybook` run-status mapping (4 subtests), no-run-found
  failure case.
- Existing scheduler/playbook tests (104): PASS, unaffected — `classifySchedule`,
  `dispatchDueSchedulesAt`, `catchUpSchedulesAt` all take their runner as an injected
  dependency and keep using their own fakes in tests; only the two production call sites
  (`runScheduleDispatcher`, `dispatchDueSchedules`) changed which runner they pass.

## Acceptance criteria (from intake) — observed behaviour

1. **Scheduler fires through `NavigateScheduledPlaybook`.** Confirmed by inspection: both
   `runScheduleDispatcher` and `dispatchDueSchedules` now pass `NavigateScheduledPlaybook` to
   `catchUpSchedulesAt`/`dispatchDueSchedulesAt`; `NavigateScheduledPlaybook` calls
   `respondForScheduledPlaybook` → `core.RespondForContext(..., "schedule", ...)`.
2. **`PlaybookResult.Status` derived from the run's on-disk state, not `LoopResult.Status`.**
   Observed in `TestNavigateScheduledPlaybookMapsRunStatus`: the seam always returns
   `LoopResult{Status: "complete"}` regardless of subtest, yet the mapped
   `PlaybookResult.Status` tracks the run's actual on-disk status in every case
   (complete/failed/interrupted/running→iteration_limit) — proving the mapping reads the run
   record, not the loop result.
3. **No history growth for schedule-source turns.** Observed in
   `TestRespondForContextSkipsAddExchangeForSchedule`: an end-to-end `RespondForContext` call
   with `source="schedule"` against a real (scripted) HTTP provider leaves the session's
   history empty, while an identical call with `source="telegram"` populates it — proving the
   skip is scoped to the new source value only.
4. **`playbookRails` injected for schedule source and active navigation.** Observed in
   `TestBuildSystemInjectsPlaybookRailsForScheduleSource` and
   `TestBuildSystemInjectsPlaybookRailsWhenNavigating`: the rails marker is present for
   `source="schedule"` unconditionally, absent for an ordinary telegram turn with no active
   nav pointer, and present once `setSessionNav` marks that session as navigating.
5. **Existing scheduler tests unaffected.** Full suite green; `dispatchDueSchedulesAt`/
   `catchUpSchedulesAt`'s own test suite (which injects fake runners) required no changes.

## Invariants — held / evidence

| Invariant | Verdict | Evidence |
|---|---|---|
| Model agnosticism | Held | `RespondForContext` is the existing, already-agnostic entry point; no provider-specific code added. |
| Loop termination | Held (unchanged) | `hardIterationCeiling` bound is untouched; the design note documents that a fire sharing this cap for a whole multi-stage run is a known capacity limitation, not an unbounded-loop risk. |
| Context is managed, never assumed | Held | The `AddExchange` skip is exactly what prevents a scheduled session's context from growing without bound across recurring fires — proven by `TestRespondForContextSkipsAddExchangeForSchedule`. |
| Guardrails are not optional | Held (unaffected) | `playbookWriteGuard` is untouched; scheduled navigation reaches it through the same `run_playbook` → `navigatePlaybookRun` → `setSessionNav` path a chat turn does. |
| Failure is explicit | Held | Every branch in `NavigateScheduledPlaybook` sets an explicit `result.Status`; proven for all four run statuses plus the no-run case. |
| State stays local and inspectable | Held | No new persisted format; `state.json` remains the single source of truth read by the new mapping. |
| Single binary, no framework | Held | No new dependency; test provider uses stdlib `net/http/httptest`. |

## Failure paths forced

- Run still `"running"` after the turn → mapped to `"iteration_limit"` (triggers the existing
  one-time scheduler retry).
- Run `"failed"`/`"interrupted"` after the turn → mapped through unchanged, verified against
  `alertScheduleHealth`'s existing branching (by inspection: only `"failed"`/`"iteration_limit"`
  alert; `"interrupted"`/`"complete"` do not).
- No run created at all → explicit `"failed"` with a distinguishing reply prefix.

## Provider parity

Not performed live (no two real providers available in this session). The `AddExchange`
suppression and `playbookRails` tests do exercise `RespondForContext` end-to-end against a
real HTTP request/response cycle (via `httptest`), which is a stronger check than a pure unit
test, but it is a single scripted provider, not two live ones. Per the owner's direction, live
provider parity and an actual scheduled-fire drill happen post-deploy.

## Open concerns (carried to the ship note)

1. A scheduled fire sharing one 60-iteration budget for an entire multi-stage playbook (vs.
   ~50 per stage under the old dedicated loop) may exhaust both the fire and its one automatic
   retry on a genuinely long playbook, alerting as failed even with real per-stage progress.
2. Live crash-recovery and two-provider parity verification deferred to post-deploy testing.
