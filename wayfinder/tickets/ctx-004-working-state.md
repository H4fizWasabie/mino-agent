# Context Truth — Working-state persistence

Status: **RESOLVED** (closes GitHub issue #146, commit pending)

## Question

How does a turn's established knowledge (DB paths, methods, key numbers, open discrepancies) survive into the next turn without being re-derived from scratch?

## Resolution

Per-session working note in SQLite (`session_notes`), written by two writers, injected at every turn start:

1. **Harness (judgment-free):** `AddExchange` appends every bash command run this turn (`ran: <command>`, 200-char cap). The next turn inherits discovered paths and methods even if the model never writes a note — this cannot be forgotten, unlike the 22:54 `remember` note that lacked the path.
2. **Model:** `note_session` tool (essential, 12th pinned) appends one line — confirmed paths, the method behind a number, open discrepancies ("computed 20073 vs user's 20.8k — unresolved").

Injection: `ContextFor`/`PlaybookContext` append the note (1500-char head+tail bound) right before the current user message, headed "Session working note (established by earlier turns — do not re-discover; verify only if contradictory)".

Boundary vs `save_note`: the graph is durable and pull-based; this is ephemeral per-session working state — deliberately NOT judged, NOT permanent.

## Acceptance criteria (all met)

- [x] Turn N+1 opens with turn N's established facts (path, method, numbers) in context
- [x] Open discrepancies are carried and surfaced, not dropped
- [x] A wrong-path start like 2026-08-10 requires re-discovery (harness appends the confirmed `sqlite3 /home/procura/...` command mechanically)
- [x] Note is bounded (2000 storage / 1500 injection, oldest lines dropped)

## Validation

- 4 new tests: append-bounded-newest-wins, head+tail truncation, ContextFor injection (empty absent / populated present with header), AddExchange bash recording (non-bash tools excluded)
- `go test ./...` — 500 pass; schema-union cap tests re-based for the 12th essential
