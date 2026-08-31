# Intake: playbook navigation run-continuity bug (hotfix)

Issue: #475. Live incident, 2026-08-31. Entered directly at stage 03 (cause already root-caused
against production evidence within the hour) per the stage-entry-guide's "bug with a known
cause enters at stage 03" shortcut — this note records the cause for the record.

## Problem

`morning-briefing` sent the owner the same Telegram brief 5 times; `threads-tribal-battle`
abandoned its own just-published run and would have reposted had Mino not manually caught it.
Both are scheduled playbooks whose final stage uses a side-effecting (non-retry-safe) tool.

## Root cause

`navigatePlaybookRun` re-derived run resumability via `loadOrCreatePlaybookRun` →
`latestResumablePlaybookRun` on every single call — including calls where the *same session*
was simply continuing its own just-started run to verify a side-effecting stage it had just
finished. That check cannot distinguish "the same session verifying its own in-progress work"
from "resuming a different process's run after a crash." It always chose the crash
interpretation, judged the stage unsafe to resume, and spawned a fresh run that restarted the
whole pipeline — including the side effect.

## Decision

Use the existing `sessionNav` pointer (already tracking exactly which run a session is
authorized to touch, built for #450/#451) as the authority for "is this a continuation."
The crash-safety resumability gate now only runs when there is no in-memory pointer for this
session — a genuinely fresh entry point (first call of a session, or a process restart, which
wipes `sessionNav`).

## Immediate mitigation

`schedules.json` renamed to `schedules.json.paused-v370bug` on the VPS the moment this was
reported, backup at `schedules.json.backup-pre-pause`. Restored after this fix deploys and is
re-verified live.

## Acceptance criteria

1. A session advancing its own run past a `BehaviorMutate`-tool stage it just finished
   continues the same run — never spawns a new one.
2. A genuinely fresh entry point (no active `sessionNav` pointer) still gets the full
   crash-safety resumability check, unchanged.
3. A regression test reproduces the incident shape and fails without the fix, passes with it.
4. Full test suite passes; `go vet` and `go build` clean.
