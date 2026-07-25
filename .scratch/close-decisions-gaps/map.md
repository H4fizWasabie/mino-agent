# Wayfinder Map: Close remaining DECISIONS.md gaps

## Destination

Implement the five DECISIONS.md sections that are not yet true in code: §8 (four-layer safety), §18 (observability), §17 (eval CLI), §16 (onboarding flow), and §15 (fan-out). In that priority order.

## Notes

- Go stdlib only. Flat package structure. All new code in root `*.go` files unless unwieldy.
- Existing patterns to follow: `request_approval` / `resolve_approval` tool flow, `slog` logging, env-var config.
- Safety guards (§8) are the foundation — nothing else lands before they do.
- DECISIONS.md must stay current: update relevant entries as each ticket closes.

## Decisions so far

- [Workspace boundary check](issues/01-workspace-boundary.md) — boundary = Workspace + ~/.mino, gated on write_file/edit_file/sync_file only (not bash), per-session approval cache, auto-create approval UX
- [SQL gate](issues/02-sql-gate.md) — expand `dangerousBashReason` with DELETE/UPDATE without WHERE regex, no config toggle, gate whole multi-statement command
- [Git auto-commit before bash](issues/03-git-auto-commit.md) — `git add -A && git commit` inside bash.Fn before exec, skip silently if no repo, no new rollback tool (use `git reset --hard HEAD~1`)
- [Immutable audit log](issues/04-audit-log.md) — new `~/.mino/audit.jsonl` alongside SQLite tool_calls, JSONL with approval decision, 0600 append-only, never rotate
- [Alerting and dead man's switch](issues/05-alerting.md) — check tool_calls every 5min for >10% error rate or 6hr silence, Telegram delivery, 1hr cooldown, stuck-loop detection only (process-dead = systemd)
- [Health + metrics endpoints](issues/06-health-metrics.md) — `/health` returns version+uptime, `/metrics` Prometheus format with tool/LLM/session counters, same port 7779, no auth
- [Cost per session](issues/07-cost-per-session.md) — add session_id to usage.jsonl, token breakdown only (no $ estimate), dashboard panel on stats page
- [mino eval CLI](issues/08-eval-cli.md) — `eval/cases.json` with {name, prompt, expected_tool, must_not_loop, must_complete_in_n}, real LLM runner, behavior-only judgment, exit 0/1, overwrites eval_report.json
- [Onboarding flow](issues/09-onboarding-flow.md) — provider button grid (ChatGPT/Claude/manual), auto-open browser via xdg-open, Telegram paste-field post-setup, first-prompt affordance, manual form as advanced toggle
- [fan_out tool](issues/10-fan-out-tool.md) — {prompts, context} API, WaitGroup concurrency, partial results on failure, streaming deferred to revisit, core tool always available

## Not yet specified

- Error trending dashboard panel — depends on how metrics are stored (ticket 06)
- Eval benchmarking mode (sample N random cases, full suite before release) — depends on ticket 08
- Extension latency transport (Unix sockets) — unrelated, already in §3 revisit clause

## Out of scope

- Per-user cost attribution (§18) — multi-tenancy is §9 explicitly out of scope
- Fine-tuning pipeline (§9) — explicitly out of scope
- Plugin marketplace (§9) — explicitly out of scope
