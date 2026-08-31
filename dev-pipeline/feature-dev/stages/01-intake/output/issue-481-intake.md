# Intake: composio essentials + stage-tools force-inclusion

Issue: #481. Live incident, 2026-08-31.

## Problem

An Instagram publish stage explicitly declared `MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL` in
its `## Tools` section, but the model bypassed it for `bash`+`curl` directly against
composio's HTTP endpoint. The raw call succeeded (a real post published, verified via the
Graph API), but with no tool call under the declared name, the stage's `## Success`
verification falsely reported failure.

## Root causes

1. **#449's additive stage-tools mechanism is wired to nothing under unified-loop
   navigation.** `stageToolNamesKey` is only ever set by the old dedicated stage loop
   (`runWorkspacePlaybook`); `navigatePlaybookRun` (driving both chat and scheduled
   navigation since #450/#451/#452) never sets it. A stage's declared tools have been
   advisory-only with zero force-inclusion guarantee since #450 shipped.
2. **Composio's two dispatcher tools competed for a capped, relevance-ranked
   sliding-window slot** (`maxMCPTools = 5`) despite composio registering exactly two MCP
   tools total (everything else is a slug argument, not a separate schema) — the same
   failure shape CTX-013 already fixed for `send_document`.

## Decision

1. Add `MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL` and `MCP_composio_COMPOSIO_GET_TOOL_SCHEMAS`
   to `essentialToolNames` — the proven CTX-013 fix pattern, applied here.
2. Restore the general force-inclusion guarantee: `activeStageToolNames` predicts the stage a
   navigating turn will reach (exact via `sessionNav` for a chat continuation, best-effort —
   newest run's current stage, or stage 1 — for a scheduled fire's first call) and threads it
   into `RespondForContext`'s context before `RunLoopContext` starts.

## Non-goals

- No change to `SchemasForContext`'s own additive logic — it already worked correctly, the
  gap was purely that nothing fed it the right input.
- No change to the dedicated stage loop (`runWorkspacePlaybook`) — its own
  `stageToolNamesKey` wiring is untouched and still correct for `schedule_playbook`'s
  historical path (though #452 no longer routes scheduled fires through it).

## Acceptance criteria

1. Both composio tools appear in every turn's tool schema selection, regardless of the
   turn's relevance ranking.
2. A chat turn continuing an active navigation gets the exact current stage's declared
   tools force-included.
3. A scheduled fire's first call gets a best-effort prediction (newest run's stage, or
   stage 1) force-included.
4. A turn with no active navigation is unaffected — `activeStageToolNames` returns nil,
   `SchemasForContext` behaves exactly as before.
5. Full test suite passes.
