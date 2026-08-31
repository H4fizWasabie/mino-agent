# Implementation manifest: #481

Branch: `fix/issue-481-composio-essential-tools`

## Files changed

- `tools.go`: added `MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL` and
  `MCP_composio_COMPOSIO_GET_TOOL_SCHEMAS` to `essentialToolNames`, following the exact
  CTX-013/`send_document` precedent already in the same map.
- `playbook_nav.go`: added `activeStageToolNames(home, source, sessionID) []string` —
  predicts the stage a navigating turn will reach (exact via `sessionNav` for a chat
  continuation using `loadPlaybookRunByID`, best-effort — newest run's stage or stage 1 —
  for a scheduled fire's first call using `latestPlaybookRun`).
- `app.go`: `RespondForContext` calls `activeStageToolNames` and, when non-empty, sets
  `stageToolNamesKey` on `ctx` before `RunLoopContext` runs.
- `composio_essential_tools_test.go` (new): essentials-membership check, end-to-end
  `SchemasForContext` proof composio tools appear regardless of turn relevance,
  `activeStageToolNames` exact/best-effort/nil cases, and an end-to-end
  `RespondForContext` proof (using a deliberately non-composio tool name, to keep this
  fix isolated from the essentials fix in the same test) that a navigating turn's request
  payload actually contains the force-included stage tool.

## Build and test results

- `go build ./...`: PASS.
- New tests (6): PASS. Confirmed to fail to even compile against the pre-fix code
  (`activeStageToolNames` undefined) via `git stash`, then pass cleanly restored.
- Full suite: see `../04-verify/output/issue-481-verification.md`.

## Not merged yet

Per the owner's instruction: implemented, tested, and pushed for review, but not opened as
a mergeable PR / not merged — the owner wants to look for other related gaps first.
