# Playbooks -- workspace run artifacts

Status: **OPEN** (child of PB-001)

## Destination

Keep daily and repeated runs isolated while preserving ICM's editable,
filesystem-based stage handoffs.

## Scope

- Define the user-visible run workspace and its stage output paths.
- Keep each run's handoffs isolated from other runs.
- Keep run status, traces, audits, retries, and distillation metadata in
  Mino-managed runtime state.
- Ensure later stages read only the active run's declared outputs.
- Define retention/archive behavior before enabling high-volume runs.

## Acceptance criteria

- [ ] Two runs cannot read or overwrite each other's declared handoff outputs.
- [ ] A user can inspect and edit the active run's output files.
- [ ] A resumed run uses its own prior outputs and state.
- [ ] Historical outputs are not loaded merely because they exist.
- [ ] Existing output attribution and memory-distillation behavior is preserved.

