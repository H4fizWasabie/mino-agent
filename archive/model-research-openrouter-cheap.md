# OpenRouter Cheap Models — ≤ $0.10/M input, agent-capable

Researched 2026-08-07 against the live OpenRouter catalog API
(`GET https://openrouter.ai/api/v1/models`, 400 models).

## Selection criteria (for Mino's workload)

- **Prompt price ≤ $0.10 per 1M tokens** (per-token prices × 1e6)
- **`tools` in `supported_parameters`** — Mino is a tool-calling agent; no tools = disqualified
- **Text input modality**
- **Context ≥ 64k** (Mino's sessions are long)
- Prefer models with a **cache-read discount** — Mino's cost driver is prompt tokens (large system prompt + history); the v2.3.0 cache-stability work makes cached reads the norm, so `input_cache_read` price matters as much as list price

## Current baseline

| Model | IN/M | CACHE-RD/M | OUT/M | CTX | Effective IN/M at 86% cache hit |
|---|---|---|---|---|---|
| xiaomi/mimo-v2.5 (current) | $0.140 | $0.0028 | $0.280 | 1.05M | **≈ $0.022** |

Note: mimo's list price ($0.14/M) is above the $0.10 budget, but its cache-read price ($0.0028/M) is the cheapest of any candidate and Mino's Xiaomi upstream is stable enough to sustain an 86% cache hit rate (measured in usage.jsonl, 2026-08-07). Effective input cost ≈ $0.022/M — cheaper than gpt-5.6-luna (~$0.0226) at the same hit rate. **Decision (2026-08-07): stay on mimo.** Cost is not a reason to switch; the anti-laziness guards (v2.3.0) address the quality concerns.

## Top candidates (ranked by input price)

| Model | IN/M | CACHE-RD/M | OUT/M | CTX | Notes |
|---|---|---|---|---|---|
| inclusionai/ling-3.0-flash | $0.021 | $0.0042 | $0.063 | 262k | cheapest agent-capable; tiny vendor |
| qwen/qwen3.7-flash | $0.030 | $0.0060 | $0.130 | 1M | cheapest with 1M ctx |
| qwen/qwen3-30b-a3b-instruct-2507 | $0.048 | — | $0.193 | 262k | MoE, solid Qwen quality |
| openai/gpt-5-nano | $0.050 | $0.005 | $0.400 | 400k | OpenAI brand reliability |
| z-ai/glm-4.7-flash | $0.060 | $0.010 | $0.400 | 202k | GLM 4.7 family |
| qwen/qwen3.5-flash-02-23 | $0.065 | — | $0.260 | 1M | |
| **deepseek/deepseek-v4-flash-0731** | **$0.090** | **$0.018** | **$0.180** | 1M | Mino's previous model; known-good tool discipline |
| mistralai/mistral-small-3.2-24b-instruct | $0.094 | — | $0.250 | 256k | |
| openai/gpt-5.6-luna | $0.100 | $0.010 | $0.600 | 1.05M | used in Mino's usage history |
| google/gemini-2.5-flash-lite | $0.100 | $0.010 | $0.400 | 1M | |
| qwen/qwen3.5-9b | $0.100 | — | $0.150 | 262k | cheapest OUT/M at the cap |

All verified: `tools: True`, `reasoning` param supported, text modality.

## Recommendation for Mino

Mino's real cost is **prompt tokens with cache hits**. Two angles:

1. **Cheapest overall**: `qwen/qwen3.7-flash` — $0.03/M in, $0.006/M cached, 1M ctx. 4.6× cheaper than mimo on uncached, 2.1× on cached reads.
2. **Known-good swap**: `deepseek/deepseek-v4-flash-0731` — $0.09/M in, $0.018/M cached. Was Mino's previous model (provider_manager has `providers.json.bak-deepseek`); the user switched away from mimo *to* deepseek once before for tool-call discipline reasons, then back to mimo. Deepseek at $0.09 is still under budget and its cache-read ($0.018) is close to mimo's ($0.0028) on absolute terms.

Sleeper pick: `openai/gpt-5.6-luna` at exactly $0.10/M — already appears in Mino's usage.jsonl (1312 calls), so it's known to work with the current provider routing.

## Sources

- OpenRouter models catalog API (live, fetched 2026-08-07): `GET https://openrouter.ai/api/v1/models`
- Mino usage history: `/home/mino/.mino/usage.jsonl` (VPS)
- Mino provider config: `/home/mino/.mino/providers.json` (VPS)
