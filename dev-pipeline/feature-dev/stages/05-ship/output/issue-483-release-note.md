# Release note: #483

## Changelog entry

Added to `CHANGELOG.md` under `[Unreleased]`: `tool_search`/`tool_call` on-demand tool
access (Added), usage-derived tool essentials (Changed), three new `MINO_TOOL_ESSENTIALS_*`
config keys (Config), and a Known limitations section carrying both open concerns from the
verification report.

## Config additions

| Key | Default | Absent behaviour |
|-----|---------|-------------------|
| `MINO_TOOL_ESSENTIALS_COUNT` | `8` | Default of 8 applies, no error. |
| `MINO_TOOL_ESSENTIALS_WINDOW_DAYS` | `30` | Default of 30 days applies; a newer install just has fewer rows in that window. |
| `MINO_TOOL_ESSENTIALS_REFRESH_HOURS` | `24` | Default of 24 hours applies; an initial synchronous refresh also runs at startup regardless. |

## Docs touched

- `CHANGELOG.md`: new `[Unreleased]` entry (Added/Changed/Config/Known limitations).
- `docs/soul.md` (stage 03): one line on `tool_search`/`tool_call`, loaded into every turn's
  system prompt unconditionally.
- No other checked-in docs (`docs/*.md`, `wayfinder/*.md`) referenced the old
  `essentialToolNames`/sliding-window mechanism by name — nothing else contradicts the
  shipped behaviour.

## Migration notes

None. No documented public interface renamed or removed in a way a user configured against —
the changed surface (`Registry.SchemasForContext`'s internal selection logic) was never a
public/documented interface itself, only its externally-observable effect (which tools a
turn can use) changes, and that effect is additive (nothing becomes unreachable, some things
now cost one extra round trip).

## Known limitations (carried from verification)

1. `MINO_TOOL_ESSENTIALS_*` defaults are un-tuned against real production usage — shipped
   without a shadow-run period by owner instruction. Carried into CHANGELOG's Known
   limitations.
2. Deferred-tool round trip tax repeats every time a tool is used (no session-sticky
   "already searched" state) — an accepted tradeoff from the redesign that avoided breaking
   the provider prompt-cache. Carried into CHANGELOG's Known limitations.
3. `dev-pipeline/feature-dev/shared/decision-log.md` not updated with this decision — the
   pipeline's shared "Do Not Build"/accepted-decisions log doesn't name an explicit owner for
   post-ship updates; noting here in case the owner wants it added manually.

## Status

Verification report shows a full pass (`dev-pipeline/feature-dev/stages/04-verify/output/issue-483-verification.md`).
Changelog and doc edits are applied to the working tree, not yet committed. This stage does
not commit, open a PR, tag, or deploy — that's the separately-governed production path in
AGENTS.md, which starts with explicit owner approval at each boundary (already granted in
this session for merge/release-lane/deploy, per-boundary confirmation still due at each step
per AGENTS.md's own rule).
