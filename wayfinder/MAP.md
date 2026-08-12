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

Measurable: (1) a session whose previous replies exceed `inputPreviewLimit` still has the method (paths, commands) in context; (2) a user-named value that differs from a computed one produces a reply stating the discrepancy; (3) a turn with repeated `tool_call_parse_failed` iterations stops before the iteration cap.

## Notes

- **Incident 2026-08-10 (telegram session):** the user challenged an item's inclusion in an app's analytics chart. Mino burned 30 iterations, hit the cap, replied "(stopped after 30 iterations)". The user gave up and fetched the data themselves.
- **Ground truth:** the app's analytics module (its source, `internal/analytics/analytics.go:178`) computes the chart as `out+adj_out × cost` over items whose behaviour is "in-house use". The chart's July value matched the user's recollection exactly; Mino's report was built from invented SQL (net depletion × cost over all items of the type, no behaviour filter) and differed from the chart by ~4% — and included the challenged item, whose behaviour value correctly excludes it. The deployed binary was verified against the app's source: identical analytics queries, same frozen baselines, July not frozen (computed live).
- **Why Mino failed:** the three previous replies (20-25k chars each) exceeded `inputPreviewLimit` (8000) and were wholesale-replaced with a bare placeholder — the method-bearing `[tools used:]` trails never reached the model. It started the next turn at a different project's development database, re-derived everything from scratch, and confirmed by proximity ("essentially correct") instead of exact match.
- **The model self-diagnosed mid-session:** wrote a `remember` note "directly query the database without searching around first" — compensating for harness-level rot with a pull-memory note that lacked the path.
- **Skills rejected as a fix:** an existing skill carries the correct database path and was listed earlier that session — walked past anyway. A static skill rots the same way the model does.

## Decisions so far

- [CTX-001 — Root cause](tickets/ctx-001-root-cause.md) — **confirmed** against session.go + VPS state.db: the 8000-char wholesale replacement is the primary rot source; proximity confirmation (VFY class) the secondary; no stop signal the multiplier.
- [CTX-002 — Head/tail large-message preview](tickets/ctx-002-head-tail-preview.md) — **resolved** (closes #145, commit 4ffae81): messages over the limit keep first 4000 + last 4000 chars with HEAD/TAIL markers; the tail carries the trails. Test: `TestContextMessagesKeepsMethodTailOfLargeMessages`.
- [CTX-003 — Verification discipline](tickets/ctx-003-verification-discipline.md) — **resolved** (closes #149): system prompt rule — user-named ≠ computed must state both numbers and the gap; "verified" only from source of truth.
- [CTX-004 — Working-state persistence](tickets/ctx-004-working-state.md) — **resolved** (closes #146): per-session `session_notes` row, appended by the harness (bash commands) and the model (`note_session` tool), injected at turn start, bounded 1500 chars.
- [CTX-005 — Cancel-intent recognition](tickets/ctx-005-cancel-intent.md) — **resolved** (closes #148): natural cancel phrasings stop; doubt/cancel hybrids proceed as turns.
- [CTX-006 — Degeneration guard](tickets/ctx-006-degeneration-guard.md) — **resolved** (closes #147): parse failures counted per-turn total, abort at 6 — closes the alternation loophole the 2026-08-10 run exploited.

## Frontier (open tickets)

- [CTX-007 — Dashboard client disconnect can wedge the session mutex](tickets/ctx-007-wedge.md) — **resolved** (closes #152): the loop's LLM calls now go through the ctx-aware path; a dead client connection propagates into the provider call and the loop returns instead of wedging. Regression test included.
- [CTX-008 — Provider policy docs lag the main-model change](tickets/ctx-008-policy-docs.md) — **resolved** (closes #151): policy file, cost-watch monitored set, and docs now declare deepseek:deepinfra as main; the swap is permanent.
- [CTX-009 — Native send_document tool](tickets/ctx-009-send-document.md) — **resolved** (closes #153, commit ff4ecec): outbox `doc_*.json` drafts delivered via multipart `/sendDocument`; token never in args. Awaiting release to the VPS.
- [CTX-010 — Log provider failure reasons](tickets/ctx-010-provider-failure-logging.md) — **resolved** (closes #154): every failed provider call logs provider/role/model/error; circuit-breaker trips log the cooldown.
- [CTX-011 — Stop-word anywhere stops](tickets/ctx-011-stop-anywhere.md) — **resolved** (closes #157): "its fine, stop" now cancels; questions about stopping still proceed.
- [CTX-012 — Interrupt replies dropped on tool-call answers](tickets/ctx-012-interrupt-empty.md) — **resolved** (closes #156): no schemas in the interrupt call, plain-text instruction, snapshot-status fallback.
- [CTX-013 — Stale workaround memory overrides the native tool](tickets/ctx-013-send-document-unpinned.md) (open, #155) — selection verified working; four stale notes taught the curl path; deleted them. No pinning — the essential set stays universal.
- [CTX-014 — Memory facts surface age at recall](tickets/ctx-014-memory-freshness.md) (resolved, #172) — live recall now appends `age: Nd` (and `(possibly stale)` past 30d) to the match rationale via the existing `At` field; ranking score untouched. Code-checked first: `At` existed but was unwired; `Feedback` only did rejection-expiry (MEM-08). First witnessed case for the OKF `stale_after` idea we skipped — and the field was already there.

## Out of scope

- New skills as the fix (proven to rot: walked past during the incident session)
- Changes to the 30-iteration cap itself (the guard stops early; the cap stays)
- Reversing or duplicating history ordering (chronology is required; tail is already the newest)

# Mino Context-Awareness & Tool-Loading — Wayfinder Map

## Destination

Mino's context is lean at the eager-injection layer (skills loaded by section, not full body) and the model can self-regulate (see iterations/retries, diverge or stop before the cap) instead of burning to a silent harness cap.

## Decisions so far

- **Frontier (not yet a ticket): the working-set *choice* layer (#4).** Mechanism side (lazy fetch: `remember` pointers, artifact catalog, 8-tool playbook scoping, bounded history) is ~shipped; choice side (model can perceive/prune/compress its own window mid-turn) is ~0 — every model context tool is a *writer* (`note_session`, `save_note`, `add_working_memory`), none reads or shrinks the current window. Deferred by design: choice needs awareness (#171) first; revisit with a real case after #170+#171 land. (Measured 2026-08-12: playbook `one_turn` 18–28k chars ≈ 2–3× the whole ~2.4k static prompt.)
- [GitHub #170 — Eager skill bodies injected en bloc (no section routing)](https://github.com/H4fizWasabie/mino-agent/issues/170) — measured token waste on automation; only `image-generation` needed by playbooks.
- [GitHub #171 — Iteration/retry awareness + containment](https://github.com/H4fizWasabie/mino-agent/issues/171) — expose live `i/maxIter` + repeated-tool signal; model-visible rule to diverge or give up before the cap. Driver: 2026-08-12 FB `01-post` 50-iteration research churn (pre-contract-fix).
- **Static system prompt is thin (~2.4k tok), not fat** — measured; trimming it is a low-value/high-regression lever. The real token cost is the [eager skill injection](#170), not the static prompt.
- **Tool-schema union is correct as-is** — chat=20 wide (legit), automation=8 tight (legit).

## Out of scope
- Context-budget telemetry (#2) — rejected; cheaper to stop-on-spin (#171) than to gauge.
- Multi-owner trust/provenance (single-owner Mino)
- The 30-iteration cap itself
