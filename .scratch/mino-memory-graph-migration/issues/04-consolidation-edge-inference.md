# Add confidence-aware consolidation edge inference

Status: resolved
Type: grilling
Blocked by: 02

## Question

How should DeepSeek v4 Flash receive candidate facts, emit explicit or inferred edges, assign confidence and provenance, and replace stale inferred edges without disturbing explicit edges?

## Answer

During consolidation, the existing small-model call receives the session transcript, the newly distilled claim nodes, and a bounded candidate list of existing graph nodes. Embeddings may preselect candidates; they do not create edges themselves.

The model emits structured candidate edges containing `target`, `rel`, `kind`, and `confidence`. The runtime ignores model-supplied provenance and sets `source` to the consolidation session. It accepts only valid targets, known relation names, and inferred confidence at or above `0.85`; ambiguous or lower-confidence candidates are discarded from active graph edges rather than displayed as memory.

Explicit edges supplied by `save_note` or directly confirmed by consolidation are preserved. Inferred edges are replaceable: when a fact is reconsolidated, its previous inferred edges are removed and the newly validated inferred set is written. Explicit edges are never removed by this refresh.

The prompt must forbid generic word-overlap links and unsupported `related_to` edges, require an empty edge list when evidence is insufficient, and cap edges per fact to keep the graph sparse. No model call occurs during Markdown reconciliation.

## Context pointer

This decision is recorded in the Wayfinder map under `Decisions so far`.
