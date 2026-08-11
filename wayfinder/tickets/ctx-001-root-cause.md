# Context Truth — Root Cause

Status: **CONFIRMED** (2026-08-10, verified against session.go + VPS state.db)

## Question

Why did a question Mino's own previous turns had already answered burn 30 iterations and die at the cap?

## Resolution

**Three stacked mechanisms, in order of leverage:**

1. **Wholesale large-message replacement (primary).** `ContextMessages` (session.go) replaces any history message over `inputPreviewLimit` (8000 chars) with a bare placeholder. On 2026-08-10 the three previous replies measured 25,222 / 20,215 / 22,977 chars — all replaced. The `[tools used:]` trails they carried (DB path, commands, schema dumps) never reached the model. It started the next turn at `/home/mino/pos_server_test/prisma/dev.db` — a different project's database — and re-derived everything from scratch.

2. **Proximity confirmation (VFY class).** Told the chart was "~20.8k", Mino computed 20,073.26 from invented SQL (net depletion × cost, all Consumables, no `item_behaviour` filter), and replied "your memory of ~20.8k is essentially correct". The real chart value is 20,851.69 — it never fetched ground truth. This is VFY-001 ("thinks, doesn't check") applied to numbers.

3. **No stop signal (multiplier).** `isStopMessage` (app.go:361) only matches leading "stop/cancel/halt" or exact "nevermind". "Its fine then, ill get this data myself" matched nothing; the only brake is `MINO_MAX_ITERATIONS=30`. Audit shows the model was already degrading (iterations 24–26 all `tool_call_parse_failed`).

## Context

- The answer was in Mino's hands: `item_behaviour` column was in the schema it dumped at 22:51:59. It tested `exclude` and `product_status` as filters, never the defining column.
- A skill with the correct DB path exists on the VPS (`procura-purchase-history`) and was listed at 22:49 — walked past anyway. Skills are not a fix.
- The user was right the whole time: CHEM 15 (`item_behaviour='Standard / Pack'`) is excluded from the chart by the module's `in-house use` filter (analytics.go:178).

## Confirmation

- VPS state.db session tg:1794722543: message lengths measured; audit_events shows 9 parse failures incl. 4 consecutive on one marker (iterations 24–26).
- Deployed binary `/usr/local/bin/procura` (Aug 3): analytics queries byte-identical to local source; frozen map (10,400/27,800/32,600/15,576) present exactly once each; July 2026 not frozen → chart computed live = 20,851.69.
