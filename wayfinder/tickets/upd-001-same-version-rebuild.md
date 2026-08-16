# Updater — same-version rebuilds skip the update (stale binary deployed silently)

Status: **CLOSED** (GitHub issue #231) — fixed in `update.go` (same-version SHA comparison). Locked by `TestUpdateSameVersionSameSHASkips` / `TestUpdateSameVersionDifferentSHAProceeds`.

## Symptom

Live hit 2026-08-16, v2.11.0 release: a stale v2.11.0 build (pre-RUN-map, from an old tag pushed 08-15) was already on the VPS. `mino update` reported "Already up to date (v2.11.0)" and skipped the fresh build — the runtime-self-management map was NOT deployed until a manual atomic swap (SHA-verified, `.prev` kept, ledger line).

## Root cause

`isNewer` (update.go) compares only `parseSemver` numerics + prerelease suffix. Two builds with the same numeric version and no prerelease compare equal, regardless of content — no build metadata (`+build.NNNN`) and no SHA256 comparison.

## Fix

`DoUpdate` already fetches the release's `SHA256SUMS.txt`; at the same version it now also hashes the running binary and compares the two:

- Same version + same sha → "Already up to date" (positive identity match only).
- Same version + different sha → proceed through the existing download → release-checksum verify → `applyUpdate` path (RUN-004: swap + journal + health check + revert).
- Any inability to confirm identity (unreadable running binary, missing/unreachable checksum file) → proceed; a false "proceed" is safe because the download path re-verifies the release checksum and health-checks the candidate, while a false "skip" is the exact bug being fixed.
- An older release still never downgrades.
- Versioning policy unchanged — detection fix, not a process change.

## Acceptance criteria

- [x] `mino update` detects a same-version-but-different-build release and reinstalls through `applyUpdate`.
- [x] Identical build stays "Already up to date" with no download.
- [x] Tests: `TestUpdateSameVersionSameSHASkips` (connection-refused asset URL tripwire proves no download), `TestUpdateSameVersionDifferentSHAProceeds` (real built binary over httptest: swap, `.prev` kept, `binary.swap` journal op ok, ledger line, health check passed).
