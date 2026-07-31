# Graph memory cutover certification

Date: 2026-07-28 UTC deployment / 2026-07-29 Asia/Kuala_Lumpur work session

## Local

- `GOCACHE=/tmp/mino-graph-go-build go test ./... -count=1`: passed.
- Focused migration, graph-management, consolidation-validation, and integration tests: passed.
- SQLite fact migration is read-only and repeatable.

## VPS

- Host: `100.101.53.98`
- Release: `v1.3.0`
- Binary SHA-256: `667a72f654e490184bf037f2cf79fb37f4ddb959020feb1e96aaa11c07c4370b` (local = remote)
- `/health`: `{"status":"ok","version":"v1.3.0"}`
- Services: `mino`, `minowrap`, and `mino-fileingest` active.
- Graph index: version 2, 255 facts, 255 tracked files.
- Legacy archive: 173 `fact_<sqlite-id>.md` files for 173 SQLite fact rows.
- SQLite `state.db`: `PRAGMA quick_check` = `ok`.
- Rollback backup: `/home/mino/.mino/backups/state.db-20260728T170608Z`, `PRAGMA quick_check` = `ok`.
- Dashboard `/api/data`: 255 graph semantic facts and 173 read-only legacy diagnostics.
- Unprovenanced generic `related_to` edges after cutover: 0.
- Stable graph embedding identities restored for 234 claims; the bounded edge rebuild produced 2 additional inferred edges before the provider time budget was stopped, leaving 7 total explicit/inferred edges.
- No-write observation: SQLite facts remained `173/178` rows/max-id before and after 12 seconds.

## Natural API certification

One temporary session, `cert-graph-20260729`, used the real configured API to save a graph fact and recall it with `remember`. The graph returned the marker successfully. Cleanup verified zero remaining chat rows, tool-call rows, session artifacts, trace matches, and the temporary Markdown fact.

## Rollback boundary

The SQLite `facts` and `facts_fts` tables remain intact in this release. Rollback is the validated backup plus the prior binary; schema retirement is intentionally a separate release after an explicit approval and longer observation window.
