# Verification report: session-sticky upstream routing (#509)

## Test results

- Focused functional tests: PASS (8 tests).
- Focused race tests: PASS (4 tests).
- `go build ./...`: PASS.
- `go vet ./...`: PASS.
- `git diff --check`: PASS.
- `go test ./...`: NOT COMPLETED. Two bounded attempts produced no output and
  exceeded the 90-second bound in this environment; the process was stopped.

## Acceptance criteria

1. **Successful upstream remains first:** observed by
   `TestProviderManagerSticksToOpenRouterUpstream`, which starts with B cached
   behind A and receives B first on the next request.
2. **Changed route invalidates stale preference:** implemented by validating the
   remembered name against the current route; absent candidates are deleted and
   the current ranked list is returned. A live cost-watch reload was not run.
3. **Failures clear preference:** transport/timeout/parse errors clear before
   retry; malformed native tool-call arguments are marked and clear after the
   response while preserving Mino's existing self-correction path. Transport
   and malformed parser behavior are covered by existing and new focused tests;
   full end-to-end failover remains blocked by the stalled suite.
4. **Fallback becomes preference:** manager records the selected eligible
   upstream from a successful response. Covered through the sticky-manager test.
5. **Metadata and usage attribution:** observed by parser and request-header
   tests; schema v10 and dashboard reader carry an empty value safely when a
   provider does not return metadata.
6. **No response replay cache:** verified by diff/config inspection; no response
   cache header or replay store was added.
7. **Cost-watch and non-OpenRouter behavior:** cost-watch files are unchanged;
   metadata header is restricted to `openrouter.ai`; routing order is preserved
   except for the remembered eligible upstream moving to the front.
8. **Build/test/tooling:** build, vet, focused tests, focused race tests, graph
   refresh, and CodeGraph sync pass. Full suite remains unresolved as above.

## Invariants

- Model agnosticism: HELD. Upstream names are opaque configured strings and
  metadata stays in the OpenRouter-compatible adapter.
- Loop termination: HELD. No new unbounded loop; route scans are bounded by the
  configured list and existing retry limits remain.
- Context managed: HELD. No prompt content or persistent session payload was
  added.
- Guardrails: HELD. No tool or approval boundary changed.
- Failure explicit: HELD in the changed request path; errors still flow through
  existing retry/failover logging, and malformed tool arguments remain surfaced
  to the loop rather than executed.
- State local/inspectable: HELD. Preference is process-local; attribution is
  in SQLite and exposed through existing usage records.
- Single binary/dependencies: HELD. No dependency added.

## Provider parity and open concerns

The adapter remains compatible with non-OpenRouter providers because they do
not receive the metadata header and missing metadata is tolerated. Actual live
calls against two external providers were not possible in this environment;
the focused manager test exercises provider-agnostic routing with opaque names.
The full repository suite and real-provider cache-hit measurement require a
follow-up run in an environment where the existing loopback-listener tests can
complete.
