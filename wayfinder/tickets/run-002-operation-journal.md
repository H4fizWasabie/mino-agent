# Runtime Self-Management — Operation journal (undo backbone)

Status: **RESOLVED** (wayfinder ticket, RUN-002 — GitHub issue #216)

Resolved 2026-08-16: single `ops_journal` table in state.db (id, ts,
op_type, entity, before_state, after_state, status, session_id, undo_of)
with an `(entity, id)` index — no FTS. `OpJournal.Run` is the atomic seam:
state mutation + journal entry commit in one transaction ("no op without
entry, no entry without an op"); `LastOp`/`Get` are the consumer lookups
("last op on entity X", rollback-chain resolution via `undo_of`). No undo
execution yet — the consumers arrive in RUN-001/003/004/005. Tests:
`TestOpJournalRunCommitsEntryAndMutation`,
`TestOpJournalRunRollsBackOnMutationError`,
`TestOpJournalLastOpIsNewestPerEntity`, `TestOpJournalUndoChain`,
`TestOpJournalRunValidates`.

## Question

Every privileged/self-modifying operation (extension install, config edit,
package change, unit write, binary swap) needs a before-state/command/
after-state record — one shared journal consumed by binary rollback
(RUN-004), config self-heal (RUN-005), and package/unit undo
(RUN-001/RUN-003), not bespoke rollback per feature.

## Decisions so far

- **Storage: SQLite in state.db, not JSONL** (issue #216 comment,
  2026-08-16) — a transaction wraps journal-write + state-change together,
  crash-consistent by construction. Zero new machinery: state.db is already
  open and backed up with the state directory. No FTS index.
- Table shape: id, ts, op_type, entity, before_state, after_state, status,
  session_id, undo_of (rollback chain). One table, four consumers.
- The journal records only; undo execution belongs to the consumers — the
  API must not assume RUN-001/003's undo semantics exist yet. Status
  vocabulary: `ok` / `failed` / `rolled_back` (the last written by rollback
  consumers in later tickets).

## Out of scope

- Undo execution (RUN-001/003/004/005)
- Approval tier (RUN-006) and privilege bridge (RUN-003)
- Extension supervision architecture (decides RUN-001's shape, not the
  journal's)
