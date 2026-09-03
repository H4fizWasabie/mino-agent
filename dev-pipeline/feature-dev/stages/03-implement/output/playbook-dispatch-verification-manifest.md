# Implementation manifest: playbook dispatcher verification

## Root cause

`RunLoopContext` recorded direct tool calls in the navigation tracker, but
deferred tools invoked through `tool_call` were executed inside
`Registry.ExecuteContext` and were only persisted to `tool_calls`. Stage
verification therefore missed successful publish calls and emitted a warning.

## Files changed

- `tools.go`: record the actual executed tool at the shared registry boundary.
- `loop.go`: remove the now-duplicating direct-loop recording hook.
- `playbook_nav_calls_test.go`: add a regression test for a successful deferred
  `threads_post` execution.
- `CHANGELOG.md`: record the fix.

## Scope

No playbook definitions, external state, release artifacts, or deployment were
changed.

## Checks

- Focused navigation/verifier tests: PASS.
- `git diff --check`: PASS.
