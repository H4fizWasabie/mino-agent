# Outcome Verification — The Harness Must Check the Job, Not the Narrative

Type: `wayfinder:grilling` (HITL — owner signs off on failure semantics)

Blocked by: nothing. Blocks: Health Alert.

## Question

What must the harness verify before a stage/run counts as complete, so a model's self-report ("NOT FULFILLED", "could not start") can never mask a missed job?

## Context

- Today a run marked `complete` in state.json while its own battle log said "Skipped" and its summary said "Stage incomplete". The harness trusts outputs-written, not outcomes-achieved.
- The loop already pushes for missing declared outputs. It does not know that "the post was published" is the point — a log file saying "not posted" satisfies the output contract.
- Playbooks have outcome markers: `threads_post` returns a post ID; `REDDIT_POST_REDDIT_COMMENT` returns a comment ID; battle logs carry Status fields.

## Ask

- Which outcome markers does the harness recognize (post ID in tool results? status fields in logs?)?
- Is "declared output exists but states failure" a hard failure class — and if so, where does it surface (run status, alert, both)?
- Does this belong in the harness (generic: parse output for failure markers) or per-playbook (declared success condition in the contract)?
