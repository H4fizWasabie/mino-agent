# Design: token-efficiency discipline for workspace navigation

Issue: #453; builds on #450/#451/#452 (all merged).

## Chosen approach

`playbook_nav.go` gains a run-scoped read tracker (`navReads map[string]map[string]navRead`,
mutex-guarded, same pattern as the existing `navPtr` map) recording, per run ID, the last time
each path was read and its mtime at that moment. `read_file` (`makeReadTool` in `tools.go`)
consults `sessionNav(sessionID)` before reading; if the session is navigating a run and the
path's mtime matches its last recorded read for that run, the tool prepends a short note to
its (otherwise unchanged) output. `clearSessionNav` — already the single point where a run's
navigation ends — also clears that run's read tracker.

A guidance line was added to `buildWorkspaceStagePrompt`'s `## Rules` section pointing at both
the "read only declared inputs" principle and the nudge's meaning, so the model has textual
grounding for what the nudge means without a schema or contract change.

## Interfaces

- `navRead{At, ModAt time.Time}` and `noteNavRead(runID, path string, modAt time.Time)
  (navRead, bool)` / `clearNavReads(runID string)` — playbook_nav.go.
- `read_file`'s `ContextFn`: no schema change, no new argument. The nudge is a prefix on the
  existing string return value.
- `buildWorkspaceStagePrompt`: one new bullet in the existing `## Rules` section.

## Config surface

None.

## Failure behaviour

- **Nudge computed but the read itself fails** (missing file, permission error): the nudge is
  computed from `os.Stat` before `os.ReadFile`; if `os.Stat` fails, `navNote` stays empty and
  the existing error path is unchanged — no behavior change on failure.
- **Clock/mtime resolution edge cases**: the nudge only fires on exact mtime equality between
  the current stat and the previously recorded one; any filesystem write updates mtime, so a
  false "unchanged" nudge on a genuinely modified file is prevented by relying on the OS's own
  mtime guarantee rather than a content hash (cheaper, and Go's `os.Chtimes`-based test proves
  the boundary explicitly).
- **Nudge should never affect correctness**: content is returned in full every time regardless
  of the nudge — verified by a test that greps for the actual file content on both the nudged
  and non-nudged path.

## Invariant check

- **Model agnosticism**: held — no provider-specific code.
- **Loop termination**: held (unaffected) — no new loop.
- **Context is managed, never assumed**: held — this change directly serves that invariant by
  giving the model a cheap signal to avoid redundant re-processing of unchanged content across
  a long navigation.
- **Guardrails are not optional**: held — `read_file`'s existing `knownArtifactsKey`
  fabricated-path guard (issue #439) is untouched; the nudge only prefixes the return value
  after that guard already passed.
- **Failure is explicit**: held — the nudge computation never suppresses or alters an error
  path.
- **State stays local and inspectable**: held — the tracker is in-memory only, derived
  entirely from the filesystem's own mtimes; nothing persisted that isn't already inspectable
  via the file itself.
- **Single binary, no framework**: held — no new dependency.

## Known limitations (carried to the ship note)

1. The nudge only fires on an exact `read_file` call to the identical path used before —
   reading via a different but equivalent path (relative vs. absolute, symlink) won't match.
   Acceptable: the same limitation already applies to every other path-keyed mechanism in the
   codebase (`playbookWriteGuard`, `knownArtifactsKey`).
2. This measures nothing — there's no dashboard/metric proving actual token savings from the
   nudge in production. Verifying real-world impact needs live usage data, out of scope for a
   locally-testable harness change.

## Files to touch

`playbook_nav.go`, `tools.go`, `playbook_workspace.go`, `playbook_token_discipline_test.go`
(new).
