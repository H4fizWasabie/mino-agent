## Resolution

**Keyword-gate:** MCP/extension tools excluded from semantic matching entirely. They only enter via keyword FTS5 search against the one-turn window (last user message + last assistant reply).

**Cap:** max 3 MCP tools per request as safety net.

**Rationale:** MCP tools map to specific platforms (instagram, github, gmail, threads, reddit, facebook, youtube). The user installed composio to connect Mino to those platforms, so keyword presence in the user's message is the correct signal. Semantic embedding is the wrong tool here — it produces false positives ("ping" matching COMPOSIO_WORKBENCH) because MCP descriptions are massive and share generic vocabulary.

## Context

Top 6 schema space hogs are all MCP/composio tools:

| Tool | Est. schema chars |
|------|-------------------|
| COMPOSIO_REMOTE_WORKBENCH | 9,023 |
| COMPOSIO_SEARCH_TOOLS | 4,558 |
| COMPOSIO_MULTI_EXECUTE_TOOL | 2,389 |
| context7_resolve-library-id | 2,252 |
| COMPOSIO_MANAGE_CONNECTIONS | 1,390 |
| COMPOSIO_REMOTE_BASH_TOOL | 780 |
| **Total these 6** | **21,392** |

These 6 MCP tools = ~5,300 tokens if all selected. For a "ping" where they have zero relevance, this is pure waste.

MCP tools are registered into the same `Registry` as built-in tools (`extensions.go:69`), so they go through the same `SchemasForContext` path. They are NOT in `essentialToolNames`, so they only enter via keyword FTS5 or semantic embedding matches.

The challenge: when MCP tools ARE relevant (e.g., "search my gmail" needs google tools, "post to instagram" needs composio), they must be selected reliably.

## Options

A. **Separate threshold for MCP tools** — built-in tools at 0.35, MCP tools at 0.50
B. **MCP tools require keyword match** — must have an explicit keyword from user message in the tool name or description (e.g., "gmail" → google tools, "instagram" → composio tools)
C. **MCP tools excluded from semantic matching entirely** — keyword FTS5 only (fast, deterministic, no false positives)
D. **MCP tools capped separately** — max 3 MCP tools per request
