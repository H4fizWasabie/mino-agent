# Memory retrieval is loop-private — expose a deterministic read surface

Status: **OPEN** (wayfinder ticket, CTX-022 — GitHub issue #194)

## Question

`remember` — the intent-ranked graph retrieval (entryRanking + archive fallback
+ BFS expansion + provenance + conflict flags, graph_memory.go:1105) — is a tool
*inside* the chat loop. There is no CLI, MCP, or REST surface for it. An agent
standing outside mino (same model, this session's author) must scrape
`~/.mino/memories/*.md` over SSH — `ls`, `grep "^subject:"`, `head` frontmatter —
re-deriving a cruder, unscored retrieval. The knowledge layer (846 facts, typed
edges, user-provenance) is the accumulated personal asset, but it is reachable
only by paying a full LLM turn through the chat API, or by raw file access.

## Evidence (2026-08-15, live session)

| Access path | Result for the same question ("what happened to Agent-Reach?") |
|---|---|
| In-loop `remember` | Ranked subgraph in ONE call: top fact with full `matched:` rationale, user-provenance, conflict flags, and the `[supersedes] → the owner requested deletion of the repo on 2026-08-14` walk — every mechanism working as designed |
| External agent (this session) | `ls \| wc -l`, `grep -h "^subject:"` across 846 files, `head` frontmatter per fact, manual edge reading — no scoring, no rationale, no traversal |

Same model (deepseek-v4-flash) on both paths. The answer-quality difference was
entirely the retrieval interface, not the brain — the value lives in the
retrieval layer, and the layer is loop-gated.

Companion gap: OBS-002 — playbook-stage tool calls (incl. `remember`) don't
reach `audit.jsonl`, so even *usage* of memory inside runs is observability-opaque.

## Scope

Expose the existing retrieval, nothing new:

1. **CLI**: `mino remember "<query>"` (and `mino memory path <a> <b>` for
   RememberPath) — prints the exact output of `GraphMemory.Remember`. No LLM
   call, deterministic.
2. **MCP server**: `~/.mino/mcp.d/mino-memory` exposing `memory_remember` /
   `memory_path` so any agent queries with parity.
3. **REST**: dashboard endpoint (e.g. `GET /api/memory/remember?q=…`),
   localhost-bound by default, behind the existing auth for remote access.
4. **Read-only by construction**: no mutation endpoints. Writes
   (`save_note`, `manage_memory`, consolidation, edge judgment) stay
   loop-private — the graph remains single-writer.
5. Reuse `GraphMemory.Remember`/`RememberPath` directly — zero new retrieval
   logic, byte-identical output.

## Acceptance criteria

- [ ] `mino remember "q"` output is byte-identical to the in-loop `remember`
      tool for a fixed query on a fixed graph
- [ ] An external agent using only the surface output answers the Agent-Reach
      question correctly (deletion fact, provenance, no web-search override)
- [ ] No write path exposed; mutation tools unchanged; existing memory tests
      green
- [ ] Any network-visible surface is auth-gated; localhost default

## Out of scope

- Moving the brain/loop out of mino (the loop owns judgment, orchestration,
  verification, write discipline)
- External write access to the graph (consolidation/judgment stay loop-private)
- Changing the retrieval algorithm (same entryRanking/BFS)
- The model-judgment failure from the same session (user-provenanced memory
  fact ignored in favor of web data) — a prompt/verification-discipline item,
  separate ticket if pursued
