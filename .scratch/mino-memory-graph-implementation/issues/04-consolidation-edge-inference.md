# 04 — Consolidation-time relationship inference

**What to build:** Consolidation uses the configured DeepSeek v4 Flash small model to create sparse, confidence-aware relationships between canonical claims.

**Blocked by:** 01 — Graph storage contract and live reconciliation; 02 — Lossless legacy archive and canonical claim migration.

**Status:** resolved

- [x] Consolidation emits structured directed edges with relation, kind, confidence, and runtime-assigned source.
- [x] Candidate neighbors are bounded; embeddings only select candidates and never create edges directly.
- [x] Only valid high-confidence inferred edges are stored; generic word-overlap links are rejected.
- [x] Explicit edges survive inferred-edge refreshes.
- [x] Inferred edges are replaced rather than accumulated for reconsolidated claims.
- [x] Fake-LLM tests cover valid, weak, malformed, and contradictory edge output.

Implemented bounded embedding-selected candidates, confidence/provenance validation, explicit-edge preservation, inferred-edge replacement, and startup cleanup of legacy generic links. The focused consolidation validation test and full suite pass. The deployed VPS graph reports zero unprovenanced generic related_to edges.
