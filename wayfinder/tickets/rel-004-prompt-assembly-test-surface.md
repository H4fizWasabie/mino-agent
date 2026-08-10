# Prompt-Assembly Test Surface — The Bugs That Never Happen Twice

Type: `wayfinder:task` (AFK — decide the rule, then it's execution)

## Question

Which functions must carry table-driven tests before any new feature ships, so the failure class that ate today (prompt-assembly bugs: unresolvable inputs, poisoned run requests) can't recur?

## Context

- Two production failures today were 5-line bugs in prompt assembly — `buildWorkspaceStagePrompt` (inputs never resolved) and the run_playbook request (tail-injected routing leaked into stage prompts). Zero tests covered that path; 366 tests existed elsewhere.
- The fixes landed with table-driven tests that caught real edge cases within minutes (triple-newline markers, header paths).
- The rule should be cheap to enforce: name the seam, not a whole test-suite regime.

## Ask

- Which seams: every function that renders a prompt or a tool schema (build*Prompt, render*Input, clean*Request, schema builders)?
- Enforce by rule (AGENTS.md) or by CI check (test that asserts tests exist — overkill?)?
- Do playbook contracts count (CONTEXT.md parse/rendering), or only Go?
