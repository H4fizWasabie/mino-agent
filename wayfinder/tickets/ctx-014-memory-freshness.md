# Context Truth — Memory facts have no freshness/staleness governance

Status: **RESOLVED** (GitHub issue #172)

## Question

Why did Mino keep posting via the wrong (HTTP datacenter) image URL on Facebook — even after the real URL changed, and even after the skill and the memory fact were both corrected to HTTPS?

## Root cause

Recall works; **passive/time-based freshness does not exist.** (Checked the code before scoping — Mino has *more* than a blank slate.)

Mino's semantic memory recalled `public_image_hosting_setup` correctly on its `use_when` trigger ("when posting images"). The fact's *content* had gone stale: the public image host moved to an HTTPS Tailscale URL (`vultr-1.tail8e6639.ts.net`) and Facebook blocks the old `http://149.28.146.30` datacenter IP — but the fact still carried the old URL, so it kept driving the wrong behavior for a week.

### What Mino already has (verified in graph_memory.go / memory.go)

- **`At time.Time`** on every `Fact` — written on create/edit (`fact.At = time.Now().UTC()`). **Unused in `entryRanking`**: the score uses only subject/body/use_when/why/turn overlap + embedding similarity. `At` is write-only metadata today.
- **`Feedback int`** (-5..+5) with **active rejection expiry** (MEM-08): `Feedback < 0 → archiveLocked(..., "user rejection")`. A *rejected* fact is archived. But `Feedback` is **not** a ranking weight and is **not** time-based.
- **Archive lifecycle** (MEM-08) + a **reconciler** (hot-reload of changed `.md` files).

### The genuine gap

**No passive/time-based freshness.** `At` is tracked but never compared to `now`; a fact is never aged, boosted, or flagged by age. A stale-but-unrejected fact (exactly the FB case) ranks identically to a freshly-written one and is trusted indefinitely. This is precisely the OKF `stale_after` slot — and unlike OKF, the timestamp hook (`At`) already exists; it just isn't wired into recall. First witnessed case for that idea.

## How it differs from CTX-013

CTX-013 was four *stale instructional* notes teaching a wrong workflow; the fix was ad-hoc deletion with no systemic mechanism. CTX-014 is the **missing mechanism itself**: no fact of any kind (instructional *or* factual) carries a freshness signal or gets re-validated. The fix is not "delete this fact" — the fact is now correct (we edited it to HTTPS). The gap is *governance*: a fact whose underlying reality changed should be flagged as possibly-stale at recall, not trusted blindly.

## Principle (owner decision, pending)

- Facts about volatile environment state (hosts, URLs, ports, credentials, external endpoints) are the highest staleness risk and should carry the strongest freshness expectation.
- Governance, not deletion: a staleness signal + recall-time flag is the right shape; purging facts on sight loses legitimate long-lived knowledge.
- Revisit only with a concrete mechanism proposal — do not over-build now (YAGNI until a second case appears).

## Resolution applied (closes #172)

- `entryRanking` (graph_memory.go) now appends the fact's age to its match rationale on the **live** graph only (`useEmbedder`): `age: Nd` past a 24h fresh grace, `age: Nd (possibly stale)` past a 30d threshold.
- Reuses the existing `At` field — **no schema change**. The field was write-only until now.
- Ranking score is untouched; this is purely visibility, so no recall-order or behavior regression.
- `Feedback` (-5..+5) still drives active *rejection* expiry (MEM-08); this adds the missing *passive/time-based* freshness on top.
- Regression test: `TestEntryRankingSurfacesStaleness` (graph_memory_freshness_test.go) — fresh fact shows no age, week-old shows `age: 6d` but not stale, >30d shows `age: Nd (possibly stale)`, and the archive path shows no age.

## Acceptance criteria (met)

- [x] Reuse the existing `At` field — no new field needed.
- [x] At recall, a fact past the freshness window is surfaced as *possibly-stale* (age in the rationale), not trusted silently.
- [x] Witnessing metric satisfied: a fact whose reality changed is now flagged by age at the next recall.
- [x] GitHub issue #172 opened and closed by the implementing commit.
