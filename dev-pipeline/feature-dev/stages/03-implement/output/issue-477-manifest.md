# Implementation manifest: #477

Branch: `fix/issue-477-icm-scoped-navigation`

## Files changed

- `session.go`:
  - Added `BuildNavigationSystem(home, name)` — loads the playbook workspace and reuses
    `BuildPlaybookSystem` unchanged, giving navigation turns the same narrow prompt the
    dedicated loop always used.
  - Added `BuildNavigationContext(system, userMessage)` — mirrors `PlaybookContext`'s shape
    (history + session note) but appends the turn's own message directly, since navigation
    has no separate caller-side stage-prompt-append step the way the dedicated loop did.
- `playbook_nav.go`: added `scheduledSessionPrefix` constant and
  `navigationPlaybookForTurn(source, sessionID) (string, bool)` — the single source of truth
  for "is this turn already known to be navigating a playbook."
- `playbook.go`: `fireSchedule` now uses `scheduledSessionPrefix` instead of a duplicated
  string literal.
- `app.go`: `RespondForContext` calls `navigationPlaybookForTurn` before building the system
  prompt; on a hit, uses `BuildNavigationSystem`/`BuildNavigationContext` instead of
  `BuildContext`/`ContextFor`, with `routing` left empty (skips the owner-fact
  post-verification check, correctly — that's general-chat-only machinery).
- `icm_navigation_scope_test.go` (new): captures the actual system prompt sent to the
  provider (via a scripted HTTP server) for three cases — scheduled fire, ordinary chat,
  chat continuation of an active navigation — asserting the narrow prompt is used exactly
  when expected and never otherwise.

## Build and test results

- `go build ./...`: PASS.
- Focused new tests (3): PASS.
- Full suite: 1 unrelated failure (`TestDashboardNavigationAndLegacyHashContract`), confirmed
  by inspection to check `static/index.html`/`static/app.js` navigation markup — files
  modified by a concurrent, unrelated UI/UX session's in-progress work in this same working
  tree, not touched by this change. All other tests pass.

## Scope note

Per the owner's instruction, `static/app.js` and `static/style.css` (modified by a concurrent
session) are left untouched and excluded from this commit.
