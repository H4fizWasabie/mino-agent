# Provider/model coupling — transport branches, reasoning extraction, policy churn

Status: **OPEN** (wayfinder ticket, PRV-001 — GitHub issue #190)

## Question

Every provider or model change costs changelog entries + regression tests because the code carries provider-family branches instead of one generic seam. How do we name the seams so a model swap is a config edit, not a code change?

## Evidence (code, 2026-08-14)

| Seam | Location | Cost today |
|---|---|---|
| Codex transport family | `codex.go` `isCodex()`; `createWithRouting` branches to `createCodex` (provider.go:94) | Every new provider family duplicates the branch pattern |
| Hardcoded model lists | `codexModels` (provider_manager.go:48, gpt-5.6-*); `normalizeProvider` special-cases `name == "codex"` (53-58, 144) | A model list lives in code, not config — the policy file can't express it |
| Dashboard codex strings | dashboard.go:1479/1531/1538 (`"codex"` provider literals) | UI couples to provider names |
| Codex OAuth refresh | provider_manager.go:201 (`p.Name == "codex"` special case) | Same special-case in a second file |
| Reasoning extraction | `reasoning_content` (DeepSeek) vs `reasoning` (qwen via OpenRouter) — #163 | Partially generic already (fallback to whichever is present); the residual is provider-specific fields in the response parser |
| Policy churn history | v2.8.5 main-model swap (CTX-008: docs lagged the swap), #159 dead-`:provider`-pin retry, #107 GLM removal, EMB-001 ("semantic layer re-perturbed by every model swap") | Each swap is an incident-shaped event instead of a config edit |

## Direction (not a rewrite — a seam inventory)

1. **Transport family adapter**: one internal seam for "OpenAI-compatible" vs "Codex" (client creation + response shape), selected from config (`transport:` field on ProviderConfig), not from a `name ==` string check.
2. **Model lists to config**: `codexModels`/`reasoningLevels` move to the policy file (or derive from the provider's own metadata like cost-watch already does for prices).
3. **Reasoning extraction**: keep the fallback-whichever-present parser (#163); the response parser should carry a per-transport field map, not if/else on model names.
4. **Dashboard**: provider rendering reads config (`provider_routing`-style), not `"codex"` literals.

## Acceptance criteria

- [ ] Adding a new provider family requires zero `name ==` branches in provider_manager/dashboard — one config field + one transport implementation.
- [ ] A model list change (e.g. new codex model) is a config/policy edit; no Go change.
- [ ] No regression: 549-test suite stays green; the VPS policy (deepseek flash main, qwen fallback) is expressible in the same shape.

## Out of scope

- Rewriting the provider stack (it works; the goal is fewer special cases, not new abstractions)
- Multi-owner / multi-tenant anything
- Changing the routing or circuit-breaker behavior (#159 stays as-is)
