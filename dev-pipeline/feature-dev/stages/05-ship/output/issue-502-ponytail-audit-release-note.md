# Ship note: remove dead ponytail-audit paths

## Changelog

Added the issue-502 entry to `CHANGELOG.md` under `[Unreleased]`.

## Release boundary

- Local commit is the next approved action.
- No tag, release build, publish, deployment, scheduler resume, or live-state
  change is authorized or performed.
- No migration, configuration change, dependency change, or user action is
  required for this internal simplification.

## Verification status

The fresh suite excluding the unrelated rollback timeout test passed with 843
tests passed and 1 skipped. The unfiltered suite still times out in the
pre-existing `TestVerifyNewBinaryTimeout`; this limitation is recorded in the
stage-04 verification report.

