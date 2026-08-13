# Quality Frontier — Remove embeddings entirely; FTS5 + essentials + triggers become the floor

Status: **OPEN** (GitHub issue #179) — principle decided in the 2026-08-13 session; implementation-ready.

## Question

Does Mino still need embeddings anywhere in its architecture, or is FTS5 + essentials + explicit naming + skill triggers a sufficient floor?

## Root cause / evidence (VPS data + code, 2026-08-13)

Consumer inventory — the 833-row `memory_embeddings` store serves three consumers, all dead/broken/replaceable:

| Consumer | Reads | Status on VPS |
|---|---|---|
| Recall gap-fill (`mergeEmbeddingHits`, graph_memory.go:1160) | all docs | **Dead** — 80 remembers, 0 similarity contributions, 0 since 08-13 |
| Dedup (`DedupDue`, memory.go:1113) | plain `fact` key | **Broken** — `DocsBySource("fact")` returns empty since `RekeyFacts` renamed keys to `fact:<id>`; silent no-op |
| Graph-rebuild candidates (`graphCandidates`, memory.go:664) | `fact:<id>` | Maintenance-only, replaceable by keyword narrowing |
| Tool semantic gating (tools.go:509) | in-memory map, per-turn Embed | Healthy but a convenience layer: excludes MCP tools, serves only built-ins beyond the 14 essentials |
| Skill selection (skill.go:299) | in-memory, per-invocation | Healthy but skills carry explicit `Triggers` |
| Episode vectors (28) | nothing | Dead weight |
| Working-memory index (tools.go:1937) | only the dead recall path | Dead |

History supports removal: CTX-013 (send_document token leak) was solved by the **essentials list**, not embeddings; tool-002 shows the semantic layer is a perpetual tuning surface (threshold/dilution wars, re-perturbed by every model swap — a drift vector, not a drift guard).

## Decision (2026-08-13 session)

**Remove embeddings entirely.** The floor becomes: essentials (14, unchanged) + explicit naming + FTS5 on tool name/description + MCP keyword gating + families + skill triggers.

**Strengthen tool descriptions in the user's vocabulary** so FTS5 catches specialist picks ("make a picture" not just "generate raster imagery"). Data change, not code. If a real discovery gap appears, the fallback is adding trigger-like keywords to tool defs or promoting to essentials — data edits, not architecture.

## Conditions for safe removal

1. Essentials list unchanged (CTX-013's `send_document` stays pinned).
2. `semanticToolNames` dies; FTS5 `searchToolNames` is the single discovery layer.
3. Skill matching: keep trigger matching; optionally FTS5 on skill name+desc+triggers replaces `semanticMatch`.
4. `graphCandidates` → keyword narrowing from the claim text (reuses `entryRanking` signals).
5. CTX-014 age flag: `useEmbedder` gate → `liveGraph` bool — freshness must survive.
6. `manage_memory` dedup → honest answer ("consolidate covers this") instead of reporting 0 clusters.
7. Playbook automation unaffected (explicit 8-tool registries, no gating).

## Implementation seams

- Delete: `EmbeddingStore` (adapters.go), `memory_embeddings` table (migration), `Prune`/`RekeyFacts`/`HasFactEmbedding`, `MINO_EMBED_MODEL` config + wiring (app.go), cosine machinery, embedding tests.
- tools.go: `semanticToolNames`, `toolEmbeddings` map, working-memory `Index` call.
- skill.go: `cacheSkillEmbeddings`, `descEmb`, `semanticMatch`.
- graph_memory.go: `mergeEmbeddingHits`, `mergeThreshold`, `useEmbedder` → `liveGraph` in `entryRanking`.
- memory.go: `DedupDue` embedding clustering, `graphCandidates` similarity search, IndexFact call sites.
- dashboard.go: `update_fact` re-embed call.
- wayfinder/tickets/tool-002-semantic-tuning.md → closed obsolete.

## Acceptance criteria

- [ ] No embedding API call anywhere in the codebase (grep Embed/EmbedBatch/SearchScored → none).
- [ ] `memory_embeddings` table dropped cleanly.
- [ ] Recall unchanged: keyword rationale only; `age: Nd (possibly stale)` still appears on live-graph recalls.
- [ ] Tool discovery via FTS5 on user-vocabulary descriptions; essentials unchanged; `send_document` still pinned.
- [ ] Skill trigger matching works without embeddings.
- [ ] `manage_memory` dedup answers honestly.
- [ ] Tests: embedding tests removed; FTS5 + recall + freshness tests green.
