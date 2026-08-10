# Mino Verification Gap — Wayfinder Map

## Destination

Mino verifies task completion against actual state, not just tool call success. When Mino says "done," the change is actually in place — validated by evidence, not assumption.

Measurable: the next time Mino mutates runtime state (schedule, cron, config, playbook), it reads back or lists the result before declaring done.

## Notes

- **Language:** Go (harness) + English (SOUL.md, skills)
- **This is a reasoning gap, not a tool gap.** Mino has bash, read_file, write_file — all the capabilities needed to verify. The model just doesn't think to verify.
- **Real examples from today:**
  1. Surgery schedule: rm -rf playbook dir ✅, forgot schedules.json ❌
  2. Cron safety net: wrote script ✅, ran once ✅, forgot crontab ❌
  3. Playbook delivery: patched 7 files ✅, created safety net ✅, but delivery only happened because safety net caught it after

## Decisions so far

- [VFY-001 — Root cause](tickets/vfy-001-root-cause.md) — SOUL.md says "silently verify" and trusts "tool evidence" (which is just "ok"). Mino thinks, doesn't check.
- [VFY-002 — Harness fix](tickets/vfy-002-harness.md) — Enrich tool responses: cancel_schedule returns what was removed, bash cron includes verification hint, new system_check tool for state summary. Fix SOUL.md: "silently" → "with tools."
- [VFY-003 — Model fix](tickets/vfy-003-model.md) — Not the primary approach. Single SOUL.md word fix is the only model-side change.
- [VFY-004 — Validation](tickets/vfy-004-validation.md) — Regression test with a test playbook: remove it, check traces show verification calls before reply.

## Frontier (open tickets)

- [VFY-001 — Root cause analysis](tickets/vfy-001-root-cause.md)
- [VFY-002 — Harness-level interventions](tickets/vfy-002-harness.md)
- [VFY-003 — Model-level interventions](tickets/vfy-003-model.md)
- [VFY-004 — Validation approach](tickets/vfy-004-validation.md)

## Out of scope

- Fixing the specific incidents (surgery schedule, cron, playbook delivery) — Mino already resolved those
- Changing the iteration cap
- Provider/model changes

---

# Mino Schedule Reliability — Wayfinder Map

## Destination

Every scheduled playbook run either happens (on time, or caught up late) or leaves a visible record — never silent. One long routine must not starve sibling schedules; a run missed during downtime must be fired late or reported.

Measurable: a schedule whose window passes without a run appears in `schedules.json` as `missed: true` and produces a Telegram notice; a sibling schedule always fires even when another routine overruns its window.

## Decisions so far

- [SCH-001 — Root cause](tickets/sch-001-root-cause.md) — **confirmed** against playbook.go: serial dispatcher, 1-min window, no catch-up, `LastError` only on fire-and-fail. "Due but never fired" has no representation.
- [SCH-002 — Harness fix](tickets/sch-002-harness-fix.md) — **resolved**: per-schedule goroutine + synchronous slot claim (starvation), boot catch-up same-day-only (downtime), `missed_at` + one outbox notice + trace + audit (silence). Never-run schedules are not flagged.
- [SCH-003 — Validation](tickets/sch-003-validation.md) — **resolved**: 8 tests / 15-case classify table at the `dispatchDueSchedulesAt(core, now, run)` seam; 355 total pass, `-race` clean.

## Out of scope

- Ticker cadence / timezone semantics
- Full cron-style job framework (roadmap: playbooks, not a job framework)
- Missed-run alerting policies beyond always-notify

## Not yet specified

- Production observation (sch-003): confirm missed runs surface in dashboard traces/audit after deploy.

---

# Mino Daily-Job Reliability — Wayfinder Map

Canonical tracker: GitHub issue #115 (map + child tickets REL-01..REL-06). This local file is not the source of truth for the REL series.
