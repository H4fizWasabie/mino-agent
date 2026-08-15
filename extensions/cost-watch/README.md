# mino cost-watch — the price guardian

Watches the prices Mino pays. Scrapes OpenRouter per-provider pricing
hourly, **re-pins the brain's routing to the cheapest eligible host** (the
promo riders), and pages on the one case pinning cannot fix: every eligible
host above threshold. Silent for pin changes — no owner paging on routine
re-orders.

Mino's provider stack runs on promotional pricing. When a promo expires the
cheapest host changes; cost-watch chases it, and the pager is the signal for
the case where nothing cheap is left.

## How it works

```
hourly (service refresh loop)
  └─ scrape the endpoints API → per-provider pricing + promo discount
       └─ rank eligible hosts by price (curated `trains` = hard-excluded)
            └─ order changed? → rewrite provider_routing (backup first)
                 └─ SIGHUP mino → hot-reload, no restart
hourly (systemd timer)
  └─ best price > expected × threshold? → Telegram alert (pin can't fix it)
```

Eligibility: `data_handling` curated in config — `zdr` (zero retention),
`cache_only` (retains prompts for caching, never trains — owner-vetted, e.g.
Baidu), `unknown` are all eligible; `trains` (e.g. DeepSeek's own endpoint)
never appears in routing regardless of price. Promos win by being cheap:
routing is a price-sorted list (top 5), so the discounting host leads while
its promo lasts and sinks back when it expires.

The scraper is deterministic — no LLM, no API key, just the public endpoints
API. Mino is a sidecar service using the standard extension protocol
(DECISIONS.md §8). A single static Go binary — no python, no runtime
dependencies.

## Install

```bash
go build -o cost-watch .   # static binary, no dependencies
sudo mkdir -p /opt/mino-cost-watch
sudo cp cost-watch /opt/mino-cost-watch/
sudo cp cost-watch.service cost-watch-check.service cost-watch.timer cost-watch-check.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now cost-watch.service cost-watch.timer cost-watch-check.timer
```

Then register it in `~/.mino/extensions.json`:

```json
[
  {"name": "cost-watch", "url": "http://127.0.0.1:9300"}
]
```

Restart mino, and the tools appear: `cost_watch_status`, `cost_watch_check`,
`cost_watch_refresh`. The mino core must run a build with the SIGHUP reload
handler (providers.json hot-reload) for pins to apply without a restart.

## Config (`~/.mino/cost-watch.json`)

Owned by the `mino` user so the model can edit its own watchdog (CTX-020 hot-reload — a root-owned `/etc` file would make that dead on arrival). Legacy installs: `/etc/mino-cost-watch.json` is still read as a fallback.

```json
{
  "models": {
    "deepseek/deepseek-v4-flash-0731:deepinfra": {"url": "https://openrouter.ai/deepseek/deepseek-v4-flash-0731", "expected": 0.08, "threshold": 2.0},
    "qwen/qwen3.7-flash": {"url": "https://openrouter.ai/qwen/qwen3.7-flash", "expected": 0.03, "threshold": 2.0}
  },
  "data_handling": {
    "DeepInfra": "zdr",
    "Together": "zdr",
    "Fireworks": "zdr",
    "Cloudflare": "zdr",
    "DeepSeek": "trains",
    "Baidu": "cache_only"
  },
  "telegram_chat_id": ""
}
```

- `expected` — the price you pay today ($/M input) — the REL-01 policy
  prices; keep in sync with `cost.go` in the main repo
- `threshold` — alert when the best price exceeds `expected × threshold`
- `data_handling` — per-provider curation: `zdr` | `cache_only` | `trains` |
  `unknown` (unlisted). `trains` providers are hard-excluded from routing;
  everything else is eligible and ranked by price.

Telegram alerts use `TELEGRAM_BOT_TOKEN` + `MINO_TELEGRAM_CHAT_ID` from
mino.env (or `telegram_chat_id` in the config).

## Safety

- `trains` providers never enter routing, whatever their price (test-enforced)
- Every pin write is preceded by a backup (`providers.json.bak-pin`) and is
  atomic (tmp + rename); unchanged order = no write, no signal
- A failed SIGHUP only delays the pin to the next restart — the write is
  already on disk
- The scraper is fail-visible: a changed page structure alerts instead of
  silently passing

## Tests

```bash
go test ./...
```

No network needed — pricing fixtures are embedded.
