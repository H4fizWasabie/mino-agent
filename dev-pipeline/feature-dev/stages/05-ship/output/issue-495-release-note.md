# Release note: #495

## Changelog entry

Added to `CHANGELOG.md` under `[Unreleased]`: `repetition_penalty` on every OpenRouter request
(Added), cost-watch precision-first ranking (Changed).

## Config additions

| Key | Default | Absent behaviour |
|-----|---------|-------------------|
| `MINO_REPETITION_PENALTY` | `1.1` | Default applies, no error. Invalid (non-numeric) also falls back to default (existing `envFloat` contract). |

## Docs touched

- `CHANGELOG.md`: new `[Unreleased]` entries.
- `extensions/cost-watch/README.md`: ranking-flow diagram and eligibility prose updated to
  mention the precision floor ahead of price.
- `extensions/cost-watch/SKILL.md`: the "pinning is autonomous" summary line updated the same
  way, so a session reporting cost-watch status describes the real ranking order.

## Migration notes

None. No documented interface renamed or removed. `providers.json`'s shape is unchanged — the
new ranking factor is internal to cost-watch's own decision, not a new field the owner
configures. cost-watch's next catalogue refresh (hourly, or `cost_watch_refresh` on demand)
applies the new ranking automatically; no manual action needed to activate it once deployed.

## Known limitations (carried from verification)

1. Whether every routed backend silently drops an unsupported `repetition_penalty` (vs.
   rejecting the request) is an assumption from OpenRouter's documented behavior, not proven
   by a unit test — worth watching post-deploy.
2. The new ranking doesn't force an immediate re-pin; `providers.json`'s current GLM routing
   order won't reflect it until the next scheduled or manually-triggered catalogue refresh.

## Status

Verification report shows a full pass
(`dev-pipeline/feature-dev/stages/04-verify/output/issue-495-verification.md`). Changelog and
doc edits applied to the working tree. This stage does not commit, open a PR, tag, or deploy —
those follow next per the owner's already-granted approval for this specific pipeline run.
