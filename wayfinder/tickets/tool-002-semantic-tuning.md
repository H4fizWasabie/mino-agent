## Resolution

**Embedding input:** last user message + last assistant reply (~500-2000 chars). One-turn window captures conversational signal without system prompt noise.

**Threshold:** 0.40 (up from 0.25). Playbooks handle structured tasks, so the main loop can be pickier.

**Cap:** 8 results (down from 12).

**Why not full context:** 12,500-char embedding dilutes the user's task — "ping" matches COMPOSIO_WORKBENCH because system prompt vocabulary dominates. One turn of actual conversation keeps the signal task-specific.

## Context

Current code at `tools.go:274` (`semanticToolNames`):
- Embeds full `contextText` = system + all messages (~12,500 chars)
- Cosine threshold: 0.25 — very permissive
- Result cap: 12 tools
- Tool descriptions are indexed with format: `"name — description"`

The problem: for "ping", the 4-char user message is diluted into a 12,500-char embedding. The embedding vector is dominated by SOUL.md vocabulary and chat history, so it shares semantic similarity with many tool descriptions — including COMPOSIO_REMOTE_WORKBENCH (8,785 chars of description) and COMPOSIO_SEARCH_TOOLS (4,324 chars).

Observations from the trace diag:
- "ping" with 30 schemas, 29,380 schema chars, 14,477 input tokens
- The 30 schemas include MCP tools that have zero relevance to "ping"

## Options to evaluate

A. **Embed only the user's last message** — most task-specific, but loses context about the conversation domain
B. **Embed last message + first 500 chars of system prompt** — balances task specificity with domain awareness
C. **Embed last message + last assistant reply** — captures conversational context
D. **Raise threshold** — 0.25 → 0.35, 0.40, or 0.50
E. **Lower result cap** — 12 → 8 or 6
F. **Two-pass:** first pass with tight threshold, fall back to wider if < N tools match

Which combination gives the best precision without breaking real workflows?
