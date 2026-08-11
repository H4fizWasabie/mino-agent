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

---

# Mino Context Truth — Wayfinder Map

## Destination

A turn's established knowledge survives into the next turn; a user-named number is verified against ground truth, never confirmed by proximity; a run that degrades stops cheaply instead of burning to the iteration cap.

Measurable: (1) a session whose previous replies exceed `inputPreviewLimit` still has the method (DB paths, commands) in context; (2) a user-named value that differs from a computed one produces a reply stating the discrepancy; (3) a turn with 3+ consecutive `tool_call_parse_failed` iterations stops before the iteration cap.

## Notes

- **Incident 2026-08-10 (tg session, CHEM 15):** user asked why IDEXX CHEM 15 was in the July in-house-consumption Excel. Mino burned 30 iterations, hit the cap, replied "(stopped after 30 iterations)". User gave up and said they'd fetch the data themselves.
- **Ground truth:** the procura analysis module (internal/analytics/analytics.go:178) computes in-house consumption as `out+adj_out × cost` filtered to `item_behaviour='in-house use'`. July 2026 chart = **RM 20,851.69** — matching the user's "~20.8k" exactly. Mino's Excel was built from invented SQL (net depletion × cost, all Consumables, no behaviour filter) = 20,073.26, including CHEM 15 (`item_behaviour='Standard / Pack'` → correctly excluded). Deployed VPS binary verified against local source: identical analytics queries, frozen map (Jan 10,400 / Feb 27,800 / Mar 32,600 / Jun 15,576) present, July not frozen.
- **Why Mino failed:** all three prior replies (25,222 / 20,215 / 22,977 chars) exceeded `inputPreviewLimit` (8000) and were wholesale-replaced with a bare placeholder — the method-bearing `[tools used:]` trails never reached the model. It started the next turn at a different project's DB (`pos_server_test/prisma/dev.db`), re-derived everything from scratch, and confirmed by proximity ("essentially correct") instead of exact match.
- **The model self-diagnosed at 22:54:** wrote a `remember` note "directly query the Procura/PIMS database without searching around first" — compensating for harness-level rot with a pull-memory note that lacks the DB path.
- **Skills rejected as a fix:** existing `procura-purchase-history` skill carries the correct DB path and was listed at 22:49 — walked past anyway. A static skill rots the same way the model does.

## Decisions so far

- [CTX-001 — Root cause](tickets/ctx-001-root-cause.md) — **confirmed** against session.go + VPS state.db: the 8000-char wholesale replacement is the primary rot source; proximity confirmation (VFY class) the secondary; no stop signal the multiplier.
- [CTX-002 — Head/tail large-message preview](tickets/ctx-002-head-tail-preview.md) — **resolved** (closes #145, commit 4ffae81): messages over the limit keep first 4000 + last 4000 chars with HEAD/TAIL markers; the tail carries the trails. Test: `TestContextMessagesKeepsMethodTailOfLargeMessages`.

## Frontier (open tickets)

- [CTX-003 — Verification discipline](tickets/ctx-003-verification-discipline.md) — **resolved** (closes #149): system prompt rule — user-named ≠ computed must state both numbers and the gap; "verified" only from source of truth.
- [CTX-004 — Working-state persistence](tickets/ctx-004-working-state.md) — **resolved** (closes #146): per-session `session_notes` row, appended by the harness (bash commands) and the model (`note_session` tool), injected at turn start, bounded 1500 chars.
- [CTX-005 — Cancel-intent recognition](tickets/ctx-005-cancel-intent.md) — **resolved** (closes #148): natural cancel phrasings stop; doubt/cancel hybrids proceed as turns.
- [CTX-006 — Degeneration guard](tickets/ctx-006-degeneration-guard.md) — **resolved** (closes #147): parse failures counted per-turn total, abort at 6 — closes the alternation loophole the 2026-08-10 run exploited.

## Frontier (open tickets)

- None — map complete. Next iteration targets: production observation of all four fixes (discrepancy replies, note injection, cancel behavior, early aborts) before new tickets.

## Out of scope

- New skills as the fix (proven to rot: walked past at 22:49)
- Changes to the 30-iteration cap itself (the guard stops early; the cap stays)
- Reversing or duplicating history ordering (chronology is required; tail is already the newest)
