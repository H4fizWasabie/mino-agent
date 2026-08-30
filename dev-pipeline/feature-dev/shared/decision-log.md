# Decision Log

Accepted architectural decisions and the explicit "do not build" list. Read by stage 01 to
catch ideas already settled, and appended to when a design run ends without implementation.

Each decision is short. If a decision needs pages of justification, the justification is a
design note in `stages/02-design/output/` and this entry points to it.

## Format

```
### [Short title]
Date: YYYY-MM-DD
Decision: [what was decided, one or two sentences]
Because: [the reason, one or two sentences]
Instead of: [the alternative that was rejected]
```

## Decisions

### ICM structures the process, not the product
Date: 2026-08-30
Decision: ICM organises how this pipeline runs. Mino's source code lives at the repository
root under normal Go conventions and never inside stage folders.
Because: A codebase is not a sequential pipeline with human handoffs between steps, so stage
numbering fights the work. Feature development is sequential and reviewable, so that is what
the pipeline models.
Instead of: Treating the repository itself as a set of numbered stages.

### Run this pipeline inside mino-oss, not a separate repository
Date: 2026-08-30
Decision: adopt the ICM feature-development pipeline directly inside mino-oss
(`dev-pipeline/feature-dev/`), routed to from the repository's existing `AGENTS.md` and
`CONTEXT.md`.
Because: the pipeline was first prototyped as a standalone project ("Hondo") evaluating
mino-oss as a base — but mino-oss already is the harness, so a separate repository would
only have vendored or forked it. Bringing the pipeline in-place skips that detour and keeps
one source of truth for the running code.
Instead of: standing up a separate repository that imports or forks mino-oss.

## Do Not Build

Ideas that have been considered and rejected. Stage 01 checks every new idea against this
list. An entry here is not permanent, but reopening one requires a reason that did not exist
when it was closed.

| Idea | Why not | Closed |
|------|---------|--------|
| Provider-specific feature flags | Breaks model agnosticism. Provider differences belong behind the adapter | 2026-08-30 |
| Embedding a scripting language or plugin runtime in the core binary | mino-oss already draws this line: a capability some owners want but not all belongs in an extension process (HTTP-based), not the core binary. Follow the same rule. | 2026-08-30 |
