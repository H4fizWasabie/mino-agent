# Wayfinder Map: Implement DECISIONS.md §§19-22

## Destination

Implement the four remaining DECISIONS.md sections that are specified but not yet in code: §19 (context-aware memory ranking), §20 (structured task plans), §21 (extension quality feedback), §22 (eval case auto-generation). In that priority order.

## Notes

- Go stdlib only. Flat package structure. No new files unless unwieldy.
- 168 tests must stay green throughout.
- Existing patterns: `scoreFact` in adapters.go, `TaskSnapshot` in checkpoint.go, `CheckExtensions` in extensions.go, `EvalCase` in eval.go.
- DECISIONS.md stays as-is (already documents the target state).

## Tickets

1. [context_boost in scoreFact](issues/11-context-boost.md) — add `contextBoost` field to `factHit`, compute from conversation context overlap, adjust scoring formula weights
2. [task plan struct in checkpoint](issues/12-task-plan-struct.md) — add `Plan []TaskStep` to `TaskSnapshot`, LLM declares plan, harness persists + feeds back
3. [extension retry + similarity alert](issues/13-extension-retry-similarity.md) — detect ≥3 same-tool calls in 5min + >90% output similarity, trigger alert
4. [eval auto-generation](issues/14-eval-auto-gen.md) — thumbs-up dashboard button + auto-harvest from complete_task, two-tier confidence

## Out of scope

- DAG-based task graph with dependency enforcement (revisit clause §20)
- Per-extension similarity threshold tuning (revisit clause §21)
- Scheduled user review for eval cases (revisit clause §22)
- Any new external dependencies
