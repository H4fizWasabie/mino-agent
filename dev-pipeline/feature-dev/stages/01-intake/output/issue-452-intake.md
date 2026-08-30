# Intake: scheduled playbook triggering in the unified loop

Issue: #452. Grilled 2026-08-31, agreed in full.

## Problem

`schedule_playbook`/the scheduler still fire a playbook through `RunPlaybook`'s dedicated
stage loop directly (`runScheduleDispatcher` → `catchUpSchedulesAt`/`dispatchDueSchedulesAt`
→ `RunPlaybook`), bypassing the normal loop entirely — no chat message, no model turn outside
the dedicated loop. #450/#451 moved chat's entry point (`run_playbook`) to navigate one stage
per call instead; the scheduler was deliberately left behind pending this ticket.

## Decision

- The scheduler fires a synthetic instruction through Mino's own normal loop
  (`core.RespondForContext`) with the existing deterministic `"scheduled-<name>"` session,
  and a new `source == "schedule"` value — the same entry point Telegram/dashboard already
  use for a real chat turn. Mino's own loop then calls `run_playbook` and does the real stage
  work in between, the same way a chat-triggered run does now.
- No iteration-budget change: `hardIterationCeiling` (60) is a fixed cap in `RunLoopContext`
  regardless of `maxIter` — a fire that can't finish inside it already gets the existing
  one-time `scheduleRetryDelay` retry via `claimIterationRetry`.
- Health-tracking (`alertScheduleHealth`, `finishRoutine`) reads the run's own `state.json`
  after the turn ends, not the turn's `LoopResult` — only the run record knows whether the
  playbook actually finished.
- A fire that ends with the run still `"running"` (not a terminal status) is treated the same
  as `"iteration_limit"` for health-tracking, reusing the existing one-retry-then-alert
  contract with no new mechanism.
- Scheduled fires never call `AddExchange` — their pseudo-session accumulates no chat history,
  matching today's zero-persistence behavior exactly (today's dedicated-loop path never wrote
  to it either).
- **Discovered mid-design, applied here**: `buildSystem` (the normal-turn system prompt)
  never included `playbookRails` — that discipline block was exclusive to
  `BuildPlaybookSystem`, used only by the dedicated loop. Without it, an autonomous scheduled
  fire (no owner to notice a run that quietly stops early) or a chat-navigated stage could
  narrate and hand back unfinished work instead of finishing. Fixed for both: `source ==
  "schedule"` always gets the rails; any turn with an active navigation pointer
  (`sessionNav`) gets them too, regardless of source.

## Non-goals

- Redesigning `#444`/`#445`/`#446`/`#453` (separate tickets; `#453` explicitly depends on this
  one being settled first).
- Changing `schedules.json`'s schema, `classifySchedule`, or the day-of-week/catch-up logic.
- A live crash-recovery or two-provider parity drill (post-deploy, per owner direction).

## Surfaces touched

`playbook.go` (new `NavigateScheduledPlaybook`/seam/instruction, scheduler call sites),
`app.go` (`AddExchange` skip), `session.go` (`playbookRails` injection).

## Acceptance criteria

1. `runScheduleDispatcher`/`dispatchDueSchedules` fire playbooks through
   `NavigateScheduledPlaybook`, which drives `core.RespondForContext` with a synthetic
   instruction and `source == "schedule"`.
2. The resulting `PlaybookResult.Status` is derived from the run's on-disk state
   (`complete`/`failed`/`interrupted`/`iteration_limit` for any non-terminal outcome), not the
   turn's `LoopResult.Status`.
3. A schedule-source turn never appends to that session's chat history.
4. `source == "schedule"` and any chat turn with an active navigation pointer both get
   `playbookRails` in their system prompt; an ordinary chat turn with no active navigation
   does not.
5. Existing scheduler tests (`classifySchedule`, `dispatchDueSchedulesAt`,
   `catchUpSchedulesAt` with their own injected test runners) are unaffected — only the two
   top-level production call sites changed which runner they pass.
