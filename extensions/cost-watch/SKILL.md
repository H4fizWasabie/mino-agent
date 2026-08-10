---
description: 'Check model prices, detect promo expiry, and swap providers via the cost-watch extension. Triggers: price, cost, promo, budget, model swap, expensive, cheaper, provider.'
name: cost-watch
triggers:
    - price
    - cost
    - promo
    - budget
    - swap model
    - expensive
    - cheaper
    - provider pricing
---

## Cost Watch — the price guardian

Use the `cost_watch_*` tools whenever prices, costs, promos, or model swaps come up. Do not wait to be asked about the tools — use them, then answer.

### Status
- `cost_watch_status` — current best provider prices for the configured models (luna-pro), last check time, and flags.
- Use it when Abah asks about prices, costs, or "what are we paying".
- Include the numbers in your answer: best provider, input price per M, and whether the flag is OK.

### Refresh
- `cost_watch_check` — scrape the OpenRouter pages NOW and return fresh prices + flags.
- Use it when Abah asks for current/live prices, or when a status flag says "not checked".
- A "PRICE SPIKE" flag means a promo expired: the best price jumped past expected × threshold.

### Swap (only with Abah's approval)
- `cost_watch_swap <model>` — swap providers.json to a chain model (luna-pro → qwen) and restart mino.
- NEVER call swap unprompted. Present the situation (flag + prices + recommendation) and ask: "swap to X? it restarts me for ~15 seconds".
- On approval, call swap with the agreed model, then confirm the new primary after restart.

### Context
- The stack: primary model + qwen fallback + deepseek background jobs. Cheap prices matter — promos expire silently.
- Restarts are deferred automatically while a playbook run is in flight.
