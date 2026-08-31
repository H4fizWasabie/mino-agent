# Implementation manifest: #475 (hotfix)

Branch: `fix/issue-475-navigation-run-continuity`

## Files changed

- `playbook_workspace.go`:
  - Added `loadPlaybookRunByID(pb, id)` — reads one run's `state.json` directly by ID, no
    resumability judgment.
  - `navigatePlaybookRun`: checks `sessionNav(sessionID)` first; if it points at this
    playbook and the referenced run is still `"running"`/`"failed"`, continues that exact run
    directly, skipping `loadOrCreatePlaybookRun`'s crash-safety gate entirely. Falls through
    to the original logic (including the `abandonedUnsafeRun` messaging) only when there is no
    active pointer for this session.
  - Corrected a stale comment claiming this function was "chat-only" (superseded by #452).
- `playbook_navigate_test.go`: added `writeSideEffectingStagePlaybook` helper and
  `TestNavigatePlaybookRunContinuesSameRunAcrossSideEffectingStage`, which reproduces the
  incident shape (two-stage playbook, stage 2 declares a `BehaviorMutate` tool) — confirmed to
  fail without the fix (new run spawned at stage 1) and pass with it (same run completes).

## Build and test results

- `go build ./...`: PASS.
- Regression test: confirmed FAILS on the pre-fix code (verified by temporarily reverting
  `playbook_workspace.go` via `git stash`), PASSES with the fix.
- Full suite (`go test ./... -count=1`, 264.000s): PASS.
- `go vet ./...`: PASS.
- `git diff --check`: PASS.

## Deferred

Whether to prune the ~40 accumulated `morning-briefing` run directories from the incident is
left to the owner — evidence value vs. disk cleanliness, not addressed in this hotfix.
