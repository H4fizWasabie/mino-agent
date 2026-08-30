# Design: additive stage-aware tool exposure

Issue: #449; coordinated with #450 and #451

The active stage contributes tool schemas to the normal loop. It does not
replace the always-available tools or the sliding/contextual selection, and it
does not act as an execution whitelist:

`always available ∪ sliding/contextual ∪ active-stage capabilities`

`RunLoopContext` reads the active stage capability names from the existing
context seam. `SchemasForContext` adds registered stage tools before the
existing selection and keeps them immune from the current turn's cap. The
playbook runner passes the canonical registry rather than `Registry.Only`.

The registry remains the execution boundary. Risk behavior, approvals,
unknown-tool handling, output verification, retries, and iteration limits are
unchanged. #447 therefore reports output-contract deviations and verification
failures, not a tool merely absent from the stage declaration.

No new loop, provider adapter, configuration key, dependency, or persistence
format is introduced. The context value is the small shared seam that #450
and #451 can reuse for durable run checkpoints and output verification.
