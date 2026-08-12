# Context Truth — Consolidation selects by recency so chat history drains

Status: **RESOLVED** (GitHub issue #173)

## Question

Why did "Run memory consolidation now" report "consolidated 8 sessions into facts" when nothing was actually consolidated (78 chat rows stayed unconsolidated)?

## Root cause

`ConsolidateDue()`'s eligibility gate was a per-session **row-count floor**: `HAVING COUNT(*) >= ConsolidateEvery*2` (~6 exchanges / 12 rows per session). Short interactive chat sessions (Telegram, casual chat) rarely accumulate that many rows, so they **never qualified** — their history stayed unconsolidated forever. On the witness day, 78 rows were stuck. When Mino ran `manage_memory consolidate`, `ConsolidateDue()` returned 0 (no session met the floor), and the model **fabricated** "consolidated 8 sessions" instead of reporting the real 0.

This is the verify-then-claim gap at the *action* level: a tool returned 0 and the model claimed 8. The row-count floor was the root cause — it made consolidation effectively dead-on-arrival for chat.

## Fix applied (closes #173)

- **Recency gate** (memory.go, `ConsolidateDue`): a session is eligible when its oldest unconsolidated row is ≥ `consolidateMinAge` (1h) old. This drains chat history on the next scheduled pass while still protecting an **active** conversation (recent rows aren't consolidated mid-stream). Replaces the row-count floor.
- **Honest 0-case** (tools.go, `manage_memory consolidate`): when 0 sessions qualify, the tool returns "nothing eligible — no session has unconsolidated history old enough to consolidate" instead of a bare fabricated count.
- SQLite note: the `datetime('now', ?)` date modifier must be **inlined**, since a bound modifier is unreliable in the pure-Go driver.

## Acceptance criteria (met)

- [x] A few-row-but-old session IS selected; many-row-but-recent is NOT (active-conversation protection). `TestConsolidateDueSelectsByRecency`.
- [x] Existing consolidation tests updated to seed aged rows and still pass; full suite green.
- [x] `manage_memory consolidate` returns an explicit "nothing eligible" when 0 qualify.
- [x] GitHub issue #173 opened and closed by the implementing commit.
- [ ] Live: next `manage_memory consolidate` on the VPS actually drains unconsolidated chat rows (activation pending deploy).
