# Context Truth — Working-state persistence

Status: **OPEN** (next build — the capability fix)

## Question

How does a turn's established knowledge (DB paths, methods, key numbers, open discrepancies) survive into the next turn without being re-derived from scratch?

## Evidence

2026-08-10 session: Mino re-hunted the procura DB path 3× in one session, hit "DB MISSING" against a wrong path, re-derived the 20,073.26 SQL twice, and started the CHEM 15 turn at a different project's database. At 22:54 the model tried to self-medicate with a `remember` note that lacked the path — it felt the rot and had no mechanism to fix it.

## Design sketch

- Per-session working-note file (e.g. under the session's result dir): written by the harness at turn end, injected at next turn start — judgment-free, cannot be forgotten.
- Contents: discovered paths, the method that produced key numbers, key numbers themselves, open discrepancies ("20,073.26 ≠ user's ~20.8k — unresolved"), rejected hypotheses.
- Injection point: `ContextFor`/`PlaybookContext` after the artifact catalog.
- Bounded (e.g. 1,500 chars, head/tail truncated) so it cannot become a token sink.
- The model at 22:54 wrote the spec itself: "directly query the Procura database without searching around first" — the note must carry the *path*, which its memory note did not.

## Acceptance criteria

- [ ] Turn N+1 opens with turn N's established facts (path, method, numbers) in context
- [ ] Open discrepancies are carried and surfaced, not dropped
- [ ] A wrong-path start like 2026-08-10 becomes impossible without re-discovery (note contains the confirmed path)
- [ ] Note is bounded and cannot grow past its cap
