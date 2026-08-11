# Context Truth — Verification discipline

Status: **RESOLVED** (closes GitHub issue #149)

## Question

How do we make a user-named number vs a computed number produce a stated discrepancy instead of a smoothed-over "essentially correct"?

## Resolution

The static system prompt (buildSystem, session.go — byte-stable, prompt-cache safe) gains a Number verification rule:

> When the user names a value and your computation differs, state BOTH numbers and the gap in your reply — a mismatch is a finding, never something to smooth over. Never reply 'essentially correct', 'close enough', or 'your memory is right' while reporting a different number. A number is only 'verified' when it comes from the source of truth — the app's own definition (source, API response), or the schema column that defines the filter — never from an invented query that happens to land nearby.

Model-side only, no new tools (per VFY-003: keep model-side changes minimal). Rides the existing VFY map.

## Acceptance criteria (all met)

- [x] Rule present in static system prompt
- [x] Test asserting the rule rides BuildSystem (`TestBuildSystemIncludesNumberVerificationRule`)

## Validation

- `go test ./...` — 503 pass
- Production observation pending: next discrepancy turn should state both numbers
