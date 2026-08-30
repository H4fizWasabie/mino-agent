# Release note: Progress-based iteration extension

## Changelog entry

Added to `CHANGELOG.md` under `[Unreleased]`:

> Turns can continue beyond the base `MINO_MAX_ITERATIONS` budget when tool
> calls and results keep changing, up to a hard 60-iteration ceiling. Repeated
> identical call/result pairs trigger a nudge at 3 repetitions and stop at 6
> if the model does not change course. Checkpoint replies distinguish a
> confirmed stall from a hard ceiling reached while progress was still
> detected.

## Config

No new configuration keys. `MINO_MAX_ITERATIONS` remains the base budget
(default `25`); the harness can extend useful progress up to 60 iterations.

## Documentation touched

- `README.md`: updated troubleshooting and power-tuning guidance.
- `docs/architecture-series.md`: updated the loop and guardrail descriptions.

## Migration

None. Existing configuration continues to work.

## Known limitations

- Progress identity is deliberately coarse and hashes prepared tool output, so
  changing metadata may count as progress and a semantically repeated result
  may not be recognized in every case.
- Live two-provider parity was not run because no provider adapter or external
  request path changed; the loop is exercised through its provider-neutral
  client interface.
