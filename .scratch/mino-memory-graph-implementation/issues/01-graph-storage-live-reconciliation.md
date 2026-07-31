# 01 — Graph storage contract and live reconciliation

**What to build:** The graph treats Markdown memory files as authoritative and keeps a fast, accurate index while Mino runs continuously.

**Blocked by:** None — can start immediately.

**Status:** resolved

- [x] Canonical front matter and directed edge metadata are parsed and written with lowercase JSON/index fields.
- [x] Claim overwrite, explicit edge validation, and rebuild-from-Markdown behavior work without SQLite.
- [x] Changed, new, deleted, malformed, and partially written Markdown files reconcile without an LLM or restart.
- [x] `index.json` is rebuilt atomically and remains a derived cache.
- [x] Graph storage and reconciliation behavior has focused tests.

Verification: `GOCACHE=/tmp/mino-graph-go-build go test ./... -count=1` passed. `git diff --check` passed.
