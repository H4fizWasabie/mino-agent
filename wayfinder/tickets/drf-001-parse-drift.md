# Quality Frontier — Memory & context drift prevention

Status: **OPEN** (GitHub issue #180) — principle decided in the 2026-08-13 session; implementation-ready.

## Question

Behavior drift is now governed by the self-awareness layer (v2.8.11: `post_mortem`, `audit_playbook`, midflight signals). Memory-content drift is not: facts can contradict ground truth, recall surfaces the contradiction blindly, and the graph-rebuild can encode the wrong side as authoritative. How do we extend the same harness-surfaces/brain-arbitrates philosophy to memory?

## Evidence (before/after comparison, VPS 2026-08-12/13)

**Behavior drift — self-awareness verified working:**

| Signal | Before (≤14:26 08-12) | After (v2.8.11+) |
|---|---|---|
| FB playbook | 09:28 run burned 50 iterations, **failed**; 446 LLM calls/day | 2 runs 08-13, **both complete**; 76 LLM calls/day |
| Instagram | 41 iterations (76 calls) | 16 iterations |
| Parse failures | 15 on 08-12 alone | 0 after deploy; 1 on 08-13 (recovered) |
| `post_mortem` | — | Called 14:26–14:27 on the failed run, diagnosed from evidence |
| `audit_playbook` | — | Called 14:47–14:48, real risk-flags rendered |
| `midflight_signal` | — | Traced 08-12 15:16, 08-13 05:05 |

**Memory drift — the exhibit (all pre-v2.8.11):**

| Time (08-12) | Event |
|---|---|
| 07:29 | Episode written with blocked `http://149.28.146.30` URL |
| 09:30 | `public_image_hosting_setup` corrected to HTTPS (`source: user-correction-20260812`) |
| 11:11 | New fact `public_image_hosting` written with the **old** URL again — 1h41m after the correction |
| rebuild | Inverted edge written: wrong-fact `supersedes` corrected-fact, **confidence 0.92** — still live in the graph |

No wrong-URL re-entry since the deployment. Drift mechanisms: (1) re-entry with fresh `At` defeats CTX-014's age flag; (2) provenance ignored at recall (`entryRanking` has no source scoring); (3) graph-rebuild encodes contradictions as authoritative edges (`validInferredEdges` checks targets exist, not provenance).

## Decision (2026-08-13 session)

Three gaps, verified real against code + live graph, all harness-side (no new LLM calls):

1. **Provenance weighting at recall** — facts with `source: user-correction` / `source: user` get a rank bonus in `entryRanking` so user corrections outrank model re-entry. (Write-side dependency: `save_note` stamps `Source: "user"` — tracked in GIG-001/#178.)
2. **Contradiction marker at recall** — when two recalled facts carry conflicting URLs/domains, surface both with `⚠ conflicts with <id>` so the brain arbitrates with visibility instead of trusting the higher rank.
3. **Rebuild provenance guard** — graph-rebuild must not write `supersedes` edges whose target is user-provenanced (`source: user-correction` / `user`). The 0.92 inverted edge is still live and must be cleaned.

## Implementation seams

- `entryRanking` (graph_memory.go:1100): add source bonus + contradiction detection on the top-ranked set; render `⚠ conflicts with <id>` in `Remember` rationale.
- `validInferredEdges` / rebuild edge write (memory.go:675, 819): skip `supersedes` edges targeting user-provenanced facts.
- `CleanMemoryEdges` or a one-off repair: drop the live inverted edge (`public_image_hosting → supersedes → public_image_hosting_setup`).

## Acceptance criteria

- [ ] The corrected fact (`public_image_hosting_setup`) ranks above the re-entry fact for image-hosting queries.
- [ ] Recalling both URL facts surfaces a conflict marker; neither is silently trusted.
- [ ] Rebuild never writes `supersedes` edges pointing at user-provenanced facts; the live inverted edge is removed.
- [ ] Regression tests: source weighting, conflict marker, rebuild guard.
