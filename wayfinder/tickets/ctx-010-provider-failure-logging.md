# Context Truth — Log provider failure reasons in the provider manager

Status: **RESOLVED** (closes GitHub issue #154, commit pending)

## Question

Why did the main model silently fail over to qwen at 04:33, and how do we make the next failover diagnosable?

## Evidence (2026-08-11)

- 04:33:29-40: four qwen/qwen3.7-flash calls handled the send-file turn's iterations; the final reply (in Malay) was qwen's.
- Mechanism: the main (deepseek via OpenRouter, unpinned at the time) failed 3x within ~60s → circuit breaker (provider_manager.go failure(): 3 failures → 60s openUntil) → candidates() skipped it → qwen-fallback provider served the rest.
- The failure REASON was logged nowhere: failure() only increments a counter. Root cause was guessed post-hoc (reasoning_effort=max rejected by an unpinned provider) and confirmed only by fixing the config (pinning :deepinfra stopped the failures).

## Design sketch

- In the provider manager's failure path (callWithConfig → failure()): slog.Warn with provider name, role, model, and the error string.
- Log circuit-breaker trips with the aggregated error.
- No behavior change — fallback semantics untouched.

## Acceptance criteria

- [ ] Every provider failure logs name/role/model/error
- [ ] Circuit-breaker trips are logged with the aggregated error
- [ ] No change to fallback behavior


## Resolution

Every failed provider call now logs `slog.Warn("provider call failed", provider, role, model, attempt, error)` in both `callWithConfig` and `callContextWithConfig`, and the circuit-breaker trip logs `"provider circuit opened"` with the provider, role, session, and cooldown. The 2026-08-11 failover would now show the exact error string in the journal.

## Validation

- `go test ./...` — 507 pass
