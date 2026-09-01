# Intake: preserve prompt-cache locality across cost-watch routing (#509)

## Problem

Mino sessions can move between OpenRouter upstream providers because cost-watch
rewrites a ranked multi-provider order and Mino passes that manual order through
unchanged. Each upstream keeps a separate prompt cache, so a stable Mino
conversation can repeatedly pay uncached input cost or lose cache locality.

## Rejection check

This does not match the decision log's rejected provider-specific feature flags:
provider differences remain behind the existing provider adapter. It also does
not introduce response caching or a second agent loop.

## Who is affected

Long-running Telegram sessions and scheduled playbooks with large prompts are
affected. The live VPS shows cache rates varying sharply by model and session,
while the OpenRouter log shows the same model moving among upstream providers.

## Smallest change

Keep cost-watch's ranked fallback list, but let Mino remember the upstream that
successfully served each session and put it first while it remains eligible.
Forget that preference on request failure or malformed output, and record the
actual upstream for measurement.

## Growth risk and scope line

This could grow into semantic quality scoring, response caching, or a new
provider-selection service. This task stops at prompt-cache locality,
structural failure failover, and observability; arbitrary valid-but-poor prose
is not automatically classified as garbled.

## Acceptance criteria

1. A session with a ranked upstream list keeps a successful eligible upstream
   first on later requests.
2. A changed cost-watch list preserves the preference only while that upstream
   remains eligible; otherwise the best current candidate is used.
3. A timeout, transport error, parse error, or malformed tool-call response
   clears the session preference before retry/failover.
4. A successful fallback becomes the new session preference.
5. OpenRouter requests expose the actual selected upstream when router metadata
   is available, and usage records retain that value without breaking providers
   that do not return it.
6. No complete autonomous response is cached or replayed.
7. Existing cost-watch ranking, privacy exclusions, provider retries, and
   non-OpenRouter behaviour remain unchanged.
8. Core tests, race tests, vet, build, and platform builds pass.

## Surfaces touched

- `provider_manager.go`: session-scoped upstream preference and invalidation.
- `provider.go`: provider-adapter metadata capture and ordered upstream input.
- `db.go` and usage/dashboard readers: optional upstream attribution.
- Provider and manager tests: routing, failure, reload, and observability cases.
- `CHANGELOG.md` and feature-pipeline outputs.

## Out of scope

- Changes to cost-watch's price, privacy, precision, or uptime ranking.
- Semantic judging of ordinary text responses.
- OpenRouter response caching (`X-OpenRouter-Cache`).
- VPS configuration, release, or deployment.
