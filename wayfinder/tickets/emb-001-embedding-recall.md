# Quality Frontier — Does memory recall still need query embeddings?

Status: **OPEN** (GitHub issue #179) — VPS data gathered; principle discussion scheduled for a dedicated session.

## Question

The embedding similarity path in memory recall (`entryRanking` → `mergeEmbeddingHits`, graph_memory.go) maintains 833 cached fact vectors and per-query embed API calls. Has it ever contributed to a recall on the VPS?

## Root cause / evidence (VPS data, 2026-08-13)

- `memory_embeddings`: **833 rows** (~1 vector per fact + episode).
- `remember` calls in `tool_calls`: **80**.
- Recall rationales containing `similarity:`: **0** — the embedding merge has contributed to **zero** recalls in recorded history.
- "No memories found": 1. Archive fallbacks (`[archived]`): 0.
- The keyword path (subject + why/use_when + turn-word overlap, MEM-02/03) carried 79/80 recalls alone.

## The genuine gap

Embeddings have **two other consumers that are not in question**: tool semantic gating (tools.go `semanticToolNames`, fires every turn) and skill selection (skill.go `cacheSkillEmbeddings`). The question is only the **fact-recall consumer** and its machinery: per-query `SearchScored` embed calls, DB cache, `Prune`, `RekeyFacts`, `HasFactEmbedding`, and the dashboard `update_fact` re-embed path.

## Decision points (principle — owner decision, pending)

1. **Cut the recall gap-fill now?** Data is decisive (0/80), but honest counterpoint: the 1 "No memories found" is exactly the vocabulary-gap case embeddings exist for. How much is that 1-in-80 worth?
2. **If cut: drop fact vectors entirely** (the 833) and keep only tool/skill embeddings — deleting Prune/Rekey/re-index complexity with them.
3. **Or instrument first** — trace every similarity contribution for a week, then decide.

## Recommendation (for the discussion session)

1 = yes, 2 = yes, 3 = no. 80 samples with zero hits is a verdict, not a maybe. If a real paraphrase-recall failure appears later, the fix is re-adding `IndexFact` + one call, not rebuilding the store.

## Acceptance criteria (to fix after discussion)

- [ ] Recall works identically (or better) without the embedding merge — keyword + why/use_when + archive fallback carry all recalls.
- [ ] `memory_embeddings` no longer stores fact vectors; tool/skill gating unchanged.
- [ ] Dead machinery removed: `mergeEmbeddingHits` path, `IndexFact`/`RemoveFact`/`HasFactEmbedding`/`RekeyFacts`, dashboard re-embed on `update_fact`.
- [ ] Tests updated accordingly.
