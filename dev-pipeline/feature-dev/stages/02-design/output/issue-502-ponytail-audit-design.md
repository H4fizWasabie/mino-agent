# Design: remove dead ponytail-audit paths

## Problem

Mino carries multiple implementations for paths that production no longer calls, so
maintainers must read and test obsolete playbook execution, provider streaming, and helper
surfaces alongside the canonical runtime.

## Approach

Delete the unreachable dedicated playbook runner, its private seam, and tests that exist only
to exercise that runner; keep coverage that belongs to shared helpers or the canonical
navigator. Reduce the LLM boundary to the context-aware request used by the loop, remove the
unreachable OpenAI/Anthropic streaming path and its unused boolean plumbing, and preserve
Codex's internal SSE response parsing. Collapse the MCP startup duplicate into `Reload`,
remove the test-only registry filter and duplicate memory sorter, and delete the obsolete
time sentinel.

Rejected approaches: keep dead paths for hypothetical callers (permanent maintenance cost);
move them behind build tags (more surface and no production need); redesign the navigator or
provider adapters (not required to remove unreachable code).

## Interfaces

| Name | Signature | Purpose |
|------|-----------|---------|
| `LLMClient` | `CreateContext(context.Context, string, ModelRole, []Message, int, string, []ToolDef) (*LLMResponse, error)` | The sole model-call operation required by the loop. |
| `RunLoop` | `RunLoop(LLMClient, string, string, []Message, *Registry, int, int, Observer, string) *LoopResult` | Run a loop without the unused stream selector. |
| `RunLoopContext` | `RunLoopContext(context.Context, LLMClient, string, string, []Message, *Registry, int, int, Observer, string) *LoopResult` | Context-aware loop entry point without the unused stream selector. |
| `Core.Respond` | `Respond(string, string, Observer) *LoopResult` | Standard turn entry point. |
| `Core.RespondFor` | `RespondFor(string, string, string, Observer, ...string) *LoopResult` | Session-aware turn entry point. |
| `Core.RespondForContext` | `RespondForContext(context.Context, string, string, string, Observer, ...string) *LoopResult` | Context-aware turn entry point. |
| `MCPBridge.Start` | `Start()` delegates to `Reload()` | One startup scan implementation. |

`ProviderManager.CreateJSON` remains the live background JSON operation. `ProviderManager`
and `Client` no longer expose unused non-context or streaming wrappers. No new public
interface, endpoint, or config key is added.

## Config Surface

| Key | Type | Default | When absent |
|-----|------|---------|-------------|
| None | — | — | Existing defaults and provider configuration remain unchanged. |

## Data Flow

The loop calls `LLMClient.CreateContext`; `ProviderManager.CreateContext` routes the request
to the selected client; the client performs the existing non-streaming OpenAI or Anthropic
request, or the existing Codex Responses SSE exchange. Playbook calls enter the navigator or
the scheduler's normal loop. MCP startup and reload both use the same config scan/connect
path. Memory community labeling uses the existing generic integer-key sorter.

## Failure Behaviour

| Failure | Behaviour |
|---------|-----------|
| Provider timeout or transport error | The existing error reaches the loop and is reported through its existing result status; no retry is added by this change. |
| Malformed provider response | The existing provider parser returns an error; the loop surfaces it according to its current error handling. |
| Context cancellation | The context-aware provider request observes cancellation; the loop returns its existing cancelled result. |
| Loop iteration bound | The existing hard iteration ceiling remains unchanged and terminates the loop. |
| Missing or invalid MCP config | The existing scan skips unreadable or invalid entries; startup remains available for valid entries. |
| Missing playbook or invalid stage contract | The canonical navigator returns the existing error and does not invent a replacement run. |

## Invariant Check

| Invariant | Verdict | Note |
|-----------|---------|------|
| Model Agnosticism | Held | The remaining loop interface is provider-neutral and all provider differences stay in adapters. |
| Loop Termination | Held | The existing loop ceiling and navigator stage bounds remain; only an unused selector is removed. |
| Context Is Managed, Never Assumed | Held | Context-aware calls and existing context budgeting are unchanged. |
| Guardrails Are Not Optional | Held | Tool registry, approvals, and navigator boundaries are preserved; no alternate execution path is introduced. |
| Failure Is Explicit | Held | Existing provider, cancellation, parser, and MCP failure handling remains; deleted wrappers cannot swallow failures. |
| State Stays Local and Inspectable | Held | No state format, storage location, or persistent record changes. |
| Single Binary, No Framework | Held | No dependencies, runtime, or framework are added. |

## Files to Touch

- `app.go`, `loop.go`, `main.go`, `telegram.go`, `dashboard.go`, `eval.go`, `skill.go` — remove the unused stream argument at the turn/loop boundary and update callers.
- `provider.go`, `provider_manager.go`, `codex.go` — remove unreachable streaming wrappers/parsers and simplify the live request signatures while retaining Codex's required SSE decoder.
- `playbook.go`, `playbook_workspace.go`, `playbook_test.go`, `playbook_script_test.go` — remove the dedicated runner and runner-only seam/tests; keep navigator and shared stage coverage.
- `mcp.go`, `tools.go`, `memory.go` — collapse or delete duplicate/test-only helpers.
- Affected test files identified by the compiler and call-site search — update fakes and calls to the reduced interfaces, without adding replacement compatibility wrappers.
- `CHANGELOG.md` and stage outputs — record the user-visible maintenance change and pipeline evidence.

## Out of Scope

- Releasing, pushing, merging, tagging, deploying, or changing live state.
- Reworking the canonical playbook navigator, scheduler, SSE HTTP endpoint, or Codex transport.
- Changing provider request semantics, retry policy, guardrails, config keys, dependencies, or persistent state.
