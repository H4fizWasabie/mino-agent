# Memory & Context Drift — staleness enforcement (DRF-002)

Status: **CLOSED** (wayfinder ticket, DRF-002) — implemented 2026-08-14.

## Question

CTX-014 (v2.8.9) shipped *visibility* for stale facts ("ranking score untouched — purely a visibility signal"), and DRF-001 (v2.9.0) shipped provenance weighting + domain-based conflict markers + the rebuild guard. The 2026-08-14 full-capability live session demonstrated the enforcement gap: a 6-day-old fact (`mino_runs_on_qwen3_7flash`, wrong since the 08-11 main swap) was recalled WITH its age marker and trusted anyway; a second fact (`model_stack`, claiming gpt-5.6-luna-pro main) was born stale and coexisted with the first without any conflict flag. The umbrella claim "Memory drift prevention" over-promised relative to the visibility-only spec.

## Decisions (2026-08-14 session)

1. **30d backstop** for model-authored semantic facts (protects the durable distill library; volatile facts are covered by `stale_after` + correction demotion — the classes that went stale in days are caught by those, not the backstop).
2. **Reuse the archive mechanism** (`ArchiveFact` / `memories/archive/`, reason `"stale"`) — archived facts stay answerable via remember's fallback; knowledge is demoted, never deleted.
3. **Asymmetry kept**: only explicit corrections (`user-correction-*`, `agent-correction-*`) demote conflicting model facts; a model re-entry demotes nothing; a plain user save states new knowledge without claiming the old is wrong.

## Implementation

- `ArchiveStaleSemantic(cutoff)`: model-authored semantic facts archive with reason `"stale"` when past their staleness point — the declared `stale_after` when set, else `At` past the 30d backstop. Authoritative facts (user / user-correction / agent-correction) never auto-stale. Wired into `MaintainGraph` next to the episodic expiry pass.
- `stale_after` front-matter field (volatile facts declare their own expiry; round-trips through write/parse).
- `markConflictSignals` extended from domain-based to **subject-based**: two top facts sharing ≥ 2 significant subject words with materially different bodies get the `⚠ conflicts with <id>` marker. Loose by design — a false flag costs one glance, a missed contradiction costs a wrong fact being trusted.
- Record-time **correction demotion**: a `user-correction-*` / `agent-correction-*` fact archives conflicting model facts on the same subject (reason `"superseded"`).
- Provenance honesty: `save_note` stamps `model-distill` inside playbook runs (via the `playbookDepth` counter) instead of `user` — the daily-ai-concept learnings are Mino's, not the owner's. `agent-correction-YYYYMMDD` formalized as a first-class source.
- Tests: `TestArchiveStaleSemantic`, `TestCorrectionDemotesConflictingModelFacts`, `TestMarkConflictSignalsSubjectBased`, `TestSaveNoteStampsModelDistillInsidePlaybook`, `TestStaleAfterRoundTrip`.

## Why this closes the live case

The qwen fact was wrong in 6 days and born-stale facts existed in 1 — no backstop threshold catches that class; `stale_after` (volatile facts marked at save time) and correction demotion (the moment a correct fact lands, the wrong one archives) do. The 30d backstop is the backstop for the weeks-scale class.

## Out of scope

- Deleting facts (archive only, fallback answers)
- Threshold tuning without evidence (30d chosen over 14d to protect the concept library)
- The judgment gap (a brain that verifies instead of trusts) — prompt-level, deliberately not a code gate
