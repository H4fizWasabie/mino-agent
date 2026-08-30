# Intake: split retry policy by idempotency

Issue: #444. Grilled 2026-08-31 — resolved without implementation.

## Problem (as posed)

"Only get to try once, fail loudly" exists because retry-until-green caused a real duplicate
Threads post. The ticket asked for a replacement that retries bounded for
idempotent/observe-only failures while keeping fail-loud for non-idempotent, side-effecting
actions, and asked whether `stageRetrySafe`'s existing `BehaviorObserve` classification
(built for playbook-resume) should extend to turn-level retry generally now that playbooks
run inside the normal loop.

## Finding

This is already resolved as a side effect of #450/#451/#452, which shipped after this ticket
was filed:

- `stageRetrySafe` (`playbook_workspace.go`) already IS the idempotent-vs-side-effecting
  split: a stage whose declared tools are all `BehaviorObserve` or `write_file` is
  retry-safe; any other tool makes a stage fail loud and never auto-resume.
- Both playbook entry points inherit this unchanged: `navigatePlaybookRun` (chat, #450/#451)
  refuses to resume an unsafe stage and starts a fresh run, naming the abandoned one;
  `NavigateScheduledPlaybook`'s scheduled-fire retry (`claimIterationRetry`/
  `scheduleRetryDelay`, #452) only fires for a non-terminal run outcome, which by
  construction never re-triggers a side effect `stageRetrySafe` would have refused.
- Ordinary provider-call retry/failover (priority, sticky pinning, circuit breaker in
  `provider_manager.go`) is a separate, already-mature mechanism unrelated to this ticket's
  concern (duplicate side effects from resuming a crashed action), not touched here.

## Decision

Extending this classification further, to ordinary (non-playbook) tool-call failures within
a single chat turn, was considered and rejected: no incident motivates it (unlike the
duplicate-Threads-post evidence that justified `stageRetrySafe` itself), and today's
model-driven retry judgment (prompted in `buildSystem`'s static rules: "retry with corrected
arguments... never retry the same dead action to the cap") already covers ordinary tool
failures without a harness-level mechanism. Building one now would be a speculative
abstraction with nothing asking for it.

## Non-goals

- A general turn-level auto-retry mechanism for `BehaviorObserve` tools outside playbook
  stage resumption — explicitly rejected pending a real incident.
- Changing `provider_manager.go`'s retry/failover/circuit-breaker logic.

## Outcome

No code change. Issue closed with this design note as the record of why.
