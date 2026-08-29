# Playbooks -- allow adaptive workspace maintenance

Status: **IMPLEMENTED** (child of PB-001, GitHub issue #391)

## Destination

Allow Mino to inspect, repair, and adapt playbook source files when run
evidence shows that the workspace contract, routing, inputs, script, or output
definition caused a failure.

## Why

An ICM playbook is a navigable workspace, not an immutable prompt bundle.
Mino must be able to improve the source contract that produced a recurring
failure, then validate and resume or rerun through the normal playbook engine.
The taskify-era work-tool fence currently treats playbook maintenance as
forbidden task work, even though `manage_playbook` is the intended definition
authority.

## Authority model

- Mino owns ordinary playbook navigation, diagnosis, repair, adaptation, and
  safe retry.
- `manage_playbook` remains the definition mutation boundary; generic
  filesystem tools must not bypass its validation and audit behavior.
- Run state and prior artifacts remain evidence and are never silently erased.
- A source change normally applies to future runs. Applying a changed
  contract to an existing resumable run must be explicit in the run evidence.
- Uncertain external side effects, destructive changes, and ambiguous authority
  remain stop-and-escalate cases.

## Scope

- Remove the taskify-era restriction that blocks Mino from using
  `manage_playbook` to maintain a playbook.
- Permit inspect → diagnose → update/create the smallest source change →
  validate → resume or rerun when the recovery policy allows it.
- Preserve atomic writes, name/path validation, secret checks, script checks,
  persona validation, schedule protections, retry safety, and audit records.
- Record the evidence and reason for each adaptive source change.

## Acceptance criteria

- [x] Mino can update and validate a playbook in response to a diagnosed
      contract, routing, input, or output failure without taskify.
- [x] A task-intent offer or coding-agent approval path cannot block ordinary
      playbook maintenance.
- [x] Existing run artifacts and state remain intact after a definition repair.
- [x] The runtime distinguishes a future-run source change from an explicit
      active-run contract repair.
- [x] Unsafe retries and consequential external mutations still require their
      existing verification or owner-approval boundary.
- [x] Regression tests cover successful adaptation, invalid updates, active
      run protection, and truthful failure/escalation.

## Out of scope

- Automatic blind rewriting after every failure.
- Removing validation or audit protections from `manage_playbook`.
- Allowing Mino to alter its binary, release lane, or production deployment
  without the existing approval policy.
