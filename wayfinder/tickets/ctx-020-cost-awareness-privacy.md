# Harness — Cost awareness & privacy: cost-watch feeds the brain, model-agnostic

Status: **OPEN** (wayfinder ticket, CTX-020)

## Framing (harness, not LLM)

Mino is blind to its own spend today — `usage.jsonl` is the source of truth and `cost.go` already computes `runCostUSD`/`monthSpendUSD`, but the data only pages the *owner* via outbox alerts ($2/run, $25/month). The LLM never sees cost. Same fix as every harness gap this session: **the harness feeds the data to the brain; the brain proposes; the owner decides.**

Two cost axes: **(1) own spend** (`usage.jsonl`, provider-reported `cost_usd` — the truth) and **(2) the market** (a fetched price catalogue — approximate reference). Never confuse them: scraped = candidate, billed = truth.

## Mental model (the self-tuning loop)

```
Mino tunes the watcher's CONFIG (hot-reloaded file — not the watcher's code)
  → watcher fetches OpenRouter endpoints for whatever model is configured
  → persists the price catalogue (cost-catalogue.json)
  → system_check surfaces [catalogue] + [real spend from usage.jsonl]
  → Mino sees its own cost and adjusts (allowed providers, effort, routing)
```

The watcher stays a generic engine; Mino adjusts its *settings*, not its code — self-maintenance with no extra agents and no redeploys. Two feeds: the watcher feeds the *market*, the harness feeds the *truth* (`usage.jsonl`), and both reach the brain through `system_check` (+ per-run line + daily observation).

## Config vs code

- **Config-only (no Go changes):** the provider swap, main/fallback, allowed-provider set, and `reasoning_effort` — all in `providers.json`, running on existing code (`NewProviderManager` priority sort + `provider_routing`).
- **Code changes:** (A) the cost-watch watcher — endpoints-API fetch, catalogue persistence, hot-reload; (B) the harness feeders — `system_check` cost block, per-run cost line, daily cost observation, `policyPrices` → config.

## Scope

### 1. Privacy swap (config-only)
- `providers.json`: OpenRouter entry becomes priority 1; the direct DeepSeek API entry is **dropped entirely** (a failover that silently sends data to a training provider defeats the privacy decision). qwen-fallback stays.
- **Default routing (no hardcoded provider):** unpin DeepInfra — use the allowed-provider set, completion-price-sorted (Mino's usage is output-heavy):
  1. **DeepInfra** (main — proven in production today, ZDR, $0.18/M completion)
  2. OpenInference ($0.18/M) — ZDR status must be verified before real traffic
  3. Decart ($0.162/M, cheapest completion) — smaller host, verify before trusting
  4. Together → 5. Fireworks → 6. Cloudflare ($0.28/M, reliable no-training hosts)
  - **Excluded (default `trains`/`unknown`):** DeepSeek's own endpoint + Chinese hosts (Baidu, SiliconFlow, StreamLake, GMICloud).
  - The set is **dynamic**: Mino maintains it from the catalogue (add cheap ZDR hosts, demote sick/pricey ones). 6 is the working default, not a fixed number.
- `NewProviderManager` already sorts by priority — no code change; the gotcha is it fails hard when a configured provider's key is missing, so removal must keep `MINO_OPENROUTER_KEY` set and drop the dead `MINO_DEEPSEEK_KEY`.

### 2. `system_check` cost block
- Extend `system_check` (the existing "dynamic state" tool) with: month spend, today's spend, unpriced count, recent per-run costs (reusing `monthSpendUSD`/`runCostUSD`), plus a catalogue snapshot (see #5).

### 3. Per-run cost in `run_playbook` results
- `formatPlaybookResult` gains a cost line (from `runCostUSD` for the run's session) so the LLM sees what each run cost immediately — alongside the failure evidence already injected there.

### 4. Daily cost observation
- Piggyback the existing daily `checkMonthlyCostOnce`: besides the $25 owner page, write an LLM-visible `cost_state` trace + a session-note line ("today: $X, month: $Y / $25 — be mindful"). Daily, threshold-driven, not per-turn (no token bloat — same adaptive pattern as the audit gate).

### 5. Cost catalogue — model-agnostic and self-maintainable (NOT hardcoded)
- **Targets derived from the user's `providers.json`** — whatever models a user configured (deepseek-v4-flash via OpenRouter today; GPT Luna or Gemini for others).
- **Source = the OpenRouter endpoints API** (`/api/v1/models/:slug/endpoints`) — it returns every hosting provider + live prices for any model (verified today: 27 providers host deepseek-v4-flash-0731). No HTML scraping for OpenRouter models; scraping is only a fallback for non-OpenRouter providers.
- **cost-watch becomes a generic engine**: it reads an editable config (models to watch, provider data-handling flags, thresholds), fetches the endpoints for those targets, persists `cost-catalogue.json` (provider, model, in/out price, `zdr|trains|unknown` flag, `scraped_at`), and **hot-reloads the config** so Mino can edit its own watchdog file — self-adjustment with no extra agents and no redeploys.
- **ZDR flags are curated, not scraped**: the endpoints API gives prices but not data-handling — Mino (or the owner) marks `zdr` from provider docs; unverified hosts default to `unknown` and rank below verified ones (verify-then-claim applies to the catalogue itself).
- **Exposed via `system_check`** alongside real spend.
- **Fallback pricing moves out of the hardcoded `policyPrices` map** in `cost.go` into config (current values become the default seed) — a GPT-Luna user's fallback must be their prices, not ours.

### 6. Guardrails (non-negotiable)
- **Privacy is a hard constraint, cost a soft preference**: never route to a `trains` provider regardless of price.
- **Catalogue is reference, not decision-maker**: cheapest ≠ appropriate; Mino compares and proposes, harness policy + owner decide.
- **Scraped prices are untrusted estimates**: flagged as approximate, with `scraped_at`; never override `usage.jsonl`'s `cost_usd`.

## Acceptance criteria

- [ ] `providers.json` on the VPS: OpenRouter main (allowed set of 6, no provider pin), direct DeepSeek dropped; runs work without `MINO_DEEPSEEK_KEY`.
- [ ] `system_check` reports month/today/per-run spend + unpriced count + catalogue snapshot.
- [ ] `run_playbook` results carry the run's cost.
- [ ] Daily cost observation lands in the trace + session notes without paging the owner below thresholds.
- [ ] `cost-catalogue.json` is produced from the OpenRouter endpoints API for config-derived targets (no hardcoded model list), hot-reloads on config edit, and carries the `zdr|trains|unknown` flag per provider (unverified = `unknown`, ranked below verified).
- [ ] Fallback pricing is config-driven (hardcoded `policyPrices` removed or reduced to a default seed).
- [ ] No routing path sends data to a `trains` provider.
- [ ] Tests for the new cost surface + catalogue fallback logic.