# Preserve and canonicalize legacy durable facts

Status: resolved
Type: grilling

## Question

How should all 173 live SQLite durable-fact rows be preserved under collision-safe temporary identities, then consolidated into stable claim-based graph nodes without losing distinct claims or silently overwriting data?

## Answer

Preserve every legacy row in an inactive Markdown migration archive before canonicalization:

```text
~/.mino/memory-migration/
  legacy/
    fact_001.md
    fact_002.md
    ...
  manifest.json
```

Each archived file keeps the original SQLite ID, subject, content, timestamp, and source. `manifest.json` records the eventual canonical graph ID and reconciliation outcome for each legacy row. The archive is outside the active `memories/` directory, so temporary legacy nodes do not appear in graph traversal or dashboard visualization. SQLite remains untouched as the rollback source until the migration has passed parity checks.

This preserves all data, prevents subject-slug collisions, keeps the active graph clean, and leaves an auditable mapping from every old row to its canonical claim node.

## Context pointer

This decision is recorded in the Wayfinder map under `Decisions so far`.
