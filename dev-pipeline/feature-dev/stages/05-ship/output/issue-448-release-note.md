# Release note: Mid-flight self-repair

## Changelog entry

Added to `CHANGELOG.md` under `[Unreleased]`: after two identical tool
call/result outcomes, Mino tells the model to choose a genuinely different next
action or state its blocker. The existing progress nudge and hard stop remain
in force.

## Config and migration

No new configuration keys. No migration required.

## Documentation

Updated `README.md` and `docs/architecture-series.md`.

## Known limitations

The trigger is fixed at two outcomes, and progress identity remains the coarse
#443 hash of prepared tool output.
