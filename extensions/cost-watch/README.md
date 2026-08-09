# mino cost-watch — the price guardian

Keeps Mino on the cheapest model. Scrapes OpenRouter model pages hourly,
detects when a promotional price expires, alerts on Telegram, and can swap
`providers.json` to the next model in the chain — with a backup and an
atomic restart.

Mino's provider stack often runs on promotional pricing (e.g. GLM 5.2 at
93% off via StreamLake, luna-pro at 50% off via OpenAI). When a promo
expires the billed cost jumps 7-20× with no signal. cost-watch is that
signal — and the wrench.

## How it works

```
hourly (systemd timer, root)
  └─ scrape the model pages → parse per-provider pricing
       └─ best price > expected × threshold? → Telegram alert
            └─ "swap to luna" → backup + rewrite providers.json → restart mino
```

The scraper is deterministic — no LLM, no API key, just the public model
pages. Mino itself stays untouched: cost-watch is a sidecar service using
the standard extension protocol (DECISIONS.md §8).

## Install

```bash
sudo mkdir -p /opt/mino-cost-watch
sudo cp cost_watch.py /opt/mino-cost-watch/
sudo cp cost-watch.service cost-watch-check.service cost-watch.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now cost-watch.service cost-watch.timer
```

Then register it in `~/.mino/extensions.json`:

```json
[
  {"name": "cost-watch", "url": "http://127.0.0.1:9300"}
]
```

Restart mino, and the tools appear: `cost_watch_status`, `cost_watch_check`,
`cost_watch_swap`.

## Config (`/etc/mino-cost-watch.json`)

```json
{
  "models": {
    "glm-5.2":  {"url": "https://openrouter.ai/z-ai/glm-5.2",          "expected": 0.098, "threshold": 2.0},
    "luna-pro": {"url": "https://openrouter.ai/openai/gpt-5.6-luna-pro", "expected": 0.10,  "threshold": 2.0}
  },
  "chain": ["glm-5.2", "luna-pro", "qwen"],
  "telegram_chat_id": ""
}
```

- `expected` — the promo price you currently pay ($/M input)
- `threshold` — alert when the best price exceeds `expected × threshold`
- `chain` — the swap order (qwen needs no promo — the safe final step)
- `provider_templates` — full provider entries per chain model (edit to
  match your stack; backups are written before every swap)

Telegram alerts use `TELEGRAM_BOT_TOKEN` + `MINO_TELEGRAM_CHAT_ID` from
mino.env (or `telegram_chat_id` in the config).

## Safety

- Every swap writes `providers.json.bak-cost-watch` first
- Restarts are deferred while a playbook run is in flight (run-locks guard,
  same rule as the self-updater)
- The scraper is fail-visible: a changed page structure alerts instead of
  silently passing

## Tests

```bash
python3 test_cost_watch.py
```

No network needed — pricing fixtures are embedded.
