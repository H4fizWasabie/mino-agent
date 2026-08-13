# Quality Frontier — Reduce garbage-in/garbage-out at the distill source

Status: **OPEN** (GitHub issue #178) — root cause confirmed against VPS data; principle discussion scheduled for a dedicated session.

## Question

Memory grows ~58 facts/day; routine-recurrence snapshots ("Ran X on DATE and published...", "posted to Facebook on DATE") are stored as *durable* semantic facts. How do we stop the garbage at the source instead of cleaning it later?

## Root cause (VPS data, 2026-08-13)

- 810 facts in ~14 days ≈ **58/day**; 502 created since 08-10 alone.
- Garbage pattern confirmed in subjects: `Ran threads-workplace-drama on 2026-08-13 and published post ID...`, `Malaysian Daily News Roundup posted to Facebook 89lab on 13 Aug 2026...` — the distill prompt (memory.go `DistillOutputsDue`) already says "ONLY durable knowledge worth remembering in a month — skip routine recurrence", but it is **prompt prose, not a code gate**. The model writes them anyway and nothing rejects them.
- 533/810 facts have empty `source` (provenance lost in the graph-rebuild).
- 0/810 facts have non-zero `feedback` — the rejection/expiry loop has never fired.
- Only 3 facts ever came from `consolidation` — consolidation rarely runs (see CTX-015).
- 148/810 facts have no edges.
- Memory is additive-only: no decay/archive pass, so every garbage fact is permanent until manually deleted.

## The genuine gap

Distill acceptance criteria are enforced by the model against its own prompt, not by the harness. Secondary gap: no time-based decay, so accumulation is unbounded.

## Decision points (principle — owner decision, pending)

1. **Code-level acceptance gate in `DistillOutputsDue`?** Reject facts matching routine-recurrence patterns (e.g. subject `^Ran .* on \d{4}-\d{2}-\d{2}`, `posted to .* on .*`) before they reach the graph. Deterministic, no LLM call, ~20 lines + test. Alternative: tighten prompt wording only.
2. **Decay pass?** Archive no-edge + never-recalled + >30d facts (archive infra exists, MEM-08). Or wait until recall quality measurably degrades before building it.
3. **Provenance:** 533 facts lost their `source` — restore attribution, or treat source as cosmetic and skip?

## Recommendation (for the discussion session)

1 = yes (code gate). 2 = measure first. 3 = skip.

## Acceptance criteria (to fix after discussion)

- [ ] A distilled fact that is routine recurrence never reaches the graph.
- [ ] Regression test: distill response containing a `Ran X on DATE` subject is rejected.
