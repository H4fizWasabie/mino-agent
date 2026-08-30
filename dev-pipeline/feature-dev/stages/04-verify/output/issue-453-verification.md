# Verification: #453

Branch: `fix/issue-453-token-discipline`

## Test results

- Full `GOCACHE=/tmp/mino-gocache go test ./... -count=1 -timeout=300s`: PASS (two runs,
  275.6s and 271.7s).
- `go vet ./...`: PASS.
- `go build ./...`: PASS.
- `git diff --check`: PASS.
- New tests (4): PASS — nudge on unchanged re-read during navigation, no nudge outside
  navigation, no nudge on a genuinely changed file, read-tracker cleared with the session's
  navigation pointer.

## Acceptance criteria (from intake) — observed behaviour

1. **Nudge on unchanged re-read within a navigation run, full content still returned.**
   Observed in `TestReadFileNudgesOnUnchangedRereadDuringNavigation`: first read has no
   nudge; second read of the same untouched path carries "unchanged since" AND still returns
   the literal file content ("hello") both times.
2. **No nudge on a genuinely changed file.** Observed in `TestReadFileNoNudgeWhenFileChanged`:
   content and mtime both change between reads (mtime forced forward with `os.Chtimes` to
   rule out filesystem timestamp-resolution false positives); the second read carries no
   nudge and returns the updated content.
3. **No nudge outside an active navigation.** Observed in `TestReadFileNoNudgeOutsideNavigation`:
   two reads of an untouched file with no `sessionNav` pointer set never carry the nudge —
   proves zero behavior change for ordinary (non-playbook) tool use.
4. **Read tracker cleared with the navigation pointer.** Observed in
   `TestClearSessionNavClearsReadTracker`: after `clearSessionNav` and a fresh `setSessionNav`
   for the same run ID, the next read is treated as unseen — proves a finished run's tracker
   doesn't leak into whatever reuses that run ID.
5. **Build and full suite pass; no invariant broken.** See above.

## Invariants — held / evidence

| Invariant | Verdict | Evidence |
|---|---|---|
| Model agnosticism | Held | No provider-specific code. |
| Loop termination | Held (unaffected) | No new loop. |
| Context is managed, never assumed | Held | This change is a direct instance of the invariant — a cheap signal against redundant re-processing across a long navigation. |
| Guardrails are not optional | Held | The #439 fabricated-artifact-path guard in `read_file` runs unchanged before the nudge logic; the nudge only prefixes the return value after that guard and the real read both succeed. |
| Failure is explicit | Held | The nudge computation (`os.Stat`) failing silently falls through to the existing unchanged error path — proven by inspection, no new error path introduced. |
| State stays local and inspectable | Held | In-memory tracker only, derived from the file's own mtime — nothing new persisted. |
| Single binary, no framework | Held | No new dependency. |

## Failure paths forced

- File genuinely changed between reads (content + mtime) → no false "unchanged" nudge.
- No active navigation → nudge logic never engages, zero behavior change verified directly.
- Navigation pointer cleared and reused for the same run ID → tracker does not leak across
  the boundary.

## Provider parity

Not applicable — this change touches only local tool-output construction (`read_file`), not
provider-facing request/response handling. No provider-specific code path exists to verify.

## Open concerns (carried to the ship note)

1. The nudge is scoped to exact path identity (no relative/absolute or symlink
   normalization), matching every other path-keyed mechanism already in the codebase.
2. No production metric proves the nudge's real-world token impact; that needs live usage
   data, not a locally-testable harness property.
