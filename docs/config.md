# Mino Configuration Reference

Everything Mino reads at startup or runtime, where it lives, and who may write
it. The rule: **user-managed config and generated state are separate by
design** — files have different writers and lifetimes (auth credentials churn
on OAuth, schedules churn every run, usage/catalogue are generated output).
A unified `config.json` was explicitly rejected (2026-08-14): merging them
would need a migration + compat layer, the exact dead-code class the ponytail
audit keeps deleting.

## The files

| File | Kind | Owner | Written by | Read by |
|------|------|-------|-----------|---------|
| `~/.mino/providers.json` | config | mino | manual, dashboard OAuth login (`EnsureProvider`) | provider manager, cost-watch (monitored set) |
| `~/.mino/mino.env` | config (secrets) | mino | dashboard settings form, manual | process env via `loadEnvFile` |
| `~/.mino/auth.json` | config (credentials, 0600) | mino | OAuth logins, `AuthStore` | provider manager (key/refresh resolution) |
| `~/.mino/schedules.json` | runtime state | mino | scheduler (every run) | scheduler, briefing, `system_check` |
| `~/.mino/extensions.json` | config | mino | manual | extension loader at boot |
| `~/.mino/cost-watch.json` | config | mino | manual / **the model (CTX-020 hot-reload)** | cost-watch every refresh cycle |
| `/etc/mino-cost-watch.json` | config (legacy fallback) | root | manual | cost-watch only if the home file is missing |
| `~/.mino/cost-catalogue.json` | generated | root (cost-watch) | cost-watch hourly loop | `system_check`, dashboard |
| `~/.mino/cost-trigger.json` | generated | mino | cost-watch `--check` | promo paging |
| `~/.mino/skills_usage.json` | generated | mino | skill loader | skill matching |
| `~/.mino/threads_token.json` | generated | mino | threads extension | threads extension |

`~/.mino` = `MINO_HOME` (the service runs as the `mino` user with
`MINO_HOME=/home/mino/.mino`).

## One provider = three files (the mapping)

A single provider entry spans three files by design — route in
`providers.json`, key in `mino.env`, credential in `auth.json`:

| Piece | File | Example |
|-------|------|---------|
| Route (base_url, model, routing, transport) | `providers.json` | `{"name":"openrouter","model":"deepseek/...:deepinfra","provider_routing":["DeepInfra"],"transport":"openai"}` |
| Key source | `providers.json` → `mino.env` | `"api_key_env": "MINO_OPENROUTER_KEY"` → `MINO_OPENROUTER_KEY=...` |
| Credential | `auth.json` | `{"openrouter": {"type": "api_key", "key": "..."}}` or `oauth` entries with refresh/expiry |

`auth.json` is 0600 and holds the actual secrets; `mino.env` holds key
*names* plus non-provider secrets (Telegram); `providers.json` never holds
secrets.

## The split-brain hazard

Mino's home is `MINO_HOME` — **always** `/home/mino/.mino` on the VPS. Running
the binary as another user (e.g. `sudo mino ...` as root) creates a *second*
state tree at `/root/.mino` with its own config, its own deployments.log, and
its own brain — a silent split-brain (witnessed 2026-08-14: a root-run
instance created `/root/.mino` during a port test). Rules:

- `mino update`: `sudo HOME=/home/mino /usr/local/bin/mino update`
- anything else: run as the `mino` user, or set `MINO_HOME=/home/mino/.mino` explicitly
- the dashboard port test: `MINO_HOME=/tmp/... MINO_DASHBOARD_PORT=7780` (see `scripts/stage-smoke.sh`)

## Why not one config file

`auth.json` churns on every OAuth login (and must stay 0600); `schedules.json`
churns every run; `skills_usage.json`/`cost-catalogue.json`/`cost-trigger.json`
are generated output. A unified file would interleave credentials, runtime
state, and generated data under one writer — the 2026-08-14 audit's finding
stands: the sprawl is a *discovery* problem (this document) plus one
*ownership* problem (cost-watch config, now fixed), not an architecture
problem.
