# 05 — Semantic-memory runtime and dashboard cutover

**What to build:** All normal semantic-memory behavior uses the graph while SQLite facts become read-only migration diagnostics.

**Blocked by:** 02 — Lossless legacy archive and canonical claim migration; 03 — Graph-backed memory management; 04 — Consolidation-time relationship inference.

**Status:** resolved

- [x] `remember`, `save_note`, consolidation, memory management, and dashboard views share GraphMemory as their source.
- [x] Legacy semantic FTS reads and writes are removed or redirected.
- [x] Embeddings use stable graph claim identity and update correctly after overwrite/delete.
- [x] Episodes, chat history, and operational SQLite tables remain unaffected.
- [x] Natural local integration tests prove the complete graph-backed memory path.

Semantic search now delegates to GraphMemory; dashboard semantic facts are graph snapshots; SQLite facts are exposed only as migration diagnostics. One temporary live API session saved and recalled a graph marker successfully, then its graph file, chat rows, tool rows, and artifacts were removed.
