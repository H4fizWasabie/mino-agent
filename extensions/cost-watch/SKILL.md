---
description: 'Check model prices, detect promo expiry, and see/trigger the autonomous routing pin via the cost-watch extension. Triggers: price, cost, promo, budget, expensive, cheaper, provider pricing.'
name: cost-watch
triggers:
    - price
    - cost
    - promo
    - budget
    - expensive
    - cheaper
    - provider pricing
---

## Cost Watch — the price guardian

Use the `cost_watch_*` tools whenever prices, costs, or promos come up. Do not wait to be asked about the tools — use them, then answer.

**Pinning is autonomous: every catalogue refresh re-ranks the brain's routing to the best eligible host — precision floor first (fp8+ beats known weaker quantizations like fp4), then cheapest (trains providers hard-excluded) — and hot-reloads mino via SIGHUP — silent, no owner paging. Your job is reporting, not deciding: state what's pinned and why, never promise a model/host change the owner must approve.**

### Status
- `cost_watch_status` — current best provider prices for the configured models (the REL-01 policy models: deepseek:deepinfra, qwen3.7-flash), last check time, and flags.
- Use it when Abah asks about prices, costs, or "what are we paying".
- Include the numbers in your answer: best provider, input price per M, and whether the flag is OK.
- To answer "who is pinned right now", read `~/.mino/providers.json` — `provider_routing` is the live order.

### Refresh
- `cost_watch_check` — scrape the OpenRouter pages NOW and return fresh prices + flags.
- `cost_watch_refresh` — refresh the per-provider price catalogue AND chase the pin (the same job the hourly loop runs). Returns "pinned <provider>: <order>; mino reloaded" when the order changed, "pins unchanged" otherwise.
- Use it when Abah asks for current/live prices, or when a status flag says "not checked".
- A "PRICE SPIKE" flag means every eligible host is above expected × threshold — pinning can't fix it, report it.

### Context
- The brain policy: main/stages + small role deepseek-v4-flash-0731 (routing pinned by cost-watch), fallback+vision qwen3.7-flash (unpinned — see providers.policy.json).
- Eligibility is curated `data_handling`: zdr / cache_only / unknown ride the price sort; `trains` (e.g. DeepSeek's own endpoint) is hard-excluded — never suggest or route to a trains provider regardless of price.
- Cheap prices matter — promos expire silently, and the pin chases them hourly. If a price spike contradicts the policy table, surface it to Abah.
