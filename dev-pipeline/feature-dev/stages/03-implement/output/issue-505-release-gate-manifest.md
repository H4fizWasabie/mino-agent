# Implementation manifest: release gate ordering (#505)

## Files changed

- `scripts/release.sh` — require the tagged commit to equal `origin/master`,
  remove pre-gate pushes, and push the tag only after `stage-smoke` passes.
- `CHANGELOG.md` — record the release-lane safety fix.

## Verification

- `bash -n scripts/release.sh` — passed.
- `git diff --check` — passed.
- Release execution was intentionally not run; it performs remote staging and
  publication and requires the separate release approval boundary.

## Deferred

- No release, tag push, VPS staging, publication, or deployment was performed.
