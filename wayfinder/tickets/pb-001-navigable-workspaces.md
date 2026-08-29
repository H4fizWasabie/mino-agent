# Playbooks -- navigable workspaces

Status: **OPEN** (Wayfinder ticket, PB-001 -- GitHub issue #380)

## Destination

Make a Mino playbook a navigable filesystem workspace that one Mino agent can
enter, understand, execute, recover, and maintain. The public vocabulary stays
"playbook"; the underlying method is structured context routing through files.

## Why

Mino's current runner loads a named playbook and executes numbered stages. A
failed stage generally becomes a failed run and a report. That loses the useful
diagnostic question: which object, stage, artifact, persona, contract, or
environment caused the failure, and what safe next action is available?

The playbook should provide the map that lets Mino answer those questions and
continue intelligently instead of rerunning blindly or immediately reporting
failure to the owner.

## Core model

- Mino is the single agent and canonical loop.
- The playbook root is the workspace entry point.
- Root and stage context files route the agent and scope what it loads.
- Personas and skills are roles and context, not separate agents.
- References are stable factory configuration; outputs are run-specific working
  artifacts.
- Run state, artifacts, audits, and external receipts are navigable evidence.
- Mino decides route, stage, persona, retry, repair, adaptation, or stop.
- Owner approval remains a policy boundary for consequential actions, not a
  mandatory stage mechanism.

## Decision -- 2026-08-29

Mino playbooks adopt the filesystem and context-routing architecture of ICM,
but do not adopt ICM's mandatory human-in-the-loop execution model. A Mino
playbook is an autonomous workflow: Mino decides whether to continue, revise,
reroute, repair, retry, or stop at each stage boundary.

Human checkpoints become internal agent decision points. No separate persisted
HITL checkpoint is required in the playbook model. Owner approval remains a
separate Mino authority policy for consequential actions; it is not a required
step between ordinary stages.

## Decision -- tool exposure -- 2026-08-29

Stage tool lists will not remain rigid allowlists. A workspace stage may declare
capabilities or useful tool guidance, but Mino may select another available tool
when the route or recovery evidence requires it.

Mino's runtime remains authoritative for actual tool availability, side-effect
risk, retry safety, audit records, and owner approval. Removing the stage
whitelist is not permission to expose or retry every tool blindly.

## Decision -- orientation and output lifecycle -- 2026-08-29

A playbook run begins with workspace orientation: Mino receives the root
workspace map, stage graph, active run identity, and current progress before
the prompt narrows to the current stage contract and its working inputs.

Workspace outputs remain first-class run artifacts, but workspace navigation
does not replace Mino's output policy. The existing pipeline remains the
authority for what is retained for handoff, preserved for audit and resume,
and distilled into episodic or semantic memory. Mino must not inject every
artifact into every later prompt or promote every artifact into the knowledge
base.

## Failure recovery boundary

| Failure | Default response |
|---|---|
| Contract, input, or routing defect | Inspect and repair or adapt safely |
| Model or tool failure | Change approach and retry when safe |
| Output audit failure | Revise the artifact and re-audit |
| Uncertain external side effect | Verify receipt or idempotency before retry |
| Ambiguous or consequential authority | Escalate to the owner |

## Scope

- Assess existing playbooks against the workspace model without changing their
  behavior.
- Define the minimum routing, identity, artifact, recovery, and evidence
  vocabulary needed by the runtime.
- Reuse existing durable runs, output attribution, contract hashes,
  post-mortem evidence, design-time audits, and retry-safety checks.
- Make post-mortem and contract-audit results usable as internal recovery
  inputs rather than only final reports.

## Out of scope

- An agent for every task, stage, persona, or workspace.
- A second workflow engine or agent loop.
- Blind retries after uncertain external mutations.
- Replacing filesystem truth with a graph or dashboard.

## Acceptance criteria

- [ ] The playbook-as-workspace model is documented in Mino terminology.
- [ ] Mino can identify the active playbook, run, stage, inputs, outputs, and
      failure reason from durable state.
- [ ] A run's initial context establishes workspace orientation before stage
      execution narrows context.
- [ ] Stage outputs are stored and attributed before filtering or distillation;
      retention policy remains separate from workspace navigation.
- [ ] Stage tool guidance is separated from runtime tool authority and retry
      safety.
- [ ] Safe repair, adaptation, retry, and escalation boundaries are explicit.
- [ ] Existing playbooks are assessed before implementation tickets are split.
- [ ] Narrow child tickets are opened only after the model is agreed.
