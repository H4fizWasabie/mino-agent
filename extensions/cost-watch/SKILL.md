---
description: 'Check model prices and detect promo expiry via the cost-watch extension. Triggers: price, cost, promo, budget, expensive, cheaper, provider pricing.'
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

**Alert-only by policy (REL-01): cost-watch pages, it never swaps. Model changes are human decisions — say so and leave the change to the owner.**

### Status
- `cost_watch_status` — current best provider prices for the configured models (the REL-01 policy models: deepseek:deepinfra, qwen3.7-flash), last check time, and flags.
- Use it when Abah asks about prices, costs, or "what are we paying".
- Include the numbers in your answer: best provider, input price per M, and whether the flag is OK.

### Refresh
- `cost_watch_check` — scrape the OpenRouter pages NOW and return fresh prices + flags.
- Use it when Abah asks for current/live prices, or when a status flag says "not checked".
- A "PRICE SPIKE" flag means a promo expired: the best price jumped past expected × threshold. Report it — do not change providers.json.

### Context
- The brain policy: main/stages + small role deepseek-v4-flash-0731:deepinfra (pinned DeepInfra), fallback+vision qwen3.7-flash (see providers.policy.json).
- Cheap prices matter — promos expire silently. If a price spike contradicts the policy table, surface it to Abah.
