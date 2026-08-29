# Playbooks -- remove taskify

Status: **IMPLEMENTED** (child of PB-001)

## Destination

Remove the obsolete `taskify` chat-task feature now that Mino operates ICM
playbooks directly. Mino remains the single canonical agent; a playbook is an
optional scheduled task/workspace that Mino enters and operates.

## Why

`taskify` was introduced for chat-originated coding tasks: it offered a
five-stage scaffold, required approval, and created a task-specific playbook.
The ICM playbook model now makes that wrapper unnecessary. Keeping it adds a
second task concept and preserves approval/gate behavior that does not belong
to autonomous scheduled playbook execution.

## Scope

- Remove the `taskify` tool and its scaffold-specific playbook metadata.
- Remove task-intent detection, offer injection, approval-turn routing, and
  taskify-only fence/checkpoint behavior.
- Remove or migrate tests and documentation that describe taskify as the
  normal path for playbook work.
- Preserve the canonical loop, `manage_playbook`, `run_playbook`, scheduling,
  workspace navigation, validation, recovery, audit, and coding-agent approval
  policy.
- Do not remove generic playbook run recovery merely because taskify used it.

## Acceptance criteria

- [x] Mino no longer exposes or calls `taskify` for new work.
- [x] A normal task-intent message does not create a taskify offer or approval
      gate.
- [x] Existing scheduled and manually invoked playbooks continue to run and
      resume through the ordinary playbook engine.
- [x] No taskify-owned approval marker or scaffold is created in a playbook.
- [x] Taskify-specific tests/docs are removed or rewritten to the ICM model.
- [x] The full test suite passes and the deployed behavior is verified before
      release.

## Out of scope

- Replacing playbooks with another task engine.
- Removing owner approval for consequential coding, release, deployment, or
  other live mutations.
- Changing the playbook stage contract, scheduler, or recovery model except
  where taskify coupling requires it.
