# Design: GLM precision-aware routing + repetition_penalty

## Problem

GLM-5.3-flash decode failures today always hit `MINO_MAX_TOKENS` (16384) on a non-streamed
completion. cost-watch's autonomous provider-pinning has no concept of quantization (current
routing includes an fp4 endpoint); Mino never sends `repetition_penalty` despite most routed
backends supporting it.

## Approach

Single approach, owner-specified in live discussion — not a menu. Teach cost-watch's existing
ranking about a precision floor (fp8-or-better beats known-worse), and add `repetition_penalty`
to every OpenRouter request. Rejected: hardcoding a 3-provider routing list directly in
`providers.json` (fights cost-watch's own autonomous re-pinning, explicitly rejected by owner).

## Interfaces

| Name | Signature | Purpose |
|------|-----------|---------|
| `catalogueEntry.Quantization` | `Quantization string \`json:"quantization"\`` | New field, sourced from the endpoints API's existing `quantization` value (already present upstream, previously discarded). |
| `precisionTier` | `func precisionTier(quant string) int` | Returns 1 for a known-worse-than-fp8 set (`fp4`,`fp2`,`fp1`,`int4`,`int2`, case-insensitive), 0 otherwise. Floor, not a ladder. |
| `rankCatalogueEntries` | unchanged signature | Sort gains precision tier as the first key, ahead of the existing price-completeness/price/uptime/latency chain, which is otherwise unchanged. |
| `repetitionPenalty` | `func repetitionPenalty() float64` | Reads `MINO_REPETITION_PENALTY`, default `1.1` on absent/invalid — matches existing `MINO_*` env pattern in `config.go`. |
| `createWithRouting` payload | adds `payload["repetition_penalty"]` | Sent unconditionally; OpenRouter's normalization layer drops params a specific backend doesn't support rather than erroring (assumption, spot-check live in verify stage). |

## Config Surface

| Key | Type | Default | When absent |
|-----|------|---------|-------------|
| `MINO_REPETITION_PENALTY` | float | `1.1` | Default applies silently, no error. Invalid (non-numeric) also falls back to default. |

## Data Flow

1. cost-watch's hourly catalogue refresh fetches `/api/v1/models/{slug}/endpoints`, now also
   reads `quantization` per endpoint into `catalogueEntry`.
2. `rankCatalogueEntries` sorts precision tier first, then the existing chain unchanged within
   a tier. `pinOrder` (unchanged) takes the top `maxPins` and rewrites `provider_routing`.
3. Every `createWithRouting` call reads `MINO_REPETITION_PENALTY` (or default) and includes it
   in the payload sent to OpenRouter, regardless of which provider ends up serving the request.

## Failure Behaviour

| Failure | Behaviour |
|---------|-----------|
| Endpoint's `quantization` field missing/empty in API response | Treated as top tier (0) — same as "unknown", not penalized (no evidence it's bad). |
| `MINO_REPETITION_PENALTY` unset or non-numeric | Default `1.1` applies, no error, no log noise. |
| A backend rejects `repetition_penalty` outright (not just ignores it) | Not mitigated in this change — flagged as a live-verify item, not solved speculatively; no backend in the current routing chain is known to hard-reject it (Z.AI omits it from `supported_parameters` but OpenRouter's own docs describe unsupported params as dropped, not rejected). |

## Invariant Check

| Invariant | Verdict | Note |
|-----------|---------|------|
| Model Agnosticism | Held | `repetition_penalty` is a generic sampling parameter, not provider-specific; quantization ranking applies to any model cost-watch catalogues, not one hardcoded provider. |
| Loop Termination | Held | No loop changes. |
| Context Is Managed, Never Assumed | Held (unaffected) | Not a context-management change. |
| Guardrails Are Not Optional | Held | `eligibleForPin`'s hard `trains` exclusion is untouched and still runs before ranking. |
| Failure Is Explicit | Held | See Failure Behaviour table. |
| State Stays Local and Inspectable | Held | No new persisted state; `providers.json` rewrite is cost-watch's existing, already-inspectable mechanism. |
| Single Binary, No Framework | Held | No new dependency. |

## Files to Touch

- `extensions/cost-watch/main.go`: `catalogueEntry`, endpoint-parsing struct, `rankCatalogueEntries`,
  doc comments on `pinOrder`/`rankCatalogueEntries`.
- `extensions/cost-watch/main_test.go`: new precision-tier tests.
- `extensions/cost-watch/README.md` / `SKILL.md`: update ranking-order prose if present.
- `provider.go`: `createWithRouting` payload, new `repetitionPenalty()` helper.
- New/existing provider test file: repetition_penalty default/override coverage.

## Out of Scope

- `MINO_MAX_TOKENS` (owner explicit).
- Manual `providers.json` routing pin (owner explicit).
- `frequency_penalty`/`presence_penalty`.
- Fine-grained precision ladder beyond the fp8 floor.
