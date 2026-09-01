# Design: session-sticky upstream routing (#509)

## Problem

Cost-watch supplies a ranked upstream list, but Mino does not retain the
upstream that served a session. OpenRouter manual ordering then defeats its
automatic sticky routing, fragmenting prompt caches across providers.

## Approach

Chosen: deepen the existing `ProviderManager` seam with an in-memory,
session-scoped upstream preference, while the provider adapter reports the
selected upstream. Cost-watch remains the ranking source.

Rejected: make cost-watch emit one provider only (improves cache locality but
removes the intended fallback protection); remove cost-watch ordering entirely
(gives up price/privacy/quality policy); enable complete response caching
(unsafe for autonomous tool calls).

## Interfaces

| Name | Signature | Purpose |
|------|-----------|---------|
| Session upstream preference | private manager state keyed by session, role, and model | Remember the last successful eligible upstream without persistent state |
| Ordered upstream selection | private manager helper receiving a provider config, session, role, and current candidates | Put the remembered eligible upstream first and preserve the ranked remainder |
| Upstream invalidation | private manager helper receiving session, role, and provider | Clear the preference before retry/failover after a request failure |
| Provider response attribution | `LLMResponse.UpstreamProvider string` | Carry optional adapter-reported upstream identity to the manager and usage log |
| Router metadata adapter | provider-specific optional response metadata parsing behind the OpenRouter-compatible adapter | Learn the selected upstream without exposing provider details to callers |

The public provider and tool interfaces do not change. Callers still ask for a
model response and never need to know which upstream served it.

## Config Surface

No new configuration keys. Existing cost-watch output and provider routing
configuration remain authoritative. If metadata or routing is absent, Mino
keeps current behaviour and records an empty upstream identity.

## Data Flow

```text
cost-watch ranked provider_routing
        ↓ reload
ProviderManager current candidates + session preference
        ↓ preference first, eligible fallbacks retained
OpenRouter adapter request with session_id and ordered candidates
        ↓ optional router metadata
LLMResponse upstream identity → preference + usage attribution
```

The preference is process-local. A restart forgets it, allowing a fresh cache
warm-up without stale state. A cost-watch reload does not erase it unless the
preferred upstream disappears from the current list.

## Failure Behaviour

| Failure | Behaviour |
|---------|-----------|
| Provider timeout or transport error | Clear the session preference before retrying/failing over; preserve existing retry bounds. |
| Malformed JSON or tool-call response | Treat as a failed upstream attempt, clear the preference, and try the next allowed path. |
| Valid but semantically poor prose | Return it unchanged; semantic quality scoring is out of scope. |
| Router metadata absent or malformed | Keep the response; leave upstream attribution empty and do not alter routing safety. |
| Preferred upstream removed by cost-watch | Discard the stale preference and use the current ranked list. |
| Context cancellation | Stop immediately; do not write a new preference or partial attribution. |
| Provider list exhausted | Return the existing aggregate provider error with no sticky preference. |
| Usage schema migration unavailable | Preserve request success; log attribution failure explicitly rather than dropping usage. |

## Invariant Check

| Invariant | Verdict | Note |
|-----------|---------|------|
| Model agnosticism | Held | Upstream metadata and ordering live behind the provider adapter; the manager uses opaque names supplied by configuration. |
| Loop termination | Held | No new unbounded loop; existing retry and candidate bounds remain. |
| Context is managed | Held | Routing state does not add prompt content; stable prompt construction is unchanged. |
| Guardrails are not optional | Held | No safety boundary is bypassed; failure invalidation happens inside provider selection. |
| Failure is explicit | Held | Timeout, malformed response, cancellation, metadata failure, and exhaustion are defined above. |
| State stays local and inspectable | Held | Preference is in-memory; upstream attribution is stored in the existing local usage database. |
| Single binary, no framework | Held | Uses existing Go types, HTTP parsing, and SQLite; no dependency or process is added. |

## Files to Touch

- `provider_manager.go`
- `provider.go`
- `db.go`
- `dashboard.go` (only if usage attribution is exposed by the existing response)
- `provider_manager_test.go`
- `provider_test.go`
- `db_test.go` or existing usage tests
- `CHANGELOG.md`
- feature-pipeline output manifests/reports

## Out of Scope

- Cost-watch ranking policy and its provider eligibility rules.
- Semantic output-quality detection.
- OpenRouter response caching or replay.
- Persistent session routing state.
- Release, VPS configuration, publication, or deployment.
