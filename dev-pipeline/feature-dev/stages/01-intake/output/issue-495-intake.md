# Intake: GLM precision-aware routing + repetition_penalty

Issue: #495. Live incident, 2026-08-31. Owner decision already made in live discussion — this
intake records it, not re-derives it.

## Problem

Mino's OpenRouter GLM-5.3-flash calls repeatedly hit a decode-time repetition-collapse failure
today, always truncated at exactly `MINO_MAX_TOKENS` (16384) on a single non-streamed
completion. Two contributing gaps, both verified against OpenRouter's live endpoints API:
cost-watch's autonomous provider-pinning ranks purely by price/uptime/latency with no concept
of quantization (the current routing chain includes an fp4 endpoint, the most aggressively
quantized of the five, a known contributor to this failure shape), and Mino's own request
payload never sends `repetition_penalty`, a standard mitigation 4 of the 5 current backends
support.

## Rejection check

Not on the "Do Not Build" list (provider-specific feature flags; embedding a scripting
runtime). This does not add a provider-specific flag — quantization/repetition_penalty are
generic OpenRouter API concepts applied uniformly, not one provider special-cased.

## Decision

1. `extensions/cost-watch/main.go`: add `Quantization` to `catalogueEntry` (field already in
   the endpoints API response, currently discarded), add a precision tier as the **first** key
   in `rankCatalogueEntries`'s sort — floor/threshold shape, not a fine-grained ladder. Only
   `{fp4, fp2, fp1, int4, int2}` (case-insensitive) rank behind; fp8/fp16/bf16/fp32/unknown/
   empty all rank in the same top tier together. Existing price → uptime → latency chain is
   unchanged within a tier.
2. `provider.go`: add `repetition_penalty` to the OpenRouter payload unconditionally, value
   from `MINO_REPETITION_PENALTY` (float, default `1.1`).

## Non-goals

- `MINO_MAX_TOKENS` unchanged (owner explicit: not the fix surface).
- No manual/hardcoded provider pin in `providers.json` — cost-watch's own next catalogue
  refresh re-pins automatically once it understands precision.
- No `frequency_penalty`/`presence_penalty` — only `repetition_penalty` was approved.
- No fine-grained precision ladder (fp8 vs fp16 vs bf16 ordering) — floor only.

## Surfaces touched

- `extensions/cost-watch/main.go` (catalogue struct, ranking, doc comments)
- `extensions/cost-watch/README.md` / `SKILL.md` if they describe ranking order
- `provider.go` (`createWithRouting` payload)
- `config.go`-pattern env read for the new key

## Acceptance criteria

1. A cheaper fp4 endpoint ranks below a pricier fp8 endpoint for the same model.
2. Two fp8 endpoints still rank by price → uptime → latency exactly as before (no regression).
3. An "unknown"-quantization endpoint ranks in the same tier as fp8, not penalized.
4. Every OpenRouter request includes `repetition_penalty` (configured or default 1.1).
5. `MINO_REPETITION_PENALTY` absent or invalid → default 1.1, no error.
6. Full test suite (root + `extensions/cost-watch`) passes.
