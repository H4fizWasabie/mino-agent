# Mino Semantic Memory Graph Migration

Status: resolved
Type: wayfinder:map

## Destination

Mino's Markdown graph becomes the sole source of durable semantic memory. SQLite retains raw conversation history, episodes, embeddings where still useful, and operational state. The migration preserves existing facts, avoids identity collisions, updates every semantic-memory path, and removes the obsolete SQLite durable-facts store only after parity is proven.

## Notes

- Use the existing local-Markdown tracker under `.scratch/`.
- `.md` files are authoritative; `index.json` is a rebuildable cache.
- Live Markdown reconciliation is deterministic and LLM-free.
- DeepSeek v4 Flash through the configured OpenRouter small-model route handles consolidation-time relationship inference.
- Do not modify production data during Wayfinder planning.

## Decisions so far

- [Claim-based node identity] — each distinct durable claim gets its own stable ID; subjects are not IDs.
- [Legacy fact preservation] — archive every SQLite row as inactive Markdown with a manifest before canonicalization; keep the archive outside active `memories/`.
- [Graph front-matter schema] — stable claim IDs, compact `why`/`source` metadata, directed typed edges with `kind`/`confidence`/`source`, and lowercase JSON field names.
- [Live index reconciliation] — a deterministic background scan plus read-time freshness check reparses changed Markdown, preserves last valid facts on malformed writes, and atomically rebuilds the derived index.
- [Consolidation edge inference] — DeepSeek v4 Flash receives bounded candidate nodes, emits confidence-scored edges, and only high-confidence valid inferences are stored; inferred edges are replaceable while explicit edges are preserved.
- [Semantic-memory runtime cutover] — GraphMemory owns durable semantic memory; manage-memory, dashboard, embeddings, and semantic search move to graph paths while SQLite retains history, episodes, and operational state.
- [SQLite retirement gates] — remove `facts`/`facts_fts` only in a later release after archive parity, live behavior, backup integrity, a no-write window, and rollback evidence are proven.
- [Overwrite current facts] — corrections and current changes overwrite the existing claim node; historical supersession nodes are out of scope.
- [Graphify-style edge provenance] — edges distinguish explicit, inferred, and ambiguous relationships with confidence and source metadata.
- [Confidence-aware traversal] — `remember` traverses explicit edges and inferred edges at or above `0.85`; ambiguous edges are excluded.
- [Directed typed edges] — relationships preserve direction, with reverse lookup supported without duplicate reverse edges.
- [Structured why and context] — front matter carries compact machine-readable rationale and provenance; the body carries deeper context.
- [Preserve before consolidating] — import every legacy SQLite fact under a collision-safe temporary identity, then consolidate into canonical claim nodes, and delete last.
- [Index as cache] — Markdown is authoritative; `index.json` is derived and must reconcile from Markdown.
- [Live reconciliation] — a lightweight background check plus read-time freshness safety detects changed, new, and deleted Markdown files without an LLM.
- [Consolidation inference] — the configured small model infers relationships only during consolidation; inferred edges are replaced rather than accumulated.
- [DeepSeek threshold] — the current OpenRouter small model is DeepSeek v4 Flash; `0.85` is the default inferred-edge traversal threshold.

## Handoff

The architecture decisions are resolved. The synthesized implementation specification is [spec.md](spec.md), marked `ready-for-agent`. The implementation tickets are published under [mino-memory-graph-implementation](../mino-memory-graph-implementation/).

## Out of scope

- Replacing SQLite operational state, chat history, or episode history.
- A separate graph database.
- Community detection or Louvain-style clustering for personal memory.
- Automatic historical supersession chains.
