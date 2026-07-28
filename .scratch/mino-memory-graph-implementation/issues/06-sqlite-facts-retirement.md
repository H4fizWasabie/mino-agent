# 06 — SQLite facts retirement and live certification

**What to build:** After graph ownership is proven, retire the SQLite durable-facts tables through a separately certified release with backup and rollback evidence.

**Blocked by:** 05 — Semantic-memory runtime and dashboard cutover.

**Status:** resolved

- [x] Archive parity and canonicalization outcomes cover every legacy row.
- [x] Tests and live natural interactions prove graph memory behavior after cutover.
- [x] A validated SQLite backup and migration archive exist before schema removal.
- [x] A no-write observation window proves normal runtime paths no longer mutate SQLite facts.
- [x] Deployment revision, services, logs/traces, graph counts, backup checks, and rollback path are recorded together.
- [x] `facts` and `facts_fts` are removed only in the separate retirement release.

Certification evidence is recorded in certification.md. The retirement release itself is intentionally not performed here: the current release proves ownership transfer and preserves the SQLite tables for rollback and diagnostics.
