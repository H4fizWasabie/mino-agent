# Intake: additive stage-aware tool exposure

Issue: #449
Status: implemented on `fix/issue-449-stage-tools`

## Problem

Playbook stages currently run through `Registry.Only`, so a stage's `Tools`
list becomes a hard execution whitelist. That removes the LLM's normal choices
and is incompatible with the parent map's single-loop model.

## Decision

Stage tools are additive capabilities:

`always available ∪ sliding/contextual ∪ active-stage capabilities`

The runtime registry, side-effect risk, approval gates, output verification,
and iteration bounds remain authoritative. A registered tool is not a
deviation merely because it is absent from the stage declaration.

## Scope

Remove the stage-only registry path, pass active stage capability names through
the existing loop context, and include those schemas in the existing capped
selection. Keep the change model-agnostic with no new configuration or
dependency.
