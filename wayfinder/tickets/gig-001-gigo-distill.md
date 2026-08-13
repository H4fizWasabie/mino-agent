# Quality Frontier — Reduce garbage-in/garbage-out at the distill source

Status: **OPEN** (GitHub issue #178) — principle decided in the 2026-08-13 session; implementation-ready.

## Question

Memory grows ~58 facts/day; routine-recurrence snapshots ("Ran X on DATE and published...", "posted to Facebook on DATE") are stored as *durable* semantic facts. How do we stop the garbage at the source instead of cleaning it later?

## Root cause (VPS data, 2026-08-13)

- 811 facts in ~14 days ≈ **58/day**; 62/day since 08-13 (27 episodes + 35 semantic).
- Growth is **unbounded**: 208 episodes (`ep_*`) + 603 semantic; archive fires only on user rejection or LLM judgment "why no longer holds" — no age-based expiry exists.
- `use_when` was proposed as the expiry mechanism — **verified in code: it is recall gating only** (trigger phrases scored 10/word at recall, MEM-02/03). No timestamps, no fire counters, no archive keyed on it. It reduces garbage *out*, not growth.
- The distill prompt (memory.go `DistillOutputsDone`) says "skip routine recurrence" but it is **prompt prose, not a code gate**. Transient news headlines (`Marina Chin heads NSC panel...`) get stored as durable facts.
- `type: episodic | semantic` already exists on every fact but is **write-only metadata** — no policy keys on it. Same situation `At` was in before CTX-014.

## Decision (2026-08-13 session)

**Procedural / long-term split via the existing `type` field:**

| Concept | Existing field | Policy |
|---|---|---|
| Procedural (run logs, tool success/failure, post outcomes) | `type: episodic` | expiry → archive after **30d** (reason "expiry"); **traversal-only** — never a recall start node |
| Long-term (deemed worthy) | `type: semantic` | no auto-expiry, protected |
| "Upgrade" path | already the distill contract | distill emits semantic facts only from whitelisted playbooks |

**Distill gate per playbook** (artifacts carry the playbook identity, so a config flag, not pattern-matching):

- **Semantic ON**: daily-ai-concept (Mino's learning)
- **Semantic OFF (run node only)**: ai-news, malaysian-news, threads-community, threads-workplace-drama, threads-replies, facebook, instagram, gmail, reddit — routine output

**Skip:** provenance restoration (533 empty sources — cosmetic, expiry keys off `type`), generic routine-recurrence regex gate (would kill by-design run nodes).

**Add (write provenance, from the 2026-08-13 drift session):** `save_note` stamps `Source: "user"` at birth — user-authored notes must be distinguishable from model-distilled facts. The recall-side use of this provenance (weighting) lives in DRF-001 (#180).

## Implementation seams (for the implementer)

- `entryRanking` / `Remember` (graph_memory.go:958-1120): exclude `type: episodic` from start-node candidates; keep episodes in the facts map so BFS still traverses into them (provenance context via `attributed_to`).
- `DistillOutputsDue` (memory.go:204): read the playbook's `distill_semantic` flag; when OFF, write the run node and skip `out.Facts`.
- Maintenance pass: archive episodic facts with `At` older than 30d, reason "expiry" (reuse `archiveLocked`). Archived episodes stay answerable via the `[archived]` fallback; semantic facts keep the outcome in their own body.
- Playbook config: add the `distill_semantic` field to config.md parsing.

## Acceptance criteria

- [ ] An episodic fact never appears as a recall start node (still visible in the neighborhood tree).
- [ ] Episodic facts older than 30d move to archive automatically; `[archived]` recall still answers.
- [ ] Only whitelisted playbooks (daily-ai-concept) produce semantic facts from distill; news/threads/facebook/instagram/gmail/reddit produce run nodes only.
- [ ] Regression tests: recall excludes episodes as starts; distill gate honors the per-playbook flag; expiry archives old episodes only.
- [ ] `save_note` facts carry `Source: "user"`.
