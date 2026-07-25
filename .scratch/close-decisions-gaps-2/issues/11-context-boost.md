# context_boost in scoreFact

Status: resolved
Type: grilling
Blocked by: —

## Question

Add a `contextBoost` signal to `scoreFact` — bumping facts whose subject overlaps with the active conversation turn. The formula changes from:

```
finalScore = 0.55 × similarity + 0.20 × importance + 0.15 × recency + 0.10 × feedback
```

to:

```
finalScore = 0.45 × similarity + 0.20 × importance + 0.15 × recency + 0.10 × feedback + 0.10 × contextBoost
```

## Answer

1. **factHit struct** (`adapters.go`): add `contextBoost float64` field.

2. **Conversation context threading:** Add a `recallCtx string` field to `Memory`. The `recall` tool Fn sets `mem.recallCtx = query` before calling `SemanticSearch` (the recall query is the LLM's distillation of the conversation turn — it's the best proxy available without plumbing the full user message through the tool layer). `hybridFactCandidates` reads `m.recallCtx` to compute `contextBoost` for each fact.

3. **contextBoost computation:** Keyword overlap between the recall context and each fact's `subject + " " + content`. Compute as `min(1.0, termOverlap / max(len(contextTerms), 1))` — a normalized 0..1 score. Uses the same `ftsTerms` tokenizer for consistency.

4. **scoreFact update:** New formula with the `contextBoost` term at 0.10 weight, `similarity` drops from 0.55 to 0.45.

5. **Existing test** `TestScoreFactUsesImportanceAndExplicitFeedback` gets updated to include `contextBoost` in the hit struct (default 0 for both low and high, so the relative ranking is unaffected).

File list: `adapters.go` (factHit struct, hybridFactCandidates, scoreFact), `tools.go` (recall tool Fn), `memory_test.go` (update test).
