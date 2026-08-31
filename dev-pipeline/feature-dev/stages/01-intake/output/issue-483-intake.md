# Intake: usage-driven tool essentials + on-demand tool search

Issue: #483. Grilled with the owner, 2026-08-31, over four rounds. Follows #481 (composio
essentials fix), which this issue's own origin note says it grew out of.

## Problem

Mino's tool-schema selection (`Registry.SchemasForContext`, tools.go) is a harness-side guess
at what a turn will need, made before the model has said anything: a hardcoded
`essentialToolNames` set, an FTS5 keyword/semantic sliding window capped at 20
(`schemaUnionCap`), and a `maxMCPTools = 5` sub-cap. The guess can be wrong, silently — #481
showed a stage-declared composio tool losing its slot contest and the model falling back to
bash+curl straight at composio's HTTP endpoint, with no tool call the harness could verify
against the stage's declared outcome. The same failure shape (model reaches for bash+curl when
it believes no native tool exists) was already observed once before, CTX-013, with
`send_document` and a bot token leaking into a raw URL — a security-relevant symptom, not just
a UX one. Both prior fixes were incident-driven: add the specific tool to
`essentialToolNames` after the fact. There is no mechanism today for a tool the harness hasn't
already been burned by to be reliably reachable.

## Rejection check

Checked against `shared/decision-log.md`'s "Do Not Build" list (provider-specific feature
flags; embedding a scripting runtime in core) — no match, does not apply.

## Decision

Replace the guessing mechanism with a two-tier system modeled on Claude Code's own deferred-tools
pattern (`ToolSearch`), which the owner confirmed live as the direct model for this design:

1. **Tier 1 — essentials, full schema always loaded, unconditionally present.** Composed of
   two parts, not one:
   - **Frequency-derived slots**: computed from real `tool_calls` table data (session_id,
     tool_name, created_at — confirmed the only real per-invocation usage log; `usage_log` is a
     separate LLM-call cost/token table, not tool usage, despite the issue naming both).
     30-day rolling window, clamped to since-install if the DB is younger. Recomputed
     periodically (daily/on-startup), not queried fresh per turn.
   - **Safety floor**: a small, deliberately kept-small set of tools pinned regardless of
     frequency, for the specific case frequency ranking structurally cannot cover — a
     rare-but-critical tool, by definition, will not clear a usage-frequency threshold. Seeded
     by re-triaging today's 15-entry `essentialToolNames` (tools.go:236-269): `send_document`
     and the two composio MCP dispatcher tools stay on the floor (floor-because-safety —
     exactly the incidents that got them added); the rest (`read_file`, `bash`, `search_web`,
     etc.) compete for frequency slots normally rather than being hardcoded, since usage data
     will confirm their inclusion anyway.
   - `tool_search` itself is unconditionally pinned, full-schema, and does not count against
     either the frequency or floor budget — it's the mechanism, not a regular tool.
2. **Tier 2 — everything else, name + short description only, visible but not callable.** A
   `tool_search` meta-tool fetches a specific deferred tool's full schema on demand, making it
   callable for the rest of that turn. The discoverability index this needs already
   substantially exists: `tool_catalog_fts` (db.go:124, populated tools.go:545-550) stores
   `name, description, keywords` per tool from each tool's real `Description`, already used by
   today's sliding window. Truncate-and-reuse is the default source for tier-2 one-liners; any
   tool whose real description doesn't make its purpose clear out of context in one line
   (composio's dispatcher is the known case: "pass a tool_slug + arguments" doesn't say
   "this is how you post to Instagram") gets a manual override description instead of the
   truncated original.

**Composition with #481's stage-declared-tools force-inclusion (`activeStageToolNames`,
playbook_nav.go:140-172): kept, not replaced.** A navigating stage's declared tools stay
force-included as full schema, unconditionally, exactly as #481 shipped it — layered under the
new mechanism rather than deleted. `tool_search` solves the general discoverability problem for
tools nobody thought to hardcode; it does not need to also carry the one case where the harness
already has certain (chat continuation) or high-confidence (scheduled fire) knowledge of what's
needed. Deleting that guarantee the same day it shipped, in favor of betting tool descriptions
are always good enough, was explicitly rejected.

**The existing FTS5 keyword sliding window, `maxMCPTools = 5`, and `toolFamilies` completion
logic are deleted, not kept alongside the new mechanism.** They were harness-side guessing at
exactly what `tool_search` now lets the model ask for directly; running both would mean two
competing answers to "what tools does this turn get," which is more complexity, not less, and
contradicts the issue's own framing of "replace" rather than "add to."

**A tool fetched via `tool_search` persists session-sticky (multiple turns), not single-turn
expiry.** A tool needed once in a task (e.g. mid-playbook-stage) is very likely needed again a
turn or two later; re-paying the search tax every single turn for a tool just fetched defeats
the accepted "one round-trip per new tool" cost model. This means a slimmed-down version of
today's session union survives (tracking turn-scoped additions), but without the 20-item cap or
eviction machinery — the essentials are now fixed-size, so there's no longer a variable-size
selection that needs bounding.

**Extra round-trip cost is an accepted tradeoff, not something to tune away.** Owner's words:
"i know its an extra call, but it enforces efficiency." No shadow-run or gating period requested
before global rollout — ships as the mechanism, across chat and playbook turns alike, on release.

**SOUL.md gets one short paragraph**, not a new section: what `tool_search` is, that Mino
should reach for it when a task needs a capability not currently visible, and an explicit nudge
against the observed failure pattern — checking `tool_search` before falling back to bash/curl
against an external API a native tool might already cover. SOUL.md (`<MINO_HOME>/SOUL.md`,
seeded from `docs/soul.md`, loaded into every turn via `loadSoul`/`BuildContext`,
session.go:86-93/107) currently has no tool-selection content to extend — this is new content,
global by construction since SOUL.md is unconditionally in every turn's system prompt.

## Non-goals

- No distinct "coding task" mode or tool subset. Investigated and confirmed: Mino has no
  separate coding-agent mode (`coding_tools.go` just registers coding-flavored tools in the
  same registry chat and playbooks already use). The owner's "coding task" concern was general
  small requests (HTML generation, code review, small cross-codebase changes) — these are
  ordinary chat turns and fold fully into the chat-turn design above, nothing structurally
  separate to build.
- No change to `activeStageToolNames`'s own prediction logic (exact-via-sessionNav /
  best-effort-via-newest-run fallback) — #481 already got this right, this issue only changes
  what it composes with.
- No shadow-run or feature-flag gating phase before global rollout (explicitly declined by the
  owner in favor of shipping the mechanism directly).

## Surfaces touched

- `tools.go`: `SchemasForContext`, `essentialToolNames` (becomes frequency+floor composition
  instead of a flat hardcoded map), `searchToolNames`/`maxMCPTools`/`toolFamilies` (deleted),
  `schemaUnionCap`/`sessionSchemas`/`capAt` (replaced by unbounded session-sticky tracking, no
  eviction).
- New: a `tool_search` meta-tool registration and handler (fetches one tool's full `ToolDef` by
  name, adds it to the session-sticky set).
- New: a background/startup job computing the 30-day frequency ranking from `tool_calls`.
- `playbook_nav.go`: `activeStageToolNames` composition point in `SchemasForContext` — no logic
  change, just confirms it still force-includes ahead of/alongside the new tiers.
- `docs/soul.md` (checked-in template) and any already-provisioned `SOUL.md` files: new
  tool_search paragraph.
- `tool_catalog_fts` (db.go:124): read path for tier-2 descriptions; may need per-tool override
  descriptions for non-self-explanatory tools (composio-shaped cases).

## Acceptance criteria

1. A turn's tool schema selection contains exactly: pinned `tool_search`, frequency-derived
   essentials (30-day `tool_calls` window), the safety floor, plus any force-included active
   stage tools — no FTS5 keyword/semantic auto-inclusion, no MCP cap, no family completion.
2. Calling `tool_search` with a valid non-essential tool name returns that tool's full schema
   and makes it callable for the remainder of the turn, and for subsequent turns in the same
   session without re-searching.
3. Calling `tool_search` with an unknown/misspelled name fails clearly (no silent no-op).
4. The composio dispatcher tools and `send_document` remain always present (floor membership),
   reproducing #481's fix under the new mechanism.
5. A navigating stage's declared tools are still force-included as full schema on the turn that
   reaches that stage, matching #481's existing guarantee.
6. SOUL.md's new paragraph is present in the assembled system prompt on every turn (chat and
   playbook alike).
7. Full test suite passes, including `composio_essential_tools_test.go` (#481) adapted to the
   new essentials composition.

## Open items for stage 02 (design)

None outstanding from the grilling session — all five of the issue's original considerations,
plus the four round-4 threads (essentials split mechanics, tool_search's own pinned status,
deletion of the old sliding-window code, and session-stickiness of fetched schemas) were put to
the owner directly and resolved. Stage 02 should treat this file as complete input, not a
partial scope.
