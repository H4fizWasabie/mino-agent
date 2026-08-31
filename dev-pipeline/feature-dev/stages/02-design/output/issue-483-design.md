# Design: usage-driven tool essentials + on-demand tool search

## Problem

`Registry.SchemasForContext` (tools.go) guesses which tool schemas a turn needs before the
model has said anything, via a hardcoded `essentialToolNames` set plus an FTS5
keyword/semantic sliding window capped at 20. The guess can be wrong, silently: #481 showed a
stage-declared composio tool losing its slot contest, with the model falling back to
bash+curl and no verifiable tool call. Both prior fixes (CTX-013, #481) were incident-driven
additions to a flat hardcoded list — there is no mechanism for a tool the harness hasn't
already been burned by to be reliably reachable.

## Approach

Two-tier schema selection: a small always-loaded tier (frequency-derived essentials + a
manual safety floor + two fixed dispatcher tools), and a deferred tier (every other tool,
name+description only, reachable through the dispatchers). Rejected alternatives:

- **Raise `schemaUnionCap` / widen `maxMCPTools`** — cheaper to build, but doesn't fix the
  class of bug: any fixed cap still runs a relevance contest a rare-but-critical tool can
  lose. Same failure shape, wider net.
- **Pure frequency-only essentials, no floor** — simplest, matches "usage-driven" most
  literally, but structurally cannot prevent a repeat of #481/CTX-013: a rare-but-critical
  tool will never clear a frequency threshold by definition. Rejected by the owner directly
  during grilling.
- **A `tool_search` that "unlocks" a tool into the turn's live schema array** (original
  design) — rejected after stage 03 caught it against `loop.go:318-329`: schemas are computed
  once per turn and reused unchanged across every iteration specifically to preserve the
  provider's prompt-prefix cache (a prior attempt at per-iteration recomputation was reverted
  for exactly this reason — "iteration 2 cached 64/10671 tokens"). An unlock-based design
  either reopens that cache regression or silently downgrades "callable this turn" to
  "callable next turn," breaking the mechanism's core promise.
- **Recompute schemas mid-turn only on the tool_search path** — narrower version of the same
  regression; still a cache miss on every deferred-tool use, just scoped to fewer call sites.
  Rejected for the same reason as full per-iteration recomputation.

**Chosen: mirror composio's own existing two-tool dispatcher shape**
(`COMPOSIO_GET_TOOL_SCHEMAS` + `COMPOSIO_MULTI_EXECUTE_TOOL`, already proven in this codebase
for hundreds of composio actions without ever putting those actions in the schema array).
`tool_search(name)` looks up a deferred tool and returns its parameter schema as text.
`tool_call(name, args)` validates `args` against that tool's real schema and dispatches to its
actual handler, returning its actual result. Both dispatcher tools have fixed, unchanging
schemas of their own — the turn's `tools` array never mutates mid-turn, so the prompt-cache
problem does not arise; it isn't traded off against, it's structurally avoided.

## Interfaces

| Name | Signature | Purpose |
|------|-----------|---------|
| `toolEssentials` | `type toolEssentials struct { Floor map[string]bool; Frequency map[string]bool }` | Replaces the flat `essentialToolNames` map; two explicit sources instead of one undifferentiated list. |
| `floorToolNames` | `var floorToolNames = map[string]bool{...}` | Re-triaged from today's `essentialToolNames`: `send_document`, the two composio MCP tools. Static, incident-seeded, same authoring pattern as today (a comment per entry naming the incident). |
| `(r *Registry) refreshFrequencyEssentials` | `func (r *Registry) refreshFrequencyEssentials(ctx context.Context) error` | Recomputes the frequency tier from `tool_calls` (30-day window, `SELECT tool_name, COUNT(*) FROM tool_calls WHERE created_at >= ? GROUP BY tool_name ORDER BY COUNT(*) DESC LIMIT ?`) and stores it under `r.schemaMu`. Called once at startup and by a `time.Ticker` (stdlib, no new dependency) on a daily interval. |
| `(r *Registry) deferredToolIndex` | `func (r *Registry) deferredToolIndex(selected map[string]bool) []DeferredToolEntry` | Returns `{Name, Description}` for every registered tool not in `selected`, sourced from `tool_catalog_fts` (existing table), description truncated to one line unless a manual override exists. |
| `DeferredToolEntry` | `type DeferredToolEntry struct { Name, Description string }` | Carries the tier-2 listing into context assembly (rendered as a text block, not a tool schema — no provider tool-calling API has a "name-only" tool concept). |
| `toolSearchDef` | registered like any other tool: `Name: "tool_search"`, one string parameter `name` | Fixed schema, never mutated mid-turn. Looks up a tool (essential or deferred) by exact name and returns its real parameter schema as text, so the model knows what `args` to pass to `tool_call`. |
| `toolCallDef` | registered like any other tool: `Name: "tool_call"`, parameters `name string, args object` (arbitrary passthrough, same shape as composio's `MULTI_EXECUTE_TOOL`) | Fixed schema, never mutated mid-turn. Dispatches to the named tool's real handler. |
| `(r *Registry) handleToolSearch` | `func (r *Registry) handleToolSearch(toolName string) (string, error)` | Validates `toolName` exists in the registry; on success returns the tool's `Description` + JSON-rendered parameter schema as text. On an unknown name, returns a clear error result (no silent no-op, no partial match guessing). Stateless — no session tracking needed since nothing is "unlocked," every call is a fresh lookup. |
| `(r *Registry) handleToolCall` | `func (r *Registry) handleToolCall(ctx context.Context, sessionID, toolName string, args map[string]any) (string, error)` | Validates `toolName` exists and `args` satisfies its real parameter schema (same validation path the direct call would have used); on success invokes the target tool's actual handler and returns its actual result verbatim — the model sees the real tool's output, not a dispatcher-wrapped summary. On an unknown name or invalid args, returns a clear error naming which. |
| `(r *Registry) SchemasForContext` | signature unchanged: `(sessionID, fullCtx, oneTurnText string, stageToolNames ...[]string) []ToolDef` | Internals replaced: selection = `tool_search` ∪ `tool_call` ∪ floor ∪ frequency ∪ `stageToolNames` (unchanged force-inclusion). No session-scoped "unlocked" state at all — deferred tools stay deferred all turn, every turn; they're reached through the dispatchers, never added to the array. `searchToolNames`-driven auto-inclusion, `maxMCPTools`, `toolFamilies`, `schemaUnionCap`/`capAt` eviction are removed — no replacement, the sliding window is gone, not resized. |

## Config Surface

| Key | Type | Default | When absent |
|-----|------|---------|-------------|
| `MINO_TOOL_ESSENTIALS_COUNT` | int | `8` | Frequency tier keeps its default size of 8; no error, no behavior change from today's typical tool count. |
| `MINO_TOOL_ESSENTIALS_WINDOW_DAYS` | int | `30` | Frequency query uses a 30-day rolling window; on a fresh install with less history, the window naturally clamps to whatever `tool_calls` actually contains (no special-case code needed, the `WHERE created_at >= ?` filter is just satisfied by fewer rows). |
| `MINO_TOOL_ESSENTIALS_REFRESH_HOURS` | int | `24` | Frequency tier recomputes once daily; on absence, the ticker still fires at the default interval, only the interval value itself is missing (not the mechanism). |

All three are read once at `Registry` construction; changing them requires a restart (matches
existing config keys like `MINO_DASHBOARD_PORT`), not a live reload.

## Data Flow

1. Turn begins. `SchemasForContext` returns tier 1: `tool_search` + `tool_call` +
   `floorToolNames` + cached frequency set (last refresh, not recomputed per turn) + any
   `stageToolNames`. This set is fixed for the whole turn — computed once, matching
   `loop.go:318-329`'s existing once-per-turn contract exactly, no change to that invariant.
2. Context assembly appends a rendered tier-2 index (`deferredToolIndex`, name + one-line
   description for everything not in tier 1) as a text block in the turn's context — the same
   shape as this very session's own deferred-tool listing.
3. If the model needs a tier-2 tool, it calls `tool_search{name}` to see the real parameter
   schema, then `tool_call{name, args}` to actually run it. Both calls happen as ordinary tool
   calls within the same turn's existing iteration loop — no schema-array mutation, so no
   interaction with the per-turn schema-computation boundary at all.
4. If the model already knows a deferred tool's schema (it called `tool_search` on it earlier
   in this session and the result is still in message history), it can skip straight to
   `tool_call` — this is not a harness-tracked state, it falls out of ordinary conversation
   memory. No `sessionUnlocked`-equivalent bookkeeping is needed; there is nothing to persist
   or expire, which also simplifies the earlier round-4 "does an unlocked tool persist across
   turns" question into a non-question — every `tool_call` is a fresh, independent dispatch.
5. `refreshFrequencyEssentials` runs independently on its own ticker, outside any single
   turn's request path — a turn never blocks on a frequency recomputation.

## Failure Behaviour

| Failure | Behaviour |
|---------|-----------|
| `tool_search` called with an unknown/misspelled name | Clear "no tool named X" error result — never a silent no-op or empty success. Does not attempt fuzzy/closest-match suggestions (unneeded complexity; the deferred index text block already gives the model every real name). |
| `tool_call` called with an unknown name | Clear "no tool named X" error result, same as `tool_search`. |
| `tool_call` called with `args` that don't satisfy the target tool's real schema | Clear validation error naming the missing/invalid field — the same failure the model would see calling the tool directly, not a generic dispatcher-level rejection. |
| `tool_call` used to invoke a tool already reachable directly in tier 1 (e.g. `tool_call{name: "read_file", ...}`) | Allowed, not blocked — dispatches through to the same handler either path would reach. No special-casing needed; simpler than rejecting a functionally harmless redundant path. |
| `refreshFrequencyEssentials` query fails (DB error, e.g. mid-migration) | Keeps the last successfully cached frequency set; logs the error; does not fall back to empty essentials (that would silently strand the model with only the floor + dispatchers). |
| `tool_calls` table has zero rows (fresh install) | Frequency tier is empty; floor + dispatchers still fully functional. Not a failure state, just a cold-start condition that self-corrects as usage accrues. |
| Loop cancelled mid-`tool_call` dispatch | Cancellation propagates through `ctx` to the target handler exactly as it would on a direct call — `tool_call` adds a lookup+validate step before invoking the real handler, no new cancellation surface. |

## Invariant Check

| Invariant | Verdict | Note |
|-----------|---------|------|
| Model Agnosticism | Held | `tool_search`/`tool_call` are normal registered tools with normal, fixed schemas; the tier-2 text index is assembled harness-side and injected into context, not a provider-specific API feature. Works identically on any provider Mino's adapter supports. |
| Loop Termination | Held | No new loop introduced. `refreshFrequencyEssentials`'s ticker is a background goroutine, not a request-path loop; it has no termination condition by design (runs for the process lifetime), same shape as existing schedulers. |
| Context Is Managed, Never Assumed | Held | Tier-2 index is inherently smaller than today's up-to-20-schema union (~72 names+one-liners is smaller than 20 full schemas); frequency tier is fixed-size by config, not proportional to conversation length. Turn's schema array stays fixed-size all turn (essentials + two dispatchers), so no growth path exists at all, unlike the original design's now-rejected unlock mechanism. |
| Guardrails Are Not Optional | Held | No guardrail logic lives in `SchemasForContext` today or in this design — not a surface this change touches. |
| Failure Is Explicit | Held | See Failure Behaviour table; every path (unknown name, invalid args, DB error, cold start) has a stated, non-silent behaviour. |
| State Stays Local and Inspectable | Held | Frequency data reads from `tool_calls` (existing local SQLite table). No new state at all beyond that — the redesign eliminates the original `sessionUnlocked` in-memory map entirely, since `tool_call` needs no unlock step to dispatch. |
| Single Binary, No Framework | Held | `time.Ticker` is stdlib. No new dependency. |

## Files to Touch

- `tools.go`: `SchemasForContext`, `essentialToolNames` → `floorToolNames` + frequency tier,
  new `tool_search`/`tool_call` registration and handlers, deletion of `searchToolNames`
  auto-inclusion call sites, `maxMCPTools`, `toolFamilies`, `schemaUnionCap`/`sessionSchemas`/
  `capAt`.
- New: frequency computation and ticker (likely a new small file, e.g. `tool_essentials.go`,
  or added to `tools.go` if small enough — stage 03's call).
- `playbook_nav.go`: no logic change; `activeStageToolNames`'s output continues to compose
  into `SchemasForContext` as `stageToolNames`.
- Context assembly (wherever `fullCtx`/system prompt is built — the exact call site, likely
  near `BuildContext`, session.go): inject the tier-2 `deferredToolIndex` text block.
- `docs/soul.md` + provisioned `SOUL.md` template: new short paragraph covering both
  `tool_search` and `tool_call` as a pair (search-then-call), not just one meta-tool.
- `db.go`: no schema change — `tool_calls` and `tool_catalog_fts` already have what's needed.
- Tests: `composio_essential_tools_test.go` (#481) adapted to floor-tier composition;
  `tools_test.go` updated for the new `SchemasForContext` internals; new tests for
  `tool_search` (unknown name), `tool_call` (unknown name, invalid args, successful dispatch
  returning the real handler's real result), and `refreshFrequencyEssentials` (cold-start
  empty table, DB-error-keeps-stale-cache).

## Out of Scope

- No distinct coding-task mode (confirmed not needed at intake).
- No change to `activeStageToolNames`'s own prediction logic.
- No shadow-run/feature-flag gating phase — ships as the mechanism directly, per owner
  instruction.
- No live-reload of the three new config keys — restart-only, matching existing config keys.
- No persistence of `sessionUnlocked` across restarts.
