# Cut semantic memory paths over to the graph

Status: resolved
Type: task
Blocked by: 01, 02, 03, 04

## Question

Which runtime paths must stop reading or writing SQLite `facts`/`facts_fts`, and what graph-backed behavior must replace `manage_memory`, dashboard memory actions, embeddings, consolidation, and compatibility reads?

## Answer

Cut over semantic memory in this order:

1. Keep `remember`, `save_note`, consolidation, and graph visualization on `GraphMemory`; these are already graph-oriented.
2. Rewrite `manage_memory` so correct, forget, confirm, and reject operate on graph claim IDs/subjects and update or remove Markdown facts and their embeddings.
3. Make dashboard Memory and graph editing read/write the graph. The Database view may show SQLite operational state, but must no longer present the legacy `facts` table as the canonical memory view.
4. Replace or retire the legacy `Memory.Search`/`facts_fts` semantic path. Episodic search and operational queries remain SQLite-backed.
5. Normalize embedding identity around graph fact IDs so updates and deletions remove the old vector deterministically.
6. Change migration to read SQLite only into the inactive archive and manifest; no normal runtime path writes new rows to SQLite `facts`.
7. Add a temporary compatibility/read-only diagnostic path for parity checks, then remove it after the retirement ticket is satisfied.

The semantic-memory boundary is therefore `GraphMemory`; SQLite remains authoritative for chat history, episodes, calendar/reminders, tool/audit state, and other operational tables. The cutover is complete only when tests demonstrate that every durable-memory mutation is visible in Markdown and no production semantic-memory path depends on `facts` or `facts_fts`.

## Context pointer

This decision is recorded in the Wayfinder map under `Decisions so far`.
