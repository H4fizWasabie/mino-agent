# Intake: restore ICM-scoped context for playbook navigation

Issue: #477. Live incident, 2026-08-31.

## Problem

A chat-triggered `instagram-daily-capability` run hit the 60-iteration ceiling and needed
multiple full restarts, where the same playbook used to complete in "a few iterations" under
the old dedicated stage loop. The model didn't get weaker — the context around it did.

The dedicated stage loop (`BuildPlaybookSystem`) gave each stage a narrow, purpose-built
system prompt: playbook rails, workspace map, persona, nothing else. The unified-loop
navigation shipped in #450/#451/#452 runs every playbook-navigating turn through the full
general-chat system prompt (`buildSystem`) instead — verification/provenance/tool-preference/
iteration-discipline text, plus skill-matching and owner-fact-matching overhead, none of
which playbook navigation needs.

This is exactly the failure mode the Interpretable Context Methodology (the philosophy
behind this repo's `dev-pipeline`) names directly: "Layered context loading. Agents load
only the context they need for the current stage. Less irrelevant context means better
model performance. This is prevention rather than compression," contrasted against "a
monolithic approach where all stage instructions, all reference files, and all prior
outputs are loaded into a single prompt... pushing into the range where models start
losing track of what matters."

## Decision

A turn already known, at its start, to be navigating a playbook gets the narrow
`BuildPlaybookSystem` prompt (reused unchanged) and a matching narrow message-context
builder, instead of `buildSystem`/`ContextFor`:

- **Scheduled fires**: always — a scheduled fire's entire purpose is exactly one playbook,
  derivable from its deterministic `"scheduled-<name>"` session ID before any tool call.
- **Chat continuations**: whenever the existing `sessionNav` pointer already shows an active
  run for this session at the start of a new turn — an earlier message already started
  navigating and it's still in progress.

## Non-goals

- The very first chat message that triggers a brand-new playbook run within one turn is not
  covered: the system prompt is fixed for the whole turn (provider cache stability), and the
  harness doesn't know it's playbook work until the model itself decides to call
  `run_playbook`, partway through that same turn. Scheduled fires and multi-message chat
  navigation are the primary real-world cases and are fully covered.
- No change to `BuildPlaybookSystem`, `PlaybookContext`, or the dedicated stage loop
  (`runWorkspacePlaybook`) — both reused/left untouched.

## Acceptance criteria

1. A scheduled fire's system prompt contains `playbookRails` and excludes general-chat-only
   text (tool-preference rules, iteration-discipline text).
2. An ordinary chat turn with no active navigation still gets the full general-chat prompt,
   unchanged.
3. A chat turn continuing an already-active navigation (sessionNav set from an earlier
   message) gets the narrow prompt, same as a scheduled fire.
4. Full test suite passes (excluding any concurrent, unrelated work-in-progress elsewhere in
   the tree — verified by inspection, not assumed).
