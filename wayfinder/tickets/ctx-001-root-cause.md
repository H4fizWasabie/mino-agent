# Context Truth — Root Cause

Status: **CLOSED** (2026-08-17 — analysis complete; each root-cause mechanism was fixed by its own follow-up ticket: ctx-002 head/tail preview #145, ctx-003 verification discipline #149, ctx-005 cancel-intent #148 — all RESOLVED)

## Question

Why did a question Mino's own previous turns had already answered burn 30 iterations and die at the cap?

## Resolution

**Three stacked mechanisms, in order of leverage:**

1. **Wholesale large-message replacement (primary).** `ContextMessages` (session.go) replaces any history message over `inputPreviewLimit` (8000 chars) with a bare placeholder. On 2026-08-10 the three previous replies measured 25,222 / 20,215 / 22,977 chars — all replaced. The `[tools used:]` trails they carried (database path, commands, schema dumps) never reached the model. It started the next turn at a different project's development database and re-derived everything from scratch.

2. **Proximity confirmation (VFY class).** Told the chart was "~20.8k", Mino computed a value from invented SQL (net depletion × cost over all items of the type, no behaviour filter) and replied "your memory is essentially correct". The computed value differed from the chart's real value by ~4% — it never fetched ground truth. This is VFY-001 ("thinks, doesn't check") applied to numbers.

3. **No stop signal (multiplier).** `isStopMessage` (app.go:361) only matched leading "stop/cancel/halt" or exact "nevermind". "Its fine then, ill get this data myself" matched nothing; the only brake was `MINO_MAX_ITERATIONS=30`. Audit shows the model was already degrading (the last iterations were all `tool_call_parse_failed`).

## Context

- The answer was in Mino's hands: the defining column was in the schema it dumped mid-session. It tested other columns as filters, never the one that defines the chart.
- A skill with the correct database path exists on the VPS and was listed earlier that session — walked past anyway. Skills are not a fix.
- The user was right the whole time: the challenged item's behaviour value correctly excludes it from the chart by the module's filter (analytics.go:178).

## Confirmation

- VPS state.db session: message lengths measured; audit_events shows 9 parse failures incl. 4 consecutive on one marker.
- Deployed binary (Aug 3): analytics queries byte-identical to the app's source; same frozen baselines present; the disputed month is not frozen → computed live.
