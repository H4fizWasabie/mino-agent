# Implementation manifest: #452

Branch: `fix/issue-452-scheduled-navigate`

## Files changed

- `session.go`: `buildSystem` now appends `playbookRails` for `source == "schedule"`, and for
  any turn whose session has an active navigation pointer (`sessionNav`), regardless of
  source. Fixes a gap discovered mid-design: `playbookRails` was previously exclusive to
  `BuildPlaybookSystem` (the dedicated loop), so neither scheduled fires nor #450/#451's
  chat-navigated stages ever got that discipline block.
- `app.go`: `RespondForContext` skips `conversation.Session.AddExchange(...)` when `source ==
  "schedule"`, so a schedule's pseudo-session never accumulates chat history.
- `playbook.go`:
  - Added `respondForScheduledPlaybook` (seam var wrapping `core.RespondForContext`),
    `scheduledPlaybookInstruction`, and `NavigateScheduledPlaybook`.
  - `runScheduleDispatcher`/`dispatchDueSchedules`: pass `NavigateScheduledPlaybook` instead
    of `RunPlaybook` to `catchUpSchedulesAt`/`dispatchDueSchedulesAt`.
- `playbook_schedule_navigate_test.go` (new): `playbookRails` injection for both new
  conditions, `AddExchange` suppression (end-to-end via a scripted HTTP provider, same
  pattern as `app_test.go`), and `NavigateScheduledPlaybook`'s run-status mapping
  (complete/failed/interrupted/running→iteration_limit) plus the no-run-found failure case.

## New interfaces

See `../02-design/output/issue-452-design.md`'s Interfaces section — unchanged from design.

## New config keys

None.

## Tests added

`TestBuildSystemInjectsPlaybookRailsForScheduleSource`,
`TestBuildSystemInjectsPlaybookRailsWhenNavigating`,
`TestRespondForContextSkipsAddExchangeForSchedule`,
`TestNavigateScheduledPlaybookMapsRunStatus` (4 subtests: complete/failed/interrupted/running),
`TestNavigateScheduledPlaybookNoRunIsFailure`.

## Build and test results

- `go build ./...`: PASS.
- Focused new tests (9 total): PASS.
- Existing scheduler + playbook tests (104): PASS, unaffected by the call-site change.
- Full suite: see `../04-verify/output/issue-452-verification.md`.

## Deferred / known limitations

Carried from the design note: a scheduled fire sharing one 60-iteration `hardIterationCeiling`
budget for an entire multi-stage playbook (vs. ~50 per stage under the old dedicated loop) may
need more than the existing one automatic retry for genuinely long playbooks — not addressed
here. Live crash-recovery and two-provider parity verification deferred to post-deploy testing.
