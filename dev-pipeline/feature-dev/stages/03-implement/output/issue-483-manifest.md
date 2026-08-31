# Implementation manifest: #483

Branch: `feat/issue-483-tool-search-essentials`

## Files changed

- `tools.go`: core rewrite of `Registry.SchemasForContext`'s tier-1 composition.
  - `essentialToolNames` (flat, incident-driven, 15 entries) → `floorToolNames` (re-triaged
    down to safety-only: `send_document`, the two composio MCP dispatcher tools — original
    incident-citing comments preserved verbatim) + a separate, mechanically-derived
    frequency tier.
  - New: `refreshFrequencyEssentials(ctx) error` — queries `tool_calls` (30-day rolling
    window via `MINO_TOOL_ESSENTIALS_WINDOW_DAYS`, top-N via `MINO_TOOL_ESSENTIALS_COUNT`),
    swaps the result into `r.frequencyTools` under `r.essentialsMu`. On query failure, keeps
    the last successfully cached set rather than zeroing essentials out.
  - New: `startToolEssentialsRefresher()` — synchronous initial refresh (so a warm restart
    isn't cold on turn one) then a `time.Ticker` (stdlib) on
    `MINO_TOOL_ESSENTIALS_REFRESH_HOURS`, running via the existing `safeGo` panic-safe
    goroutine helper for the process lifetime.
  - New dispatcher tools, mirroring composio's own existing `GET_TOOL_SCHEMAS` +
    `MULTI_EXECUTE_TOOL` shape: `tool_search(name)` → `handleToolSearch` returns a tool's
    description + parameter schema as text (stateless, no session tracking).
    `tool_call(name, args)` → `handleToolCall` validates the name exists and dispatches
    through the existing `ExecuteContext` path — meaning a tool invoked only ever through
    `tool_call` still logs its own real name (not `"tool_call"`) into `tool_calls`, so
    frequency tracking sees real usage regardless of which path reached the tool.
  - New: `deferredToolIndex(selected)` / `renderDeferredToolIndex(entries)` — tier-2
    name+one-line-description index, sourced from the already-populated `tool_catalog_fts`
    table, rendered as a text block (not a schema).
  - Deleted: `searchToolNames`-driven auto-inclusion in `SchemasForContext`, `maxMCPTools`,
    `toolFamilies`, `schemaUnionCap`/`schemaUnion`/`capAt` (the whole per-session capped
    union and its LRU eviction) — no replacement; the sliding window is gone, not resized,
    per the design's explicit rejection of running both mechanisms at once.
- `loop.go`: after the existing once-per-turn `schemas := tools.SchemasForContext(...)`
  (line 329, contract unchanged), appends `renderDeferredToolIndex(tools.deferredToolIndex(...))`
  to `system` — once per turn, same as `schemas`, so it never interacts with the
  cache-preserving once-per-turn computation the design was built around.
- `docs/soul.md`: one line describing `tool_search`/`tool_call` and nudging against
  bash/curl fallback before checking them — the #481/CTX-013 failure pattern by name.
- `composio_essential_tools_test.go`, `tools_essential_test.go`: assertions retargeted from
  `essentialToolNames`/`essentialNamesSorted` → `floorToolNames`/`floorNamesSorted`. Same
  guarantees (composio's two tools and `send_document` always present), same incident
  citations, just checking the new name.
- `tools_test.go`: removed the schema-union eviction tests (`TestSchemaUnionCapEvictsLRU`
  and siblings — the mechanism they tested is deleted). Added
  `TestSchemasForContextFixedTierOneSet` (floor + dispatchers + stage tools present every
  turn regardless of content; deferred tools never appear directly; identical turns produce
  byte-stable output with no eviction left to make it unstable),
  `TestDeferredToolIndexListsUnselectedToolsOnly`, `TestToolSearchReturnsSchemaText`, and
  (my addition during review) `TestRenderDeferredToolIndexNamesEntryAndNudgesToolCall` — the
  latter exists specifically to satisfy `TestPromptAssemblySeamsCovered`'s presence check,
  which matches on test-function-name substring and needed a function actually named after
  the `renderDeferredToolIndex` seam.
- `reminder_test.go`: removed tests that exercised the now-deleted FTS keyword/semantic
  sliding window directly (`TestSchemasForContextKeepsCoreAndRetrievesSpecialist`,
  `TestSchemasForContextUserWordsSurviveSystemPromptWordBudget`, and similar) — the behavior
  under test no longer exists by design.
- `seams_test.go`: registered `renderDeferredToolIndex` in `promptAssemblySeams` (REL-04).
- `osv_validation_test.go` (my fix during review): `TestOSV04ReminderQuestionUsesReminderStoreNotCalendar`
  asserted `list_reminders` gets auto-offered to the model — true under the deleted FTS
  window, no longer true by design once neither floor nor (on a fresh test DB with no
  `tool_calls` history) frequency-derived. Updated the scripted flow to
  `tool_search("list_reminders")` → `tool_call("list_reminders", {})`, registered the two
  dispatcher tools in the test's manually-built registry, and updated assertions to check
  `tc.Args["name"]` for calendar-tool leakage (since the outer call name is now always
  `tool_call`, not the dispatched tool's own name).

## New config keys

| Key | Type | Default |
|-----|------|---------|
| `MINO_TOOL_ESSENTIALS_COUNT` | int | 8 |
| `MINO_TOOL_ESSENTIALS_WINDOW_DAYS` | int | 30 |
| `MINO_TOOL_ESSENTIALS_REFRESH_HOURS` | int | 24 |

Read via `toolEssentialsEnvInt` (new helper): absent or invalid/non-positive → default,
matching the existing `MINO_*` env var convention (config.go).

## Build and test results

- `go build ./...`: PASS.
- `go vet ./...`: PASS (clean).
- `go test ./...`: 849 passed, 0 failed, 1 skipped, in 2 packages. (First run after the
  fork's implementation landed 846 passed / 2 failed — `TestOSV04ReminderQuestionUsesReminderStoreNotCalendar`
  and `TestPromptAssemblySeamsCovered` — both fixed during review, see above; full re-run
  clean after.)
- `gofmt -l`: flags `osv_validation_test.go` among ~18 other pre-existing files repo-wide;
  the specific diff (`gofmt -d`) shows only trailing-comma formatting outside the lines this
  change touched — pre-existing drift, not introduced here.

## Deviations from the design note

One, already reflected in the current (revised) design note rather than left implicit: the
original design's `tool_search`-unlocks-a-schema-slot mechanism was caught as unbuildable
against `loop.go`'s once-per-turn schema contract before any code was written (first fork
attempt stopped cleanly, per stage 03's own rule, rather than improvising around it). The
design was revised to the two-dispatcher (`tool_search` + `tool_call`) shape before
implementation restarted — the code in this manifest builds to that revised contract, not
the original. No other deviations; every interface, config key, and default in the
implementation matches the design note's Interfaces/Config Surface tables.

## Deferred / not covered

- `dev-pipeline/feature-dev/shared/decision-log.md` not yet updated with this decision (stage
  05's territory, or a manual addendum — the pipeline doesn't name an owner for that file
  explicitly; flagging for stage 05).
