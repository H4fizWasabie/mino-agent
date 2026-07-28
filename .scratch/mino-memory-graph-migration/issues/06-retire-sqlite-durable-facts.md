# Retire SQLite durable facts after live parity

Status: resolved
Type: task
Blocked by: 05

## Question

What deterministic tests, backup checks, live VPS evidence, rollback path, and release boundary are required before removing SQLite `facts` and `facts_fts` without risking semantic-memory loss?

## Answer

Retire SQLite durable facts only in a separate release after the graph cutover has been deployed and live-certified.

Required gates:

- Archive manifest count equals the SQLite source count, with every row mapped to a canonical graph claim or an explicit reviewed disposition.
- No subject-collision loss remains; all 173 observed live legacy rows have an auditable outcome.
- Unit and integration tests cover graph writes, overwrite, forget, embedding updates, edge validation, reconciliation, consolidation inference, and dashboard memory actions.
- A live natural conversation proves `remember`, `save_note`, `manage_memory`, consolidation, and dashboard graph reads against the deployed binary.
- Service health, deployed revision, relevant logs/traces, graph count, archive count, and SQLite backup state are recorded together.
- `PRAGMA quick_check` passes on the live database and the pre-removal backup; the backup is retained according to the existing deployment retention policy.
- A no-write observation window confirms that normal runtime paths no longer insert, update, or delete SQLite `facts` rows.

The release sequence is: deploy graph cutover; run live certification; create and validate a recoverable SQLite backup; observe the no-write window; deploy the schema removal; re-run health, memory, and rollback checks. Rollback is the previous binary plus the validated database backup and preserved migration archive. Never remove the SQLite table in the same release that first changes semantic-memory ownership.

## Context pointer

This decision is recorded in the Wayfinder map under `Decisions so far`.
