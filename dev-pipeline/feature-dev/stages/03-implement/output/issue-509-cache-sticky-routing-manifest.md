# Implementation manifest: session-sticky upstream routing (#509)

## Files changed

- `provider.go`: opt in to OpenRouter router metadata, parse the selected
  upstream, carry it on `LLMResponse`, and store it in usage rows.
- `provider_manager.go`: pass the session ID, remember the last successful
  eligible upstream per session/role/model, prefer it in the current ranked
  route, and clear it on request failure.
- `db.go`: add schema migration v10 and the `usage_log.upstream_provider`
  column.
- `dashboard.go`: expose the upstream field through the existing usage record
  shape.
- `provider_manager_test.go`: cover session IDs, router metadata, and sticky
  upstream ordering.
- `db_test.go`, `reminder_test.go`: update schema-version expectations.
- `CHANGELOG.md`: record the user-visible routing/cache observability change.

## Interfaces and configuration

- Added private manager state and helpers only; no public caller interface or
  configuration key changed.
- Added `LLMResponse.UpstreamProvider` as an internal response attribution
  field.
- OpenRouter metadata is requested only for `openrouter.ai` endpoints.
- Response caching is intentionally not enabled.

## Tests added or updated

- `TestProviderManagerSticksToOpenRouterUpstream`
- `TestSessionIDSentToOpenRouter`
- `TestParseResponseReadsOpenRouterUpstream`
- `TestParseResponseMarksMalformedToolCall`
- Existing cache-usage, real-cost, and schema migration tests

## Verification

- Focused tests: PASS (7 tests).
- `go vet ./...`: PASS.
- `git diff --check`: PASS.
- `go test ./...`: bounded at 90 seconds with no output; the existing harness
  did not complete. This is recorded as unresolved environment/test-harness
  verification, not a pass claim.
- `graphify update .`: PASS.
- `codegraph sync`: PASS; seven changed source files indexed.

## Deferred

- No semantic detector for valid but garbled prose; generic Mino code cannot
  safely classify that failure.
- No persistent upstream preference; process restart intentionally resets it.
- No cost-watch ranking changes, whole-response caching, release, merge, or
  deployment in this PR.

## Scope note

`reminder_test.go` is outside the design file list only because it hard-codes
the schema version; updating that expectation is required by the v10 migration
and is directly justified by the implementation.
