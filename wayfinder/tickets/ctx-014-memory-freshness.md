# Context Truth — Memory facts have no freshness/staleness governance

Status: **OPEN** (wayfinder ticket, no GitHub issue yet)

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

## Acceptance criteria (to be met when scoped)

- [ ] Reuse the existing `At` field — **no new field needed**. The missing piece is wiring, not schema.
- [ ] At recall, a fact whose `At` age exceeds a threshold is surfaced as *possibly-stale* (age in the match rationale), not trusted silently. (Optional: mild recency boost for fresh facts — but age-flagging is the core.)
- [ ] Witnessing metric: when a fact's underlying reality changes, the next recall flags its age rather than acting on it blindly.
- [ ] No GitHub issue opened yet — open one if/when this is scoped for implementation.
