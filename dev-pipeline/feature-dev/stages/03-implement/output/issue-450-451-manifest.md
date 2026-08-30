# Implementation manifest: #450 / #451

Branch: `fix/issue-450-451-checkpoint`

## Files changed

- `playbook_nav.go` (new): per-session navigation pointer (`setSessionNav`/`clearSessionNav`/
  `sessionNav`), the authorization seam for chat-navigated writes.
- `playbook_workspace.go`:
  - `playbookWriteGuard`: falls back to the session nav pointer when no context tag is set.
  - Extracted `validatePlaybookPreRun` and `bindOrRefreshRunContract` out of
    `runWorkspacePlaybook` (no behavior change to the dedicated loop, just DRY between it and
    the new entry point).
  - Added `abandonedUnsafeRun` (mirrors `latestResumablePlaybookRun`'s traversal).
  - Added `interruptRunOnDisk` (state.json fallback for cancelling a run with no live
    registry entry).
  - Added `navigatePlaybookRun`: the new chat-mode advance-by-one-step entry point.
- `playbook.go`:
  - Added `NavigatePlaybookRun` (run-lock + memory-recording + trace wrapper around
    `navigatePlaybookRun`, mirroring `RunPlaybook`'s existing wrapper).
  - `makeRunPlaybookTool`: `ContextFn` now calls `runPlaybookWithResponsibility(...,
    NavigatePlaybookRun, ...)` instead of `RunPlaybook`; tool description updated to describe
    the advance-one-step contract.
  - `makeCancelRunTool(home string)`: added the `interruptRunOnDisk` fallback when
    `cancelRun` finds no live run.
- `app.go`: updated the one `makeCancelRunTool()` call site to pass `s.Home`.
- `playbook_test.go`: rewrote `TestRunPlaybookToolNoOutboxWhenClientConnected` for the
  two-call navigation flow (its old form mocked the now-unused `runPlaybookStageLoop` path).
- `playbook_navigate_test.go` (new): script-stage auto-drive, unsafe-resume refusal,
  retry-safe resume/verify/advance, deviation reporting on failed verification,
  `interruptRunOnDisk` + terminal-cancellation behavior, and the write-guard nav fallback.

## New interfaces

See `../02-design/output/issue-450-451-design.md`'s Interfaces section — unchanged from
design, no deviation during implementation.

## New config keys

None.

## Tests added

`TestNavigatePlaybookRunScriptStageAutoDrives`,
`TestNavigatePlaybookRunRefusesUnsafeResume`,
`TestNavigatePlaybookRunRetrySafeResumeVerifiesAndAdvances`,
`TestNavigatePlaybookRunReportsDeviationOnFailedVerification`,
`TestInterruptRunOnDiskStopsTheCancelledRunPermanently`,
`TestPlaybookWriteGuardSessionNavFallback`, plus the rewritten
`TestRunPlaybookToolNoOutboxWhenClientConnected`.

## Build and test results

- `go build ./...`: PASS.
- `go vet ./...`: PASS.
- Focused navigation tests: PASS (7/7).
- Full suite: see `../04-verify/output/issue-450-451-verification.md`.

## Deferred / known limitations

Carried verbatim from the design note's "Known limitations" section: the undeclared
`write_file`-target deviation check does not apply in navigation mode; `NavigatePlaybookRun`'s
run-lock does not cover the self-update-deferral window between calls the way `RunPlaybook`'s
does; live two-provider and VPS crash-recovery verification were not performed this session.
