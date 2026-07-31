# 02 — Lossless legacy archive and canonical claim migration

**What to build:** Every legacy SQLite durable fact is preserved in an inactive Markdown archive, then mapped to collision-safe canonical claim nodes.

**Blocked by:** 01 — Graph storage contract and live reconciliation.

**Status:** resolved

- [x] Every legacy row is archived with original ID, subject, content, timestamp, and source.
- [x] A manifest records each row's canonical ID and reconciliation disposition.
- [x] Subject collisions cannot overwrite or silently discard claims.
- [x] The active graph excludes temporary archive records.
- [x] Migration is repeatable and produces deterministic counts and reports.

Implemented in `graph_memory.go` and `memory.go`:

- Archives rows under `memory-migration/legacy/fact_<sqlite-id>.md` and records the source, timestamp, body, and SQLite ID in `manifest.json`.
- Maps rows to deterministic active graph IDs, preserving exact duplicates and suffixing conflicting claims instead of overwriting them.
- Keeps episodes and SQLite rows intact; migration is read-only against SQLite.
- Re-running the migration skips already reconciled rows and does not rediscover word-overlap edges.

Verification: `GOCACHE=/tmp/mino-graph-go-build go test ./... -count=1` passed; migration tests cover archive fidelity, collision safety, idempotence, and SQLite preservation.
