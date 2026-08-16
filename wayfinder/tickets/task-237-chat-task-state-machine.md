# Chat Tasks — task state machine for chat-originated work

Status: **RESOLVED** (closes GitHub issue #237, absorbs #236)

## Question

Large multi-phase requests run as unstructured chat turns: every iteration
re-sends the whole session's history (the context tax — 2M tokens for one
webpage redesign). The playbook engine is already a state machine (stages,
contracts, write guards, state.json); chat work never enters it, because the
model has `run_playbook` but never offers it.

## Resolution

Chat-originated tasks enter the playbook state machine through a
harness-owned three-part flow:

1. **Detection + offer.** Task-intent phrasing ("build me X", "redesign X",
   "fix this", "create/make X", "coding task: ...", "run this as a task")
   injects an offer into the turn tail. The offer is a discussion opener —
   no scaffold, no work, until the owner approves. Trivial requests carry no
   task verb and never match; "make sure ..." (the one false-positive class)
   is excluded.
2. **taskify scaffold.** On approval the model calls `taskify`, which
   scaffolds the owner-locked loop as an ordinary playbook in the same engine
   and namespace: 00-plan → 01-decompose (owner approval gate; nothing
   executes before) → 02-act (bounded) → 03-observe (verification as a named
   stage) → 04-repeat (gaps → checkpoint split). The model adapts the
   decomposition; the harness guarantees the loop, the checkpoints, and the
   gate (approval intercepted at the message entry point — the model can
   never approve its own run).
3. **Checkpoint split + cap-resume.** A taskified stage that burns >80% of
   its iteration budget without declared outputs gets a split offer instead
   of a hard fail: the partial state on disk is the checkpoint; `split_stage`
   accept creates 02-act-b and the run resumes mid-task, never from zero.
   Stage context = contract + prior stages' declared outputs; raw session
   history stays out of taskified runs. The #238 boundedness check runs over
   every scaffold contract at creation time.

## Acceptance criteria (all met)

- [x] Task-intent message → offer only, no playbook created until approved
- [x] Approving scaffolds the 5-state shape (00-plan … 04-repeat)
- [x] Over-scoped stage triggers split offer; accepting creates a checkpoint;
      cap-hit resume continues mid-task, never from zero
- [x] Stage 2 context contains only contract + stage 1 outputs
- [x] Trivial requests never offer
- [x] Chat-task playbooks live in the same engine — no new store
- [x] #238 integration: unbounded stage contracts flagged at scaffold time

## Validation

- 19 new tests (taskify_test.go), 706 total green incl. `-race`
- `go build ./...`, `go vet ./...` clean
