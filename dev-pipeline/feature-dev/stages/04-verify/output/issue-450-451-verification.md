# Verification: #450 / #451

Branch: `fix/issue-450-451-checkpoint`

## Test results

- Full `GOCACHE=/tmp/mino-gocache go test ./... -count=1 -timeout=300s`: PASS (265.704s).
- `go vet ./...`: PASS.
- `go build ./...`: PASS.
- `git diff --check`: PASS.
- New navigation tests (7): PASS — script-stage auto-drive, unsafe-resume refusal,
  retry-safe resume/verify/advance, deviation reporting, `interruptRunOnDisk` +
  terminal-cancellation, write-guard session-nav fallback.

## Regression found and fixed during verification

The first full-suite run hung until the 10-minute default `go test` timeout killed the
binary: `TestManualRunSurvivesClientDisconnect` mocked `runPlaybookStageLoop` and waited on a
channel that function closes — a mechanism `navigatePlaybookRun` (now what `run_playbook`
calls) never invokes, so the channel never closed. This is exactly the #316 "detached
context" behavior the design's known-limitations section flagged as changed, not regressed:
a navigation-mode call is a short mechanical step with nothing long-lived to detach, so
"survives disconnect" now means "the call completes and reports to the outbox" rather than
"a background goroutine keeps a long loop alive after the caller hangs up." Rewrote the test
to cancel the ctx before the call instead of mid-flight and assert the same outbox-delivery
guarantee — see the rewritten test in `playbook_test.go`.

## Acceptance criteria (from intake) — observed behaviour

1. **`run_playbook` on a fresh playbook creates a run and returns stage-1 navigation, no
   internal LLM loop.** Observed in `TestNavigatePlaybookRunRetrySafeResumeVerifiesAndAdvances`
   and the rewritten `TestRunPlaybookToolNoOutboxWhenClientConnected`: the first call's reply
   contains the stage-1 contract text, no mock of `runPlaybookStageLoop`/`RunLoopContext` was
   invoked (there is none to invoke — `navigatePlaybookRun` never calls it).
2. **Resume after crash, retry-safe tools, resumes at the same stage.** Observed in
   `TestNavigatePlaybookRunRetrySafeResumeVerifiesAndAdvances`: a stage left `"running"` with
   no output written re-verifies, fails, and is handed back for another attempt; once the
   output is written, the next call completes it.
3. **Resume after crash, non-retry-safe tools, refuses and starts fresh.** Observed in
   `TestNavigatePlaybookRunRefusesUnsafeResume`: a `"running"` stage declaring a `BehaviorMutate`
   tool is never resumed; a new run starts and the reply names the abandoned run's ID; the
   abandoned run's `state.json` is verified untouched (`"status": "running"` unchanged).
4. **Writing declared output then advancing triggers #447's existing deviation/verification
   reporting.** Observed in `TestNavigatePlaybookRunReportsDeviationOnFailedVerification`: a
   stage that never produces its declared output fails after exhausting `maxStageAttempts`,
   and the owner outbox receives a deviation entry via the unchanged
   `reportStageDeviations`/`stageDeviationFlags` path.
5. **`schedule_playbook`-triggered runs are unaffected.** Confirmed by inspection:
   `runScheduleDispatcher`/`catchUpSchedulesAt` call `RunPlaybook` directly, never through the
   `run_playbook` tool — `RunPlaybook`/`runWorkspacePlaybook` and their existing tests
   (`playbook_script_test.go`, the rest of `playbook_test.go`) are unmodified and still pass.
6. **Build and full suite pass; no invariant broken.** See Test results above and Invariants
   below.

## Invariants — held / evidence

| Invariant | Verdict | Evidence |
|---|---|---|
| Model agnosticism | Held | No provider-specific code touched; navigation is orchestration around existing tool-agnostic verification. |
| Loop termination | Held | `navigatePlaybookRun`'s loop only continues on a stage transition bounded by `len(pb.Stages)`; every other branch returns explicitly. Forced by running a multi-stage script playbook to completion in tests. |
| Context is managed, never assumed | Held (unaffected) | Navigation does not read stage history into context; Mino's own turns do, unchanged. |
| Guardrails are not optional | Held | `playbookWriteGuard`'s session-nav fallback is additive — `TestPlaybookWriteGuardSessionNavFallback` proves a write is denied both with no nav pointer AND with a nav pointer for a *different* run. |
| Failure is explicit | Held | Every branch in `navigatePlaybookRun`/`interruptRunOnDisk` sets an explicit `result.Status`/on-disk status and persists before returning; forced via the unsafe-resume, deviation, and interrupt tests. |
| State stays local and inspectable | Held | `PlaybookRun`/`state.json` schema unchanged; the new nav pointer is in-memory only, reconstructable, never treated as ground truth. |
| Single binary, no framework | Held | No new dependency; `go build ./...` uses only already-accepted deps. |

## Failure paths forced

- Crash mid non-retry-safe stage → refused resume (test above).
- Crash mid retry-safe stage, output never written → retried in place, then fails after
  `maxStageAttempts` with a reported deviation.
- Cancellation with no live registry entry → `interruptRunOnDisk` fallback halts the run
  permanently; the next call starts fresh (matches existing terminal-cancellation semantics).
- Client disconnect (ctx already cancelled) → call still completes, outcome delivered via
  outbox instead of a dead connection.

## Provider parity

**Not performed this session.** This change is Go-level orchestration around the existing
tool-agnostic verification and state machinery — it introduces no provider-specific code
path — but the design's own bar (exercise the changed path against a running harness on at
least two providers) was not met locally. Per the owner's direction, live verification
(including an actual VPS crash/restart drill for #450's resumability claim) happens after
deploy, separately from this stage. Flagging honestly rather than claiming parity untested.

## Open concerns (carried to the ship note)

1. `stageDeviationFlags`'s undeclared-`write_file`-target check does not apply in navigation
   mode (needs a captured per-attempt call list navigation doesn't have).
2. `NavigatePlaybookRun`'s run-lock does not cover the self-update-deferral window between
   calls the way `RunPlaybook`'s does for the dedicated loop.
3. Live two-provider and VPS crash-recovery verification are deferred to post-deploy testing.
