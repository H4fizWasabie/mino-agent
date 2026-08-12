# Context Truth — Memory facts have no freshness/staleness governance

Status: **OPEN** (wayfinder ticket, no GitHub issue yet)

## Question

Why did Mino keep posting via the wrong (HTTP datacenter) image URL on Facebook — even after the real URL changed, and even after the skill and the memory fact were both corrected to HTTPS?

## Root cause

Recall works; **freshness does not exist.**

Mino's semantic memory recalled `public_image_hosting_setup` correctly on its `use_when` trigger ("when posting images"). The fact's *content* had gone stale: the public image host moved to an HTTPS Tailscale URL (`vultr-1.tail8e6639.ts.net`) and Facebook blocks the old `http://149.28.146.30` datacenter IP — but the fact still carried the old URL, so it kept driving the wrong behavior.

There is no `stale_after` / `last_verified` / expiry signal on a fact, and no recall-time freshness check. A fact, once written, is trusted indefinitely regardless of whether the world it describes changed. This is exactly the OKF `stale_after` field we reviewed and chose to skip on YAGNI — this is its first witnessed case.

## How it differs from CTX-013

CTX-013 was four *stale instructional* notes teaching a wrong workflow; the fix was ad-hoc deletion with no systemic mechanism. CTX-014 is the **missing mechanism itself**: no fact of any kind (instructional *or* factual) carries a freshness signal or gets re-validated. The fix is not "delete this fact" — the fact is now correct (we edited it to HTTPS). The gap is *governance*: a fact whose underlying reality changed should be flagged as possibly-stale at recall, not trusted blindly.

## Principle (owner decision, pending)

- Facts about volatile environment state (hosts, URLs, ports, credentials, external endpoints) are the highest staleness risk and should carry the strongest freshness expectation.
- Governance, not deletion: a staleness signal + recall-time flag is the right shape; purging facts on sight loses legitimate long-lived knowledge.
- Revisit only with a concrete mechanism proposal — do not over-build now (YAGNI until a second case appears).

## Acceptance criteria (to be met when scoped)

- [ ] A fact can carry a freshness marker (last-verified / stale_after).
- [ ] At recall, a fact past its freshness window is surfaced as *possibly-stale*, not trusted silently.
- [ ] Witnessing metric: when a fact's underlying reality changes, the next recall flags it rather than acting on it.
- [ ] No GitHub issue opened yet — open one if/when this is scoped for implementation.
