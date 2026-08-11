# mino cost-watch — the price guardian

Watches the prices Mino pays. Scrapes OpenRouter model pages hourly, detects
when a promotional price expires, and alerts on Telegram. **Alert-only by
policy (REL-01): it pages the owner — it never changes the brain.** Model
changes are human decisions.

Mino's provider stack often runs on promotional pricing. When a promo
expires the billed cost jumps with no signal. cost-watch is that signal.

## How it works

```
hourly (systemd timer, root)
  └─ scrape the model pages → parse per-provider pricing
       └─ best price > expected × threshold? → Telegram alert
```

The scraper is deterministic — no LLM, no API key, just the public model
pages. Mino itself stays untouched: cost-watch is a sidecar service using
the standard extension protocol (DECISIONS.md §8). A single static Go
binary — no python, no runtime dependencies.

## Install

```bash
go build -o cost-watch .   # static binary, no dependencies
sudo mkdir -p /opt/mino-cost-watch
sudo cp cost-watch /opt/mino-cost-watch/
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

Restart mino, and the tools appear: `cost_watch_status`, `cost_watch_check`.

## Config (`/etc/mino-cost-watch.json`)

```json
{
  "models": {

    "deepseek/deepseek-v4-flash-0731:deepinfra": {"url": "https://openrouter.ai/deepseek/deepseek-v4-flash-0731", "expected": 0.08, "threshold": 2.0},
    "qwen/qwen3.7-flash": {"url": "https://openrouter.ai/qwen/qwen3.7-flash", "expected": 0.03, "threshold": 2.0}
  },
  "telegram_chat_id": ""
}
```

- `expected` — the price you pay today ($/M input) — the REL-01 policy
  prices; keep in sync with `cost.go` in the main repo
- `threshold` — alert when the best price exceeds `expected × threshold`

Telegram alerts use `TELEGRAM_BOT_TOKEN` + `MINO_TELEGRAM_CHAT_ID` from
mino.env (or `telegram_chat_id` in the config).

## Safety

- No tool or check path can rewrite `providers.json` — the swap capability
  was removed (issue #128): an LLM-callable brain-swap tool is how the
  luna surprises happened
- The scraper is fail-visible: a changed page structure alerts instead of
  silently passing

## Tests

```bash
go test ./...
```

No network needed — pricing fixtures are embedded.
