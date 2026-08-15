# Cloudflare Workers AI — Text-to-Image model research (2026-08-15)

Purpose: pick a decent text-to-image model for Mino's daily image generation
(instagram/facebook/threads playbook images, ~8-10 images/day, 1024x1024).

**Constraint: free Workers AI plan = 10,000 neurons/day.** All numbers below
are recomputed in neurons (the free-tier meter), not tokens.

Source: Cloudflare official docs + pricing catalogue
(`developers.cloudflare.com/workers-ai/llms-full.txt`, fetched 2026-08-15),
account 3a538ce73fb41c85697966b20d9fc5d6.

## The catalogue (text-to-image task) — priced in NEURONS (free-tier meter)

A 1024x1024 image = 4 tiles of 512x512. Daily budget: 10,000 neurons.

| Model (API id) | Neurons per 1024x1024 img | Images/day cap (10k) | vs current | Free-tier verdict |
|---|---|---|---|---|
| `@cf/black-forest-labs/flux-1-schnell` (current) | ~29-58 (19.2 tiles + 9.6×steps) | ~175-350 | 1x | fine |
| `@cf/black-forest-labs/flux-2-klein-4b` | ~126 (4×5.37 input + 4×26.05 output) | ~80 | ~2-4x | **fits** |
| `@cf/black-forest-labs/flux-2-klein-9b` | ~1,364 (first MP) | ~7 | ~24x | **too expensive** |
| `@cf/leonardo/phoenix-1.0` | ~2,120-2,370 (4×530 + 10×steps) | ~4 | ~38x | **too expensive** |
| `@cf/black-forest-labs/flux-2-dev` | ~6,300 (225×steps @28) | ~1.6 | ~100x | **out** |
| `@cf/stabilityai/stable-diffusion-xl-base-1.0` | older base model (Beta) | — | — | — |
| `@cf/bytedance/stable-diffusion-xl-lightning` | fast, few steps (Beta) | — | — | — |

Mino's real consumption is 2-3x the published image count: each daily run
critiques and regenerates once (instagram did 3 attempts on 08-14). Real
need ≈ 15-25 generations/day.

- klein-4b: 15-25 × 126 ≈ **1,900-3,150 neurons/day (19-32% of budget)** ✓
- phoenix: 15-25 × 2,370 ≈ 3.5-6x the daily budget ✗ — one instagram run
  with retries alone eats ~70% of the day
- klein-9b: 2-3.4x budget ✗ — the 1,364/first-MP charge is a free-tier killer
- flux-2-dev: 6,300/image ✗✗

## Recommendation for Mino's workload

Mino's quality bar (from the playbook stage contracts) is NOT photorealism —
it is: on-topic, **text-free, no logos, no garbled artifacts**, prompt
adherence, square 1024x1024, cheap. Observed failure class: instagram images
rejected for visible letters/garbling (2026-08-14).

1. **Default: `@cf/black-forest-labs/flux-2-klein-4b`** — the only real quality
   upgrade that survives the 10k-neuron free tier. ~126 neurons/image (~1.3% of
   the day per image), a full day of playbooks burns ~20-30%. Same BFL family
   as today (drop-in). "Ultra-fast, distilled, state-of-the-art quality" — and
   its pricing is step-independent (input+output tiles only), so no step-count
   surprises.
2. **Keep flux-1-schnell as the budget fallback** — ~29-58 neurons/image at its
   1-4 step design; a 10k-day fits hundreds. Useful if a run ever needs many
   cheap attempts.
3. **phoenix-1.0: manual/hero images only, NOT the daily pipeline** — its
   "coherent text / prompt adherence" is exactly the instagram rejection class,
   but at ~2,370 neurons/image it caps at ~4 images/day. One instagram run with
   its 3-attempt flow burns ~70% of the day's budget. Keep it as the "quality
   bar" tool for one-off images you hand-prompt.
4. **Out on free tier:** klein-9b (1,364/first MP), flux-2-dev (6,300/image).

## Live test results (2026-08-15, real playbook runs + view_image judge)

Same golden-key prompt, judged by the playbook's own `view_image` critic
unless noted:

| Model | Neurons/1024² | Judge verdict | Fit 15-25 gens/day |
|---|---|---|---|
| **flux-1-schnell** (reverted to) | ~29-58 | **PASS** (controlled golden-key test) | ✓✓ |
| flux-2-klein-4b | ~126 | 1 pass / 4 attempts (run 2 published a post) | ✓ |
| flux-2-klein-9b | 1,364 | REJECT — "severe pixelation/aliasing" (122KB output) | ✗ |
| sd-xl-base-1.0 (free, unlisted) | 0 | REJECT — "garbled artifacts" | ✓ cost-wise |
| sd-xl-lightning (free, unlisted) | 0 | REJECT — "severe garbled artifacts" | ✓ cost-wise |
| phoenix-1.0 | 2,370 | untested | ✗ (4/day cap) |
| lucid-origin | 2,844 | untested | ✗ (3.5/day cap) |
| flux-2-dev | ~6,300 | untested | ✗✗ |

Pattern: rejected outputs were consistently ~110-130KB (undercooked defaults);
passing outputs ~280-350KB. Models needing explicit steps/guidance (SDXL,
klein-9b) underperform because Mino's tool sends prompt-only. Among models
that produce good output with prompt-only defaults (schnell, klein-4b) the
pass rate is comparable (~1/3 per attempt); the retry loop absorbs it.

**Verdict: on the free 10k-neuron tier there is no Cloudflare model that beats
flux-1-schnell for this workload.** Every higher-quality claim costs 10-50x
and cannot serve the daily pipeline; every free/budget fit fails the bar.
klein-4b is the only comparable alternative (one env var away — the multipart
support ships in the binary). The real quality unlock is off-tier: the
OpenRouter Gemini tier-2 fallback (~$0.0015/image, already in the chain) or
paid neurons.

## Change mechanics (tiny)

- The knob is one env var: `MINO_IMAGE_MODEL=@cf/black-forest-labs/flux-2-klein-4b`
  (or `@cf/leonardo/phoenix-1.0`) in `~/.mino/mino.env` — `generateWithCloudflare`
  (tools.go:1986) already reads it and handles both raw-image and JSON-envelope
  responses. No code change needed to switch.
- Optional code improvement: the tool currently sends only `{"prompt"}`. Phoenix
  and klein support `steps`/`guidance`/`seed`; passing them (e.g. steps:25,
  guidance:4 per Cloudflare's phoenix example) would add control later.
- Partner models may require enablement on the account (check the dashboard
  model list the user linked).
