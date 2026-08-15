# Memory: retrieval is loop-private — external read surface + retirement semantics + provenance-gated verification

Status: **RESOLVED** (wayfinder ticket, CTX-022 — GitHub issue #194)

Resolved 2026-08-15, shipped across v2.10.3→v2.10.6: Part A (`mino remember`
CLI, `/api/memory/remember`+`path` REST, `mino-memory` MCP server),
Part B (archive dir at init, retirement prompt semantics), Part C
(provenance gate escalated through three live-tested rounds, ending in the
post-reply verification pass that signs contradictions). Live-verified on
the VPS: the Agent-Reach answer now carries the harness-signed correction.

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

## Part B — Retirement semantics: "keep as history" must mean archive, not live retention

Evidence (2026-08-14 trace, cross-checked against the live conversation):
when the owner asked to forget the Agent-Reach repo and then to "store them as
historical record", mino deleted the repo and **left the memory facts fully
live and rankable** — `agent_reach_tool_description` kept its active `why`
("expands Mino's internet access capabilities") and `use_when` ("when
evaluating cost-effective social media integrations"). The archive tier
(`ArchiveFact`, `memories/archive/`, `[archived]` tag, thin-result-only recall)
exists but was never engaged: `memories/archive/` has never been created on the
VPS — the archive path is dead code in practice. Consequence: the stale live
fact ranked in recall and produced the wrong "still actively maintained"
answer in the 2026-08-15 demo.

The system prompt tells the model when to *remember* (session.go:57) but says
nothing about retirement semantics — no instruction that "keep as history" /
"forget" means `manage_memory reject` on the target facts.

### Scope B

1. **Prompt guidance** (docs change, ~90% of the fix): beside the existing
   remember guidance, instruct that owner retirement signals ("store as
   historical record", "forget X") map to archiving the matching facts —
   archive tier, never the live tier. Document that `reject` is the archival
   mechanism for intentional history, not only for corrections.
2. **Archive-dir init** (code, ~10%): `os.MkdirAll(archiveDir)` in
   `NewGraphMemory` so the archive path exists from first boot.
3. **Regression test**: reject/archive a fact, assert it is excluded from live
   recall and surfaces only via the thin-result fallback tagged `[archived]`.

### Acceptance criteria B

- [ ] After an owner "keep as history" instruction, target facts are archived
      (moved to `memories/archive/`), not left rankable
- [ ] `memories/archive/` exists on a fresh graph init
- [ ] Test proves archived facts are excluded from live recall

## Part C — Provenance-gated verification (harness owns source weighting)

The 2026-08-15 demo's wrong answer ("still actively maintained") wasn't a
model failure — the harness misweighted the sources. The system prompt's own
verify rule (session.go:110: "Memory may be stale; the live state is truth")
biased the model toward web data over its user-provenanced memory fact. Under
the MAP's harness framing ("the LLM is a component; Mino-the-harness owns the
conditions"), every session failure is a harness gap — the prompt is harness
text, so source weighting is harness work.

### Scope C

1. **Provenance clause** (prompt): the verify rule gains the missing gate —
   user-provenanced memory outranks live/web data unless the fact is flagged
   stale or superseded; live verification fills gaps, it does not re-litigate
   a user-authored fact.
2. **Post-tool signal** (loop): when `remember` returned a user-provenanced
   fact on a subject and the model then calls `search_web` on the same
   subject, inject a mid-flight warning naming the memory fact (the
   nerves.go mid-flight signal pattern — act on the verified signal, never
   silent re-narration).

### Acceptance criteria C

- [ ] Prompt contains the provenance gate (user-provenanced > web unless
      flagged stale/superseded)
- [ ] Loop warns when web search follows a user-provenanced recall on the
      same subject

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
