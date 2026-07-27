# Memory Graph — Design Notes

> Captured from discussion with Abah, 2025. Replaces flat FTS5 memory with
> graphify-style knowledge graph.

## Problem

Mino's current memory is a bag of post-it notes. Facts stored as flat rows,
recall returns top-K by FTS5 keyword match. Zero awareness of relationships.
The LLM reconstructs "why these facts relate" every turn.

Observed symptoms (from VPS, July 2026):

- Same fact duplicated 6+ times with slight wording variations
- "Abah's name is Hafiz" stored 5 times (IDs 72, 73, 76, 95, 158)
- "Mino OSS project" stored 7 times (IDs 82, 85, 98, 107, 114, 117, 159)
- Large unrelated ephemera stored as facts (AI news briefs, transient intents)
- No dedup, no edges, no structure

## Solution

Replace the flat memory store with a graph. Modeled after graphify's
methodology — nodes, typed edges, community detection, subgraph traversal.

### Storage: One `.md` file per fact

```
~/.mino/memories/
  abah_is_hafiz.md
  abah_prefers_golang.md
  mino_oss_project.md
  procura_is_authoritative.md
  pims_deployed_20260726.md
  ...
```

### Front matter schema

```yaml
---
id: abah_prefers_golang        # snake_case, unique, deterministic from subject
type: semantic                  # semantic | episodic
subject: Abah prefers Go for backend development
at: 2025-01-15T10:30:00+08:00  # ISO 8601, when captured
edge:                           # explicit relationships
  - target: abah_is_hafiz
    rel: attributed_to
  - target: go_language
    rel: prefers
---

Optional body. 1-3 sentences. Extra context the LLM can read_file for.
```

| Field | Required | Notes |
|-------|----------|-------|
| `id` | yes | Unique, deterministic |
| `type` | yes | `semantic` or `episodic` |
| `subject` | yes | Single sentence. What recall displays. |
| `at` | yes | ISO 8601 timestamp |
| `edge` | no | List of `{target, rel}` |
| Body | no | 1-3 extra sentences. Pulled only on `read_file`. |

### Edge relations

`attributed_to`, `prefers`, `maintains`, `depends_on`, `supersedes`,
`located_at`, `requires`, `deployed_on`, `scheduled_at`, `calls`,
`used_in`, `related_to`

### Tool: `remember` (replaces `recall`)

```
remember("Procura database")
  → FTS5 finds start node: procura_is_authoritative
  → BFS edges 2 hops
  → FTS5 fallback for terms with 0 graph hits
  → Return subgraph (primary) + top-3 embedding matches (supplement)
```

Instead of a flat 400-token blob of duplicates, returns ~80 tokens of
structured subgraph. LLM can `read_file` individual `.md` for depth.

### Roles of FTS5 and embeddings (demoted, not removed)

| Role | Mechanism |
|------|-----------|
| Entry point | FTS5 finds start nodes from natural language |
| Duplicate detection | Embedding similarity flags near-duplicate `.md` files |
| Skill matching | Embedding (unchanged) |
| Semantic fallback | Embedding bridges vocabulary gaps |
| Edge discovery | Embedding similarity proposes missing edges |

### Two write paths, one function

| Path | Trigger | What happens |
|------|---------|--------------|
| Consolidation | Background, periodic | Distillation LLM reads chat_log, writes `.md` files |
| `save_note` | In-conversation | Mino's loop calls tool, writes `.md` immediately |

Both call same `RecordFact()` function. Existence check: if `memories/{id}.md`
exists → merge/update/skip instead of duplicate.

### Context efficiency

Current recall for "What do I know about Abah?": ~400 tokens, 60% duplication.
Same query with `remember`: ~80 tokens of subgraph. No duplication. LLM pulls
body only when needed via `read_file`.

## What changes in Mino codebase

1. `memory.go` — add `RecordFact()`, graph traversal, replace `Search()`/`recall`
2. `tools.go` — update `save_note` to write `.md` instead of INSERT
3. Consolidation prompt — output `.md` files instead of JSON → INSERT
4. New `memories/` directory under `~/.mino/`
5. `adapters.go` — embedding indexer watches `memories/` directory

### `remember` output format (decided)

Indented tree. Compact (~80 tokens), LLMs parse naturally.

```
procura_is_authoritative
  → [supersedes] procurepilot_is_legacy
  → [depends_on] procura_db_location
    → [located_at] vps_server
  → [maintains] abah_is_hafiz
```

## Migration from existing flat memory

Three-phase, one-time. Triggered by `--migrate-memories` flag on startup.

### Phase 1: Migrate

```
For each row in facts:
  → memories/{slug_from_subject}.md
  → front matter: id, type=semantic, subject, at=created_at, edges: []
  → body = content

For each row in episodes:
  → memories/{slug_from_summary}.md
  → front matter: id, type=episodic, subject, at=happened_at, edges: []
  → body = summary
```

~30 lines of Go. Nodes only, no edges yet.

### Phase 2: Dedup

1. Embed all `.md` files (existing `EmbeddingStore`)
2. Cluster by cosine similarity > 0.85 (group near-duplicates)
3. Per cluster: LLM merges into one clean `.md`
4. Delete stale `.md` files from merged clusters

Distillation LLM prompt: "Here are N records that say the same thing. Produce one clean fact." Same format, different input source.

### Phase 3: Normal operations

Incremental consolidation handles new facts. Edges filled in over subsequent consolidation passes.

## Open questions (deferred)

- Community detection: when? Louvain on graph?
- Graph indexer: rebuild on startup? Watch filesystem?
