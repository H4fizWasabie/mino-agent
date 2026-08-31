# Implementation manifest: #495

Branch: `fix/issue-495-glm-precision-repetition-penalty`

## Files changed

- `extensions/cost-watch/main.go`: added `Quantization` to `catalogueEntry` and the
  endpoints-parsing struct (field already present upstream, previously discarded); new
  `precisionWorseThanFP8` set and `precisionTier(quant string) int` (floor, not a ladder —
  only `{fp4, fp2, fp1, int4, int2}` rank worse, everything else including "unknown"/empty
  ranks at the top tier); `rankCatalogueEntries`'s sort now checks precision tier first,
  ahead of the existing price-completeness/price/uptime/latency chain (unchanged within a
  tier); doc comments on `rankCatalogueEntries` updated to describe the new order.
- `extensions/cost-watch/main_test.go`: `TestPinOrderPrefersFP8OverCheaperFP4` (cheaper fp4
  must rank below pricier fp8), `TestPinOrderUnknownQuantizationNotPenalized` (unknown ranks
  with fp8, not behind it). Existing `TestPinOrderUsesUptimeThenLatencyAfterPrice` already
  proves same-tier ordering is unaffected (all its entries have empty `Quantization` → same
  tier), so no separate regression test was needed for that.
- `extensions/cost-watch/README.md`, `extensions/cost-watch/SKILL.md`: updated the two spots
  describing ranking as "by price" to mention the precision floor first.
- `provider.go`: `createWithRouting`'s payload now includes `repetition_penalty`, sourced from
  the existing `envFloat` helper (config.go) reading `MINO_REPETITION_PENALTY`, default `1.1`.
  Sent unconditionally.
- `provider_test.go`: `TestCreateContextSendsRepetitionPenaltyDefault`,
  `TestCreateContextSendsRepetitionPenaltyFromEnv` — real HTTP round-trip against a test
  server, inspecting the actual request body for the field (matches this file's existing
  `TestCreateJSONRetriesWithoutResponseFormat` pattern).
- `CHANGELOG.md`: new `[Unreleased]` entries (Added: repetition_penalty; Changed: cost-watch
  precision-first ranking).

## New config key

| Key | Type | Default |
|-----|------|---------|
| `MINO_REPETITION_PENALTY` | float | 1.1 |

Read via the existing `envFloat(key, fallback)` helper (config.go) — absent or non-numeric
value → default applies silently, no error. No new helper written; reused what was already
there.

## Build and test results

- Root module `go build ./...`: PASS.
- Root module `go test ./... -count=1`: 856 passed (854 existing + 2 new), 0 failed.
- `extensions/cost-watch` module `go build ./...` / `go test ./...`: PASS, 25 passed (23
  existing + 2 new).
- `gofmt -l` on every touched Go file: clean.

## Deviations from the design note

None. Every interface, config key, and default in the design note matches what's implemented.

## Deferred / not covered

- The design note's Failure Behaviour table flags "a backend rejects `repetition_penalty`
  outright" as unmitigated and unconfirmed — no backend in the current routing chain is known
  to hard-reject it (only Z.AI omits it from `supported_parameters`, and OpenRouter's own
  documented behavior is to drop unsupported params, not reject the request), but this is an
  assumption to be spot-checked against real traffic post-deploy, not proven here by a unit
  test (can't be — it depends on a specific backend's real behavior, not Mino's own code).
