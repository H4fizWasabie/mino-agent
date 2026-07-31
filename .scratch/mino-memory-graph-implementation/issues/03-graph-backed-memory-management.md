# 03 — Graph-backed memory management

**What to build:** Mino’s correction, forgetting, confirmation, dashboard editing, and embedding updates operate on authoritative graph claims.

**Blocked by:** 01 — Graph storage contract and live reconciliation.

**Status:** resolved

- [x] `manage_memory` corrects and overwrites graph claims rather than SQLite facts.
- [x] Forgetting removes the graph fact, valid inbound references, and its embedding deterministically.
- [x] Confirmation/rejection feedback has a graph-backed representation or an explicit documented replacement.
- [x] Dashboard memory reads and edits use graph claims and Markdown bodies.
- [x] Graph-backed management behavior has focused API and runtime tests.

Implemented GraphMemory.ReplaceFact, DeleteFact, Feedback, and FindFact; moved management and dashboard semantic actions to Markdown-backed claims; and added stable graph-claim embedding identities. Verification includes graph management tests and the full Go suite.
