#!/usr/bin/env python3
"""mino cost-watch — the price guardian.

Scrapes OpenRouter model pages for per-provider pricing, exposes the mino
extension protocol, and runs an hourly autonomous check that alerts on
Telegram when a promotional price expires.

Extension protocol (DECISIONS.md §8):
  GET  /tools    -> [{"name": "...", "schema": {...}}]
  POST /execute  -> {"tool": "...", "args": {...}} -> {"result": "..."}
  GET  /check    -> {"alert": bool, "message": "..."}
"""
import json
import os
import re
import subprocess
import sys
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

CONFIG_PATH = "/etc/mino-cost-watch.json"
MINO_ENV = "/home/mino/.mino/mino.env"
PROVIDERS_JSON = "/home/mino/.mino/providers.json"
RUN_LOCKS = "/home/mino/.mino/run-locks"
UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36"

DEFAULT_CONFIG = {
    "listen": "127.0.0.1",
    "port": 9300,
    "models": {
        "glm-5.2": {"url": "https://openrouter.ai/z-ai/glm-5.2", "expected": 0.098, "threshold": 2.0},
        "luna-pro": {"url": "https://openrouter.ai/openai/gpt-5.6-luna-pro", "expected": 0.10, "threshold": 2.0},
    },
    "chain": ["glm-5.2", "luna-pro", "qwen"],
    "provider_templates": {
        "glm-5.2": [
            {"name": "openrouter", "priority": 1, "base_url": "https://openrouter.ai/api/v1",
             "api_key_env": "MINO_OPENROUTER_KEY", "model": "z-ai/glm-5.2",
             "reasoning_effort": "high", "provider_routing": ["StreamLake"],
             "small_model": "deepseek/deepseek-v4-flash-0731",
             "small_reasoning_effort": "max", "small_provider_routing": ["DeepInfra"]},
            {"name": "qwen-fallback", "priority": 2, "base_url": "https://openrouter.ai/api/v1",
             "api_key_env": "MINO_OPENROUTER_KEY", "model": "qwen/qwen3.7-flash"},
        ],
        "luna-pro": [
            {"name": "openrouter", "priority": 1, "base_url": "https://openrouter.ai/api/v1",
             "api_key_env": "MINO_OPENROUTER_KEY", "model": "openai/gpt-5.6-luna-pro",
             "reasoning_effort": "high", "provider_routing": ["OpenAI"],
             "small_model": "deepseek/deepseek-v4-flash-0731",
             "small_reasoning_effort": "max", "small_provider_routing": ["DeepInfra"]},
            {"name": "qwen-fallback", "priority": 2, "base_url": "https://openrouter.ai/api/v1",
             "api_key_env": "MINO_OPENROUTER_KEY", "model": "qwen/qwen3.7-flash"},
        ],
        "qwen": [
            {"name": "openrouter", "priority": 1, "base_url": "https://openrouter.ai/api/v1",
             "api_key_env": "MINO_OPENROUTER_KEY", "model": "qwen/qwen3.7-flash",
             "reasoning_effort": "high",
             "small_model": "deepseek/deepseek-v4-flash-0731",
             "small_reasoning_effort": "max", "small_provider_routing": ["DeepInfra"]},
        ],
    },
    "telegram_chat_id": "",
}

state = {"last_check": None, "prices": {}, "flags": {}}


# --- config / env -----------------------------------------------------------

def load_config():
    if os.path.exists(CONFIG_PATH):
        with open(CONFIG_PATH) as f:
            cfg = json.load(f)
        for k, v in DEFAULT_CONFIG.items():
            cfg.setdefault(k, v)
        return cfg
    return dict(DEFAULT_CONFIG)


def read_mino_env(key):
    if not os.path.exists(MINO_ENV):
        return ""
    with open(MINO_ENV) as f:
        for line in f:
            line = line.strip()
            if line.startswith(key + "="):
                return line.split("=", 1)[1]
    return ""


def send_telegram(cfg, message):
    token = read_mino_env("TELEGRAM_BOT_TOKEN")
    chat = cfg.get("telegram_chat_id") or read_mino_env("MINO_TELEGRAM_CHAT_ID")
    if not token or not chat:
        return "telegram not configured"
    data = json.dumps({"chat_id": chat, "text": message}).encode()
    req = urllib.request.Request(
        f"https://api.telegram.org/bot{token}/sendMessage",
        data=data, headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            return f"sent ({r.status})"
    except Exception as e:
        return f"send failed: {e}"


# --- scraper (proven in the proof-of-concept) ------------------------------

PRICING_RE = re.compile(
    r'\\?"pricing\\?":\{.*?\\?"prompt\\?":\\?"([0-9.]+)\\?".*?'
    r'\\?"completion\\?":\\?"([0-9.]+)\\?".*?'
    r'\\?"input_cache_read\\?":\\?"([0-9.]+)\\?".*?'
    r'\\?"discount\\?":([0-9.]+)')


def fetch(url):
    req = urllib.request.Request(url, headers={"User-Agent": UA})
    with urllib.request.urlopen(req, timeout=25) as r:
        return r.read().decode("utf-8", errors="ignore")


def parse_pricing(html):
    out = {}
    for m in PRICING_RE.finditer(html):
        prompt, comp, cache, disc = m.groups()
        back = html[max(0, m.start() - 2500):m.start()]
        names = re.findall(r'\\?"name\\?":\\?"([^\\"]{2,40}?)\\"', back)
        provider = names[-1] if names else "?"
        out[provider] = (float(prompt) * 1e6, float(comp) * 1e6, float(cache) * 1e6, float(disc))
    return out


def check_models(cfg):
    """Scrape every configured model; set flags when best price > expected*threshold."""
    prices, flags = {}, {}
    for name, m in cfg["models"].items():
        try:
            provs = parse_pricing(fetch(m["url"]))
        except Exception as e:
            flags[name] = f"SCRAPE FAILED: {e}"
            prices[name] = {"error": str(e)}
            continue
        if not provs:
            flags[name] = "no pricing parsed (page structure changed?)"
            prices[name] = {}
            continue
        best = min(provs.items(), key=lambda kv: kv[1][0])
        best_price = best[1][0]
        prices[name] = {"best_provider": best[0], "best_input": best_price,
                        "best_output": best[1][1], "best_cache": best[1][2],
                        "discount": best[1][3], "providers": len(provs)}
        limit = m["expected"] * m["threshold"]
        if best_price > limit:
            flags[name] = (f"PRICE SPIKE: best ${best_price:.4f}/M > "
                           f"expected ${m['expected']:.4f} × {m['threshold']} "
                           f"(promo likely expired)")
        else:
            flags[name] = "ok"
    state["last_check"] = time.strftime("%Y-%m-%d %H:%M:%S")
    state["prices"], state["flags"] = prices, flags
    return prices, flags


def swap_model(cfg, model):
    """Rewrite providers.json from the chain template + restart mino."""
    if model not in cfg["provider_templates"]:
        return f"Error: no provider template for {model!r} (chain: {', '.join(cfg['chain'])})"
    if not os.path.exists(PROVIDERS_JSON):
        return "Error: providers.json not found"
    backup = PROVIDERS_JSON + ".bak-cost-watch"
    with open(PROVIDERS_JSON) as f:
        original = f.read()
    with open(backup, "w") as f:
        f.write(original)
    with open(PROVIDERS_JSON, "w") as f:
        json.dump({"providers": cfg["provider_templates"][model]}, f, indent=2)
    # In-flight playbook guard: defer the restart (same rule as the self-updater).
    if os.path.isdir(RUN_LOCKS) and os.listdir(RUN_LOCKS):
        return (f"providers.json swapped to {model} (backup at {backup}); "
                f"restart deferred — playbook run in flight")
    try:
        subprocess.run(["systemctl", "restart", "mino"], check=True, timeout=60)
        return f"providers.json swapped to {model} (backup at {backup}); mino restarted"
    except Exception as e:
        return f"providers.json swapped to {model} (backup at {backup}); restart failed: {e}"


def status_text(cfg):
    lines = [f"last check: {state['last_check'] or 'never'}"]
    for name in cfg["models"]:
        p = state.get("prices", {}).get(name, {})
        f = state.get("flags", {}).get(name, "not checked")
        if p.get("best_provider"):
            lines.append(f"- {name}: best {p['best_provider']} "
                         f"${p['best_input']:.4f}/M in (${p['best_output']:.4f}/M out) "
                         f"[{f}]")
        else:
            lines.append(f"- {name}: {f}")
    return "\n".join(lines)


# --- HTTP (extension protocol) ----------------------------------------------

def tool_schemas():
    return [
        {"name": "cost_watch_status",
         "description": "Current best provider prices for the configured models, last check time, and promo-expiry flags.",
         "schema": {"type": "object", "properties": {}}},
        {"name": "cost_watch_check",
         "description": "Scrape the OpenRouter model pages NOW, refresh prices, and return them with any flags.",
         "schema": {"type": "object", "properties": {}}},
        {"name": "cost_watch_swap",
         "description": f"Swap providers.json to a chain model and restart mino. Chain: {', '.join(DEFAULT_CONFIG['chain'])}.",
         "schema": {"type": "object", "properties": {
             "model": {"type": "string",
                       "description": f"Target model: {', '.join(DEFAULT_CONFIG['chain'])}"}},
             "required": ["model"]}},
    ]


class Handler(BaseHTTPRequestHandler):
    cfg = None

    def _json(self, code, payload):
        body = json.dumps(payload).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/tools":
            self._json(200, tool_schemas())
        elif self.path == "/check":
            prices, flags = check_models(self.cfg)
            problems = [f"{n}: {f}" for n, f in flags.items() if f != "ok"]
            self._json(200, {"alert": bool(problems), "message": "; ".join(problems) or "all prices ok"})
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/execute":
            self._json(404, {"error": "not found"})
            return
        try:
            req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
        except Exception as e:
            self._json(400, {"error": f"bad request: {e}"})
            return
        tool = req.get("tool", "")
        args = req.get("args", {})
        if tool == "cost_watch_status":
            result = status_text(self.cfg)
        elif tool == "cost_watch_check":
            prices, flags = check_models(self.cfg)
            result = status_text(self.cfg) + "\n" + json.dumps(flags, indent=1)
        elif tool == "cost_watch_swap":
            model = args.get("model", "")
            result = swap_model(self.cfg, model)
        else:
            self._json(400, {"error": f"unknown tool {tool!r}"})
            return
        self._json(200, {"result": result})

    def log_message(self, *a):  # quiet
        pass


def main():
    cfg = load_config()
    Handler.cfg = cfg
    if len(sys.argv) > 1 and sys.argv[1] == "--check":
        # Autonomous hourly mode (systemd timer): scrape, alert on spikes.
        prices, flags = check_models(cfg)
        problems = [f"{n}: {f}" for n, f in flags.items() if f != "ok"]
        if problems:
            msg = "⚠️ mino cost-watch\n" + "\n".join(problems) + \
                  f"\nSwap: cost_watch_swap (chain: {', '.join(cfg['chain'])})"
            print(send_telegram(cfg, msg))
        else:
            print("all prices ok:", json.dumps({k: v.get("best_input") for k, v in prices.items()}))
        return 0
    server = ThreadingHTTPServer((cfg["listen"], cfg["port"]), Handler)
    print(f"cost-watch listening on {cfg['listen']}:{cfg['port']}")
    server.serve_forever()


if __name__ == "__main__":
    sys.exit(main())
