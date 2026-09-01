# Implementation manifest: remove dead ponytail-audit paths

## Files changed

- `playbook.go`, `playbook_workspace.go`, `playbook_nav.go`, `playbook_script.go`,
  `run_registry.go`: removed the uncalled dedicated playbook runner, its loop seam, its
  iteration constant, and stale references; the canonical navigator remains the only
  playbook execution path.
- `playbook_test.go`, `playbook_script_test.go`, `playbook_schedule_navigate_test.go`:
  removed tests whose only subject was the deleted runner and retained navigator/scheduler
  coverage.
- `loop.go`, `app.go`, `main.go`, `telegram.go`, `dashboard.go`, `eval.go`, plus affected
  tests: removed the unused stream selector from the loop and turn APIs and reduced test
  fakes to `CreateContext`.
- `provider.go`, `provider_manager.go`, `codex.go`, and provider tests: removed unused
  OpenAI/Anthropic streaming wrappers and parsers while retaining the Codex Responses SSE
  decoder required by that transport; non-streaming request parsing is unchanged.
- `mcp.go`: made startup use the existing reload scan.
- `tools.go`, `tools_test.go`: removed the uncalled `Registry.Only` helper and its
  helper-specific test.
- `memory.go`, `memory_test.go`: reused `sortedInts` for community IDs and removed tests
  for deleted provider stream parsers.
- Stage outputs: intake and design define the approved scope and interfaces.

## Interfaces and config

- `LLMClient` now exposes only `CreateContext`.
- `RunLoop`, `RunLoopContext`, `Core.Respond`, `Core.RespondFor`, and
  `Core.RespondForContext` no longer accept a stream boolean.
- No new interface, config key, endpoint, dependency, or persistent state was added.

## Tests

- Fresh `GOCACHE=/tmp/mino-ponytail-go-build go test -count=1 -timeout 180s
  -skip '^TestVerifyNewBinaryTimeout$' ./...` — 843 passed, 1 skipped across both
  packages; the excluded rollback timeout test is unrelated to this diff.
- Fresh full `go test -count=1 -timeout 180s ./...` reached the pre-existing
  `TestVerifyNewBinaryTimeout` failure at the timeout; see stage-04 verification.
- `GOCACHE=/tmp/mino-ponytail-go-build go vet ./...` — passed.
- `git diff --check` — passed.
- Graphify update and CodeGraph sync completed after edits; deleted symbols no longer appear
  in either index.

## Criteria coverage

- Canonical playbook execution remains covered by `playbook_navigate_test.go`,
  `playbook_nav_calls_test.go`, and `playbook_schedule_navigate_test.go`.
- Context-aware loop cancellation and termination remain covered by `loop_regression_test.go`
  and the existing context tests.
- Provider request payload and JSON parsing remain covered by `provider_test.go`,
  `provider_manager_test.go`, and `codex_test.go`.
- MCP startup behavior now shares the tested reload implementation.
- Tool and memory behavior remains covered by the surviving registry and memory tests.

## Deferred

- No release, push, merge, tag, deployment, or live-state change was performed in this
  stage. The approved workflow can continue after stage-04 verification and stage-05
  changelog work.
