# Verification: #483

Branch: `feat/issue-483-tool-search-essentials`

## Test results

- `go build ./...`: PASS.
- `go vet ./...`: PASS (clean).
- `go test ./...`: 849 passed, 0 failed, 1 skipped, 2 packages.
- New/changed tests exercising this change specifically: `TestSchemasForContextFixedTierOneSet`,
  `TestSchemasForContextAddsStageCapabilities`, `TestDeferredToolIndexListsUnselectedToolsOnly`,
  `TestRenderDeferredToolIndexNamesEntryAndNudgesToolCall`, `TestToolSearchReturnsSchemaText`,
  `TestToolCallDispatchesToRealHandler`, `TestRefreshFrequencyEssentialsColdStartAndStaleCacheOnError`,
  `TestSendDocumentIsEssential`, `TestSchemasForContextAlwaysIncludesComposioEssentials`,
  `TestNavigatingTurnForceIncludesDeclaredStageTool`, `TestOSV04ReminderQuestionUsesReminderStoreNotCalendar`
  — all PASS.

## Acceptance criteria (from intake) — observed behaviour

1. **A turn's tool schema selection contains exactly: pinned dispatchers, frequency
   essentials, floor, plus force-included active stage tools — no FTS keyword/semantic
   auto-inclusion, no MCP cap, no family completion.** Observed in
   `TestSchemasForContextFixedTierOneSet`: turn text mentioning `special_101`..`special_104`
   by name does not add them to the schema array (the old explicit-mention auto-inclusion
   path is gone); only `floorNamesSorted`, `tool_search`, and `tool_call` appear. Two
   identical turns produce byte-identical schema arrays — no session-churn eviction survives
   to make it unstable.
2. **Calling `tool_search` with a valid non-essential tool name returns that tool's full
   schema; `tool_call` makes it callable.** Observed in `TestToolSearchReturnsSchemaText`
   (returns description + JSON parameter schema as text) and
   `TestToolCallDispatchesToRealHandler` (dispatches to the real `Fn`, `called` flips true,
   the real handler's real return value comes back verbatim — `"made 3 widgets"`, not a
   dispatcher-wrapped summary).
3. **Calling `tool_search`/`tool_call` with an unknown/misspelled name fails clearly.**
   Observed in both tests above: `does_not_exist` → `"no tool named ..."` for each dispatcher;
   an empty name → an `"Error"`-prefixed message. No silent no-op in any case.
4. **The composio dispatcher tools and `send_document` remain always present (floor
   membership), reproducing #481's fix under the new mechanism.** Observed in
   `TestSendDocumentIsEssential` and `TestSchemasForContextAlwaysIncludesComposioEssentials` —
   both retargeted from `essentialToolNames` to `floorToolNames`, same assertions, same
   pass/fail behaviour as before #483.
5. **A navigating stage's declared tools are still force-included as full schema on the turn
   that reaches that stage, matching #481's existing guarantee.** Observed in
   `TestNavigatingTurnForceIncludesDeclaredStageTool` — unchanged from #481, still passes
   against the new tier-1 composition.
6. **SOUL.md's new paragraph is present in the assembled system prompt on every turn.**
   Observed by inspection: `docs/soul.md` diff adds the `tool_search`/`tool_call` line inside
   the "Working Discipline" section, which `loadSoul`/`BuildContext` already includes
   unconditionally in every turn's system prompt (session.go:86-93/107, unchanged by this
   PR) — no conditional wrapping was added around the new line.
7. **Full test suite passes.** See Test results above.

Additionally, the redesign's own guarantee (real per-tool usage still gets logged when a
tool is only ever reached via `tool_call`, load-bearing for the frequency mechanism to work
at all) was verified in `TestRefreshFrequencyEssentialsColdStartAndStaleCacheOnError`: a row
inserted directly into `tool_calls` for `"widget"` is picked up by
`refreshFrequencyEssentials` exactly as a real dispatch would produce it (`handleToolCall` →
`ExecuteContext` → the same `INSERT INTO tool_calls (..., tool_name, ...)` path every direct
tool call already used, unchanged by this PR).

## Invariants — held / evidence

| Invariant | Verdict | Evidence |
|---|---|---|
| Model agnosticism | Held | `ToolDef{Name, Description, Parameters}` (provider.go:482) is unchanged — `tool_search`/`tool_call` are ordinary entries in `[]ToolDef`, no new field types. Grepped `provider.go`/`provider_manager.go` for any tool-name-specific branching: none exists; the only per-provider fork (`isAnthropic()`) is blind to tool identity. Same generic conversion path handles the new dispatchers as every existing tool. |
| Loop termination | Held (unaffected) | No new loop. `startToolEssentialsRefresher`'s ticker is a background goroutine via the existing `safeGo` helper, same shape as other long-lived reconcilers already in the codebase, not a request-path loop. |
| Context is managed, never assumed | Held | Tier-1 is fixed-size by config (`MINO_TOOL_ESSENTIALS_COUNT`, default 8) plus a small floor, not proportional to conversation length or tool catalog size. Tier-2 index is name+one-line-description text, smaller per-tool than a full schema. |
| Guardrails are not optional | Held | No guardrail logic lives in `SchemasForContext` before or after this change — not a surface this change touches. |
| Failure is explicit | Held | Every new path has a forced, observed, non-silent behaviour — see Failure paths forced below. |
| State stays local and inspectable | Held | Frequency data reads from the existing local `tool_calls` SQLite table. No new persisted state; `r.frequencyTools` is in-memory, rebuilt from the same local table on the next refresh after a restart. |
| Single binary, no framework | Held | `time.Ticker` (stdlib) is the only new mechanism; no new dependency. `go.mod` unchanged (not diffed by this PR). |

## Failure paths forced

- `tool_search`/`tool_call` called with an unknown name → clear `"no tool named %q"` error,
  not a silent no-op — forced directly in `TestToolSearchReturnsSchemaText`/
  `TestToolCallDispatchesToRealHandler`.
- `tool_call` called with args missing a required field → validation error surfaced the same
  way a direct call's validation would — forced in `TestToolCallDispatchesToRealHandler`.
- `tool_calls` table empty (cold start) → frequency tier empty, refresh itself does not error
  — forced in `TestRefreshFrequencyEssentialsColdStartAndStaleCacheOnError`.
- `refreshFrequencyEssentials` query fails (closed DB) → error returned, but the
  last-good cache is kept rather than zeroed — forced in the same test (closes the DB mid-test,
  confirms `widget` survives in `r.frequencyTools` after the failed refresh).

## Provider parity

Not run as a live dual-provider call (no owner-authorized spend for this verification pass).
Settled by inspection instead, per the Model Agnosticism row above: `tool_search`/`tool_call`
are plain `ToolDef` entries flowing through the same generic, provider-blind conversion path
every other tool already uses; no provider-specific code was added or touched. This is the
same class of change as #453's read_file nudge (also "not applicable" on provider parity for
the same structural reason — no new provider-facing branch), just verified here with an
explicit grep for tool-identity branching rather than asserted by absence of touched files.

## Open concerns (carried to the ship note)

1. **No production frequency data yet.** `MINO_TOOL_ESSENTIALS_COUNT`/`_WINDOW_DAYS` defaults
   (8, 30) are the grilling session's stated numbers, not tuned against real `tool_calls`
   volume — the owner explicitly declined a shadow-run period before shipping, so this is a
   live-tuning risk accepted going in, not a gap in this verification pass.
2. **Deferred-tool round-trip tax is now uniformly 2 calls (`tool_search` then `tool_call`)
   for every use, with no session-level "already searched" state** — the redesign (composio
   dispatcher shape) traded the original design's session-sticky unlock for eliminating the
   cache-breaking bug entirely. A model that re-searches a tool it used two turns ago pays
   the tax again unless it recalls the schema from its own conversation history. This is a
   known, accepted shape (see design note's Data Flow step 4), not a defect, but worth
   watching in real usage for whether it under- or over-taxes common deferred-tool patterns.
3. **`osv_validation_test.go`'s OSV-04 reminder scenario now requires the scripted client to
   emit two tool calls instead of one** — correct given the removed auto-inclusion, but any
   other test relying on the old single-call "the FTS window found it for me" pattern against
   a genuinely deferred (non-floor, non-frequency) tool would need the same two-step update.
   Full suite is green, so none currently do, but future test authors should know the pattern
   changed.
