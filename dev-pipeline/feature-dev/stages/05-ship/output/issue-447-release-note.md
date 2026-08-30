# Release note: after-the-fact playbook deviation detection

## Changelog entry

Added under `[Unreleased]`: LLM playbook stage attempts now flag undeclared
tools, undeclared `write_file` targets, and deterministic contract-verification
failures in the trace, audit log, and owner outbox. The run remains unblocked;
there is no LLM prose judge.

## Config and migration

No new configuration keys. No migration required.

## Documentation

Updated `README.md` and `docs/architecture-series.md`.

## Known limitation

The v1 scope checker does not parse opaque shell commands for arbitrary paths.
Future grouped checkpoint work (#449/#450/#451) can extend the shared contract
boundary without changing this reporting sink.
