# Ship note: #450 / #451

Chat-triggered playbook runs (`run_playbook`) no longer drive a dedicated stage loop through
an entire playbook in one call. Each call now advances a run by one mechanical step: verify
whatever stage is currently in progress (the same output-existence and `## Success` outcome
checks as before), drive any zero-inference script-backed stage straight through with no
model involvement, and hand back the next stage's contract for Mino to act on with its own
`read_file`/`write_file` calls — call `run_playbook` again to advance.

Resume safety is unchanged in substance: a stage whose declared tools are all read-only or
`write_file` resumes automatically after a crash; a stage with any other tool is never
auto-resumed, and the caller is now told explicitly that an abandoned run exists rather than
silently getting a fresh one. `cancel_run` gained a fallback (`interruptRunOnDisk`) for
navigation-mode runs that have no live call to cancel between steps — it marks the run
interrupted on disk so the next `run_playbook` call for it halts.

`schedule_playbook` and the scheduler are unaffected: they still run every stage of a
playbook through the existing dedicated loop in one call, sharing the same `state.json` and
resume rules as chat's navigation path. This split is intentional and temporary — the
scheduler's own move into the unified-loop navigation model is a separate, later ticket
(#452).

## Config additions

None.

## Docs touched

- `docs/playbooks-design.md`: describes the navigation-vs-dedicated-loop split, resume-safety
  rule, and that scheduled and chat-triggered runs share state despite executing differently.
- `README.md`: one line noting `run_playbook`'s per-stage advance behavior from chat.
- `CHANGELOG.md`: entry above.

## Migration notes

None for playbook authors — stage contracts (`CONTEXT.md`, `## Tools`, `## Outputs`) are
unchanged; only how chat drives them changed. Anything that scripted around `run_playbook`
returning a fully-finished playbook result in one call (rather than one stage's worth of
progress) needs to call it repeatedly until the reply reports `complete`.

## Known limitations

1. The undeclared-`write_file`-target deviation check (`stageDeviationFlags`) does not apply
   to navigation-mode stages — it needs a per-attempt tool-call list navigation doesn't
   capture. The contract-verification flag (output existence, `## Success` outcomes) still
   applies in full. The dedicated loop (scheduler) keeps both checks unchanged.
2. `NavigatePlaybookRun`'s run-lock (#309 self-update deferral) covers only each individual
   `run_playbook` call, not the side-effecting work Mino does between calls while navigating
   a stage — a real gap versus the dedicated loop's whole-run lock, left for a later ticket.
3. Live two-provider parity and an actual VPS crash/restart drill for the resumability claim
   were not performed this session — deferred to post-deploy testing per the owner's
   direction. The full local test suite, `go vet`, and `go build` all pass (see the
   verification report), and unsafe-resume, retry-safe-resume, deviation-reporting, and
   cancellation-fallback behavior are each covered by a forced-failure-path test.

Release/tag/deployment intentionally not performed. This change is for the batch release
after the remaining approved Wayfinder issues are merged, per the owner's instruction not to
release yet.
