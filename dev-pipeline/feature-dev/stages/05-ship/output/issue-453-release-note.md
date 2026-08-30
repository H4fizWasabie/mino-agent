# Ship note: #453

Playbook navigation (chat-triggered or scheduled, #450–#452) now carries two small
token-discipline additions:

- Every stage's rendered contract says plainly to read only its declared inputs unless
  genuinely blocked, instead of leaving that unstated.
- `read_file` remembers, per navigation run, which paths it has already served and their
  mtime at the time. Reading an unchanged path again gets a short prefix note
  ("unchanged since you read it at HH:MM:SS this run") instead of silence — the model can
  choose not to re-process content it already has. The full file content is always still
  returned; this is a nudge, never a withholding, so no verification-discipline rule
  (verify-then-claim, CTX-003, CTX-016) is put at risk.

Zero-inference script stages needed no change here — #450/#451/#452 already drive them
straight through with no model call.

The nudge and the tracker behind it are scoped entirely to an active playbook navigation
(`sessionNav`); `read_file` behaves exactly as before for every other call.

## Config additions

None.

## Docs touched

- `docs/playbooks-design.md`: one sentence on the read-only-declared-inputs guidance and the
  unchanged-read nudge.
- `CHANGELOG.md`: entry above.

## Migration notes

None. No interface, tool schema, or config surface changed.

## Known limitations

1. The nudge is scoped to exact path identity — no relative/absolute-path or symlink
   normalization — matching every other path-keyed mechanism already in the codebase
   (`playbookWriteGuard`, `knownArtifactsKey`).
2. No production metric proves the nudge's actual token-savings impact; that needs live usage
   data from real playbook runs, not something a locally-testable harness change can verify on
   its own. Full local test suite, `go vet`, and `go build` all pass (see the verification
   report), and the nudge's boundary conditions (unchanged, genuinely changed, no active
   navigation, tracker cleared on run end) are each covered by a forced test.

Release/tag/deployment intentionally not performed, per the owner's instruction not to release
until the remaining approved Wayfinder issues are merged.
