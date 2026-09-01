# Intake: remove dead ponytail-audit paths

## Problem

Mino carries multiple implementations for paths that production no longer calls, so
maintainers must read and test obsolete playbook execution, provider streaming, and helper
surfaces alongside the canonical runtime.

## Who is affected

Mino maintainers hit this during every change touching playbooks, providers, tools, or
memory. Users do not currently receive a new capability from these paths; the cost is
maintenance surface, misleading tests, and higher regression risk.

## Smallest change

Delete only the confirmed-unreachable implementations and test-only API surface, and
delegate the duplicated MCP startup path to its existing reload path. Keep the live
playbook navigator and scheduler, the SSE HTTP endpoint, provider request behavior,
guardrails, and accepted dependencies unchanged.

## Growth risk and scope line

This could grow into a broad provider rewrite or playbook redesign. It stops at dead-code
removal and duplicate-path consolidation identified by the audit; no new architecture,
provider behavior, or dependency change is included.

## Rejection check

Not on the Do Not Build list. The change reduces code while preserving the single binary,
model-agnostic adapter boundary, and existing extension boundary.

## Surfaces touched

- Playbook execution entry points and their obsolete tests.
- LLM client and provider streaming-only interfaces and plumbing.
- MCP bridge startup/reload behavior.
- Tool registry test-only filtering helper.
- Memory sorting helper duplication.
- One obsolete compile-time time sentinel.

## Acceptance Criteria

1. `go test ./...` passes after the obsolete dedicated runner and unreachable streaming
   surfaces are removed.
2. The production call graph still reaches playbooks through `NavigatePlaybookRun` or
   `NavigateScheduledPlaybook`, and no production caller depends on a removed surface.
3. Live model requests continue through the context-aware provider path, with no changed
   user-visible response behavior in existing tests.
4. `MCPBridge.Start` has the same observable server state as `Reload` without maintaining
   a second scan implementation.
5. Existing tool and memory tests pass without the removed test-only or duplicate helpers.
6. No new dependency, config key, endpoint, or persistent state is introduced.

## Out of scope

- Releasing, pushing, merging, tagging, deploying, or changing live state.
- Reworking the canonical playbook navigator or scheduler.
- Reintroducing provider streaming or changing the SSE endpoint contract.
- Removing the dashboard universe rollback endpoint or other intentionally retained
  compatibility paths.
