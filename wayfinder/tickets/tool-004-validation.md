## Resolution

Status: **RESOLVED** (historic — decided/shipped; predates the Status-line format)

**Live VPS test.** Implementation → deploy → one test session via dashboard API → observe traces → clean up → report.

**Test constraints:** single session, no guidance, no spam. Let Mino behave naturally.

**Trace diagnostics:** add `tool_selection` event logging `{essential: [...], keyword: [...], semantic: [...], mcp_keyword: [...]}` so every turn shows exactly which tools entered via which path and why.

## Context

Current usage patterns from VPS (July 20-28):

| Domain | Key tools needed | Trigger words |
|--------|-----------------|---------------|
| Coding | codegraph_query, graphify_query, git_diff, glob, grep, edit_file | "fix", "bug", "implement", ".go" |
| Gmail | MCP_google_search_emails, MCP_google_delete_email | "email", "gmail", "inbox" |
| Composio/automation | COMPOSIO_* tools | "instagram", "post", "composio" |
| Web search | search_web, fetch_url | "search", "find", "news" |
| Memory | remember, save_note | "remember", "save", "note" |
| Scheduling | schedule_playbook, list_schedules | "schedule", "daily", "morning" |
| General chat | NONE | "hello", "ping", "thanks" |

Validation approach options:
A. Deploy to VPS, watch traces for 2-3 days, compare tool selection before/after
B. Write eval cases that assert specific tools appear for specific messages
C. Both — eval cases for regression gate, VPS observation for real-world confirmation
D. Add a trace event that logs which tools were selected + why (keyword/semantic/essential), then analyze after deploy

## Constraint

Mino has `eval/cases.json` and `mino eval` CLI. Cases can check that expected tools are called for given prompts. But eval tests behavior (tool calls made), not tool availability (schemas sent). We'd need a new assertion type or a separate mechanism.
