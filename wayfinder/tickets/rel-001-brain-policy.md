# Brain Policy — Which Model Does What, At What Cost

Type: `wayfinder:grilling` (HITL — the owner decides the cost/capability tradeoff)

## Question

What is Mino's model policy? The current brain (gpt luna) is "too agentic by itself and expensive" — it refuses work it shouldn't (skipped a scheduled post, claimed tools unavailable), and costs more than hy3/deepseek. Decision needed: which model for the main loop, which for playbook stages, which for vision, and what the cost ceiling is.

## Context

- Observed today: gpt luna is the stage brain that skipped the tribal post ("inputs unavailable") and wrote "could not start because run_playbook was unavailable" — both false/avoidable claims. hy3 (previous brain) coped on the same prompts.
- Price data (from the models API): hy3 $0.132 in / $0.528 out; deepseek-v4-flash-0731 pinned $0.08 in / $0.18 out (cache $0.016), official :deepseek cache $0.003; gpt luna — to be measured.
- Mino usage is input-heavy and cache-heavy (~37% cache reads): effective cost favors pinned providers with cheap cache.
- Provider jumping is a known failure mode (25 providers on the deepseek ID, 3.5x price spread) — pinning suffixes (`:deepseek`, `:tencent`) already used.

## Ask

- Which brain for the main loop? For playbook stages? (One model or role-split?)
- What is the monthly/run cost ceiling that triggers a review?
- Does the "too agentic" behavior change the model choice, or do harness guards (outcome verification) make any model safe enough?
