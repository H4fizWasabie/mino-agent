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
- [ ] Safe repair, adaptation, retry, and escalation boundaries are explicit.
- [ ] Existing playbooks are assessed before implementation tickets are split.
- [ ] Narrow child tickets are opened only after the model is agreed.
