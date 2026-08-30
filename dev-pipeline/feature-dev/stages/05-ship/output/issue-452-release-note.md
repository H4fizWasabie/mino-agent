# Ship note: #452

Scheduled playbook fires no longer call a dedicated executor directly. `NavigateScheduledPlaybook`
now sends a synthetic, self-contained instruction through Mino's own normal loop
(`core.RespondForContext`, `source == "schedule"`) — the same entry point a real chat message
uses — so Mino calls `run_playbook` and does the actual stage work itself, exactly like the
chat-triggered navigation #450/#451 shipped. `schedule_playbook`, `classifySchedule`, and the
scheduler's due/catch-up dispatch are otherwise unchanged; only the runner function they fire
into changed.

Two things were fixed as part of getting this right:

- **No history growth.** A scheduled fire's pseudo-session (`"scheduled-<name>"`) never calls
  `AddExchange`, so it doesn't accumulate a growing tool-call trail that would otherwise be
  re-read (and re-billed) on every future fire, forever.
- **Autonomous discipline.** `playbookRails` — the "finish it, don't hand back unfinished
  work, no narration" operating rules — used to be exclusive to the old dedicated loop's
  system prompt. Neither a scheduled fire nor a chat-navigated stage (#450/#451) ever got it.
  Both now do: every `source == "schedule"` turn, and any chat turn with an active navigation
  pointer, get the rails injected.

Health-tracking (`alertScheduleHealth`, `finishRoutine`) reads the run's own `state.json`
after the turn ends to decide the outcome, not the turn's `LoopResult` — a run still
`"running"` when the turn ends (iteration cap, provider hiccup, or the model just stopping) is
treated the same as `"iteration_limit"`, reusing the existing one-time-retry-then-alert
contract with no new mechanism.

## Config additions

None.

## Docs touched

- `docs/playbooks-design.md`: scheduling section rewritten for the synthetic-instruction
  model; the earlier "scheduled runs still use a dedicated loop" line from #450/#451's own
  doc update is corrected.
- `README.md`: one line updated to describe scheduled and chat-triggered runs as the same
  navigation mechanism.
- `CHANGELOG.md`: entry above.

## Migration notes

None for playbook authors. Anything that inspected `PlaybookResult.TokensIn`/`TokensOut` for
a scheduled run should note these are now 0 from `NavigateScheduledPlaybook` directly (the
actual model call happens inside `RespondForContext`'s `LoopResult`, which isn't separately
exposed to schedule-health callers) — `StagesRun` and `Outputs` are still populated from the
run's own completed-stage records, unchanged in meaning.

## Known limitations

1. A scheduled fire shares one 60-iteration `hardIterationCeiling` budget for the entire
   playbook run per fire (vs. up to ~50 per stage under the old dedicated loop). The existing
   one-time automatic retry gives a second attempt that resumes in-progress stages rather than
   restarting, but a playbook needing more than two fires' worth of iterations to finish will
   still alert as failing even with real per-stage progress. Left for whoever next tunes
   iteration-budget policy (touches #444's scope).
2. Live crash-recovery and two-provider parity verification were not performed this session —
   deferred to post-deploy testing per the owner's direction, same as #450/#451. The full
   local test suite, `go vet`, and `go build` all pass, and the `AddExchange`/`playbookRails`
   behavior is verified end-to-end against a real (scripted) HTTP request/response cycle, not
   just in isolation.

Release/tag/deployment intentionally not performed, per the owner's instruction not to release
until the remaining approved Wayfinder issues are merged.
