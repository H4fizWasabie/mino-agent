# Implementation manifest: #453

Branch: `fix/issue-453-token-discipline`

## Files changed

- `playbook_nav.go`: added `navRead`/`navReads`/`noteNavRead`/`clearNavReads`; `clearSessionNav`
  now also clears the finishing run's read tracker. Also corrected a stale doc comment left
  over from #450/#451 that still described the scheduler as using the old dedicated-loop
  `traceTagKey` path (superseded by #452).
- `tools.go`: `makeReadTool`'s `ContextFn` computes a nudge (via `os.Stat` + `sessionNav` +
  `noteNavRead`) before reading, and prefixes it onto every return path (`End of file.`, the
  paged/truncated chunk, and the plain chunk) without altering any of them otherwise.
- `playbook_workspace.go`: one new bullet in `buildWorkspaceStagePrompt`'s `## Rules` section.
- `playbook_token_discipline_test.go` (new): nudge on unchanged re-read during navigation, no
  nudge outside navigation, no nudge on a genuinely changed file, and read-tracker cleared
  with the session's navigation pointer.

## New interfaces

See `../02-design/output/issue-453-design.md`'s Interfaces section — unchanged from design.

## New config keys

None.

## Tests added

`TestReadFileNudgesOnUnchangedRereadDuringNavigation`, `TestReadFileNoNudgeOutsideNavigation`,
`TestReadFileNoNudgeWhenFileChanged`, `TestClearSessionNavClearsReadTracker`.

## Build and test results

- `go build ./...`: PASS.
- Focused new tests (4): PASS.
- Full suite: see `../04-verify/output/issue-453-verification.md`.

## Deferred / known limitations

Carried from the design note: the nudge is path-identity-scoped (no symlink/relative-path
normalization, matching every other path-keyed mechanism in the codebase); no production
metric proves the nudge's real-world token impact — that needs live usage data.
