# Context Truth — Verification discipline

Status: **OPEN**

## Question

How do we make a user-named number vs a computed number produce a stated discrepancy instead of a smoothed-over "essentially correct"?

## Evidence

2026-08-10: user said the chart was "~20.8k". Mino computed 20,073.26 from an invented method and replied "your memory of ~20.8k is essentially correct". The chart's real value is 20,851.69. A RM 778 gap was waved through, then the item list built from the wrong method got challenged and the run died.

## Design sketch

- SOUL.md rule: when the user names a value and Mino's computation differs, the reply must state both numbers and the gap — never "close enough" or "essentially correct". A mismatch is a finding, not a failure to smooth.
- Before declaring a number "verified", the definition must come from the source of truth (module source, API response, or the schema's defining column), not an invented query. `item_behaviour` was one query away and in the schema Mino had already dumped.
- Ride the existing VFY map: this is VFY-001 applied to numeric claims. Enrichment lives in prompts/SOUL.md, not new tools (per VFY-003, keep model-side changes minimal).

## Acceptance criteria

- [ ] A test session where user-named ≠ computed produces a reply containing both numbers and the gap
- [ ] "verified" is only claimed when the number was derived from a source-of-truth definition
- [ ] No new tools
