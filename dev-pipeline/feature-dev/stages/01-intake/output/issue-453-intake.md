# Intake: token-efficiency discipline for workspace navigation

Issue: #453. Grilled 2026-08-31, agreed in full.

## Problem

The owner wants Mino to use the least tokens a task actually needs — a playbook that doesn't
need much context shouldn't burn tokens navigating exhaustively, while genuinely complex
tasks are fine spending more. Three sub-asks from the issue body:

1. Scoped reading — only load what a declared step's Inputs table says.
2. Avoiding redundant re-reads across a session.
3. Keeping mechanical/script stages zero-inference even when reached via navigation.

## Decision

- (3) is already solved: #450/#451/#452's `navigatePlaybookRun` drives script-backed stages
  straight through with no model call at all, before this ticket started.
- (1) is guidance, not enforcement — a line in `buildWorkspaceStagePrompt`'s existing `##
  Rules` section, matching #449's explicit precedent against turning stage declarations into
  a second enforcement layer.
- (2) needed a real mechanism: nothing today remembers what's already been read across the
  many turns a playbook navigation spans (the existing `knownArtifactsKey` resets every
  turn). A new run-scoped read tracker (`navReads` in `playbook_nav.go`) records path + mtime
  per navigation run; `read_file` nudges — never withholds — when a path is unchanged since
  its last read this run, so the model can choose not to re-process content it already has.
  Content is always returned in full regardless of the nudge, so no verification-discipline
  invariant (CTX-003, CTX-016, "verify-then-claim") is put at risk.

## Non-goals

- Withholding or truncating `read_file` content based on prior reads — rejected as a real
  correctness risk against the harness's own verify-then-claim discipline.
- Any change to `read_file`'s behavior outside an active playbook navigation — the tracker
  and nudge are scoped to `sessionNav`, zero behavior change for ordinary chat/tool use.
- Redesigning #444/#445/#446 (separate tickets).

## Surfaces touched

`playbook_nav.go` (new read tracker), `tools.go` (`read_file`'s nudge), `playbook_workspace.go`
(Rules-section guidance line).

## Acceptance criteria

1. A path read twice within the same navigation run, unchanged on disk between reads, gets a
   nudge on the second read; the full content is still returned both times.
2. The same path read twice with real content changes between reads never gets the nudge.
3. Reading the same path outside any active navigation (no `sessionNav` pointer) never gets
   the nudge, regardless of how many times it's read — zero behavior change for ordinary use.
4. Clearing a session's navigation pointer (run complete/failed/interrupted) also clears that
   run's read tracker.
5. `go build ./...` and the full test suite pass; no invariant broken.
