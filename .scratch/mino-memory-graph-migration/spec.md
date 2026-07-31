# Mino Semantic Memory Graph Migration

Status: ready-for-agent

## Problem Statement

Mino currently has two competing durable semantic-memory representations: SQLite `facts`/`facts_fts` rows and Markdown graph memories. The live deployment contains 173 legacy SQLite facts and 155 graph facts. Subject-derived IDs have caused collisions, many graph edges are generic `related_to` links, and the graph index can become stale when Markdown files change outside Mino.

This makes it difficult for Mino to answer not only what it knows, but how facts relate and why those relationships exist. It also makes memory correction, dashboard editing, consolidation, and future retirement of SQLite facts unsafe.

## Solution

Make the Markdown graph the sole source of durable semantic memory while retaining SQLite for raw conversation history, episodes, embeddings where still useful, and operational state.

Preserve every legacy SQLite fact in an inactive Markdown migration archive and manifest before canonicalizing claims. Use stable claim-based node IDs, structured front matter, directed typed edges, provenance, confidence, deterministic live reconciliation, and consolidation-time relationship inference through the configured OpenRouter small model (currently DeepSeek v4 Flash).

Retire SQLite `facts` and `facts_fts` only in a later release after graph parity, live behavior, backup integrity, and rollback evidence are proven.

## User Stories

1. As Abah, I want Mino to remember durable facts in one authoritative system, so that memory does not diverge between SQLite and Markdown.
2. As Abah, I want distinct claims to remain distinct, so that several facts about the same person or project do not overwrite one another.
3. As Abah, I want current facts corrected in place, so that normal updates do not clutter memory with superseded historical nodes.
4. As Abah, I want Mino to preserve old facts during migration, so that no existing memory is lost before consolidation.
5. As Abah, I want every migrated fact traceable to its original SQLite row, so that migration decisions can be audited.
6. As Abah, I want graph files to be readable and editable as Markdown, so that memory remains inspectable without a database tool.
7. As Abah, I want a short rationale attached to a fact, so that Mino understands why the fact matters.
8. As Abah, I want longer context kept in the Markdown body, so that structured graph metadata remains compact.
9. As Abah, I want relationships to be directed and typed, so that “depends on” is not confused with “is depended on by.”
10. As Abah, I want reverse graph lookup, so that Mino can answer both sides of a relationship without storing duplicate reverse edges.
11. As Abah, I want explicit and inferred relationships distinguished, so that Mino does not present guesses as facts.
12. As Abah, I want inferred relationships to carry confidence and provenance, so that uncertain memory can be treated appropriately.
13. As Abah, I want weak or ambiguous relationships excluded from normal recall, so that graph traversal remains trustworthy.
14. As Abah, I want Mino to avoid generic word-overlap edges, so that shared words do not create false relationships.
15. As Abah, I want consolidation to use the configured small model for relationship inference, so that normal interaction remains fast and affordable.
16. As Abah, I want explicit edges preserved when inferred edges are refreshed, so that confirmed relationships are not lost.
17. As Abah, I want stale inferred edges replaced during reconsolidation, so that the graph does not accumulate outdated guesses.
18. As Abah, I want Markdown edits to become visible while Mino runs continuously, so that a restart is not required to correct memory.
19. As Abah, I want malformed or partially written Markdown to leave the last valid memory intact, so that an interrupted edit cannot erase knowledge.
20. As Abah, I want `index.json` to accelerate graph reads without becoming authoritative, so that it can be rebuilt safely.
21. As Abah, I want the index rebuilt atomically, so that readers never observe a half-written graph cache.
22. As Abah, I want `remember` to traverse explicit and sufficiently confident inferred edges, so that returned context is compact and connected.
23. As Abah, I want memory correction and forgetting to operate on graph claims, so that all memory-management actions affect the authoritative store.
24. As Abah, I want dashboard memory editing to update graph files, so that the UI and Mino’s runtime observe the same memory.
25. As Abah, I want embeddings associated with stable graph IDs, so that corrections and deletions remove the correct vectors.
26. As Abah, I want SQLite facts frozen before removal, so that the migration has a safe observation period.
27. As Abah, I want a validated database backup and migration archive before retirement, so that rollback remains possible.
28. As Abah, I want live certification to prove the deployed behavior, so that local tests are not mistaken for production proof.

## Implementation Decisions

- The semantic-memory boundary is `GraphMemory`; SQLite remains responsible for chat history, episodes, calendar/reminders, tools, audit state, and other operational data.
- Each distinct durable claim receives a stable ID. IDs represent claim identity, not the current wording or full body content.
- Corrections and current changes overwrite the existing claim node. Automatic historical supersession chains are out of scope.
- Every legacy SQLite row is first copied into an inactive Markdown migration archive with a manifest. The archive is excluded from active graph traversal.
- Active graph front matter contains stable ID, type, subject, capture/update time, optional short rationale, optional source, and directed edges.
- Each edge contains target, relation, kind, confidence, and source. Edge kinds are explicit, inferred, or ambiguous; ambiguous edges are not traversed by default.
- Explicit edges are written immediately. Inferred edges are generated only during consolidation by the configured small model.
- Embeddings may select bounded candidate neighbors, but embeddings alone never create graph edges.
- Inferred edges require valid targets, known relations, and confidence of at least `0.85` for active storage/traversal. Generic word-overlap links are rejected.
- Explicit edges survive inferred-edge refreshes. Inferred edges for a reconsolidated fact are replaced rather than appended indefinitely.
- Markdown files are authoritative. `index.json` is a rebuildable metadata/cache projection with lowercase JSON fields.
- A deterministic background reconciler checks file modification state periodically, while graph reads perform a freshness safety check. Reconciliation uses no LLM.
- Changed files are reparsed selectively. New files are added, deleted files are removed from the in-memory graph, and malformed files retain their last valid state until a later retry.
- Index writes use a temporary file and atomic rename.
- The consolidation output contract is structured and bounded. The runtime supplies provenance rather than trusting model-supplied source fields.
- Semantic-memory runtime paths move from SQLite to the graph: memory management, dashboard memory operations, semantic search, consolidation writes, and embedding identity.
- Legacy SQLite facts become read-only migration/diagnostic data before schema removal.
- SQLite fact-table retirement is a separate release from the first graph ownership cutover.

## Testing Decisions

Tests should verify externally visible behavior rather than private implementation details.

Primary seam: `GraphMemory` behavior. Cover Markdown parsing, stable claim overwrite, explicit and inferred edge validation, confidence filtering, reverse lookup, live reconciliation, malformed-file recovery, deletion detection, atomic index projection, and rebuild from missing index.

Secondary seam: memory consolidation. Use a fake LLM response to verify structured edge parsing, explicit-edge preservation, inferred-edge replacement, provenance assignment, confidence thresholding, bounded candidates, and rejection of unsupported relationships.

Dashboard/API behavior should verify graph reads, graph body updates, graph-backed correction/forget behavior, and stable graph IDs. Existing dashboard and memory test conventions should be extended rather than introducing a new test framework.

Migration tests should verify that every legacy row receives an archive record, collision-prone subjects remain distinct, the manifest records canonicalization outcomes, and no row disappears silently.

Pre-retirement certification must verify service health, deployed revision, graph/archive counts, live `remember`, `save_note`, memory management, consolidation, dashboard reads, database backup integrity, and the absence of new SQLite fact writes during the observation window.

## Out of Scope

- Replacing SQLite operational state, chat history, or episode history.
- Introducing a separate graph database.
- Community detection or Louvain-style clustering for personal memory.
- Automatic historical supersession chains.
- LLM use during Markdown/index reconciliation.
- Removing SQLite facts in the same release that first changes semantic-memory ownership.
- Changing the configured main model or small model as part of this migration.

## Further Notes

The live VPS configuration uses OpenRouter Xiaomi MiMo v2.5 as the main model and DeepSeek v4 Flash as the small model for consolidation. Production work must verify the deployed runtime rather than relying on local configuration or source code alone.
