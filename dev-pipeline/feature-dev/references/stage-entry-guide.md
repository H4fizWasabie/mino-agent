# Stage Entry Guide

Which stage to enter for which kind of work. Read this before skipping a stage.

## Full Run

A new capability runs 01 through 05. This is the default. If you are unsure, run the full
pipeline. Intake is cheap and catches expensive mistakes.

## Legitimate Shortcuts

**Bug with a known cause.** Enter at 03. The problem statement is the bug report and the
design is already settled by how the code works today. Still run 04 and 05.

**Bug with an unknown cause.** Enter at 02. Investigation is design work. Once the cause is
found, the design note records both the cause and the fix.

**Architecture decision with no code.** Run 01 and 02, then stop. The design note is the
deliverable and it becomes an entry in `shared/decision-log.md`. Implementation is a later
run that enters at 03 reading that note.

**Documentation-only correction.** Enter at 05. There is nothing to design or verify.

## Not Worth the Pipeline

Typo fixes, formatting, dependency bumps with no behaviour change, and comment edits. Make
the change directly. The pipeline costs more than these changes are worth.

## What Never Gets Skipped

Stage 04 runs for every change that touches code. A change that has not been verified has
not been made. Stage 05 runs for every change a user could notice.

## Rejoining After a Failure

A failed verification sends the work back to 03, not to 01. The intake and design outputs
still stand. Rewrite them only when verification proves the design itself was wrong, and in
that case say so explicitly in the new design note.
