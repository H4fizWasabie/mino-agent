# Playbooks -- workspace entry contract

Status: **OPEN** (child of PB-001)

## Destination

Let Mino enter a user-authored playbook workspace through its `AGENTS.md` and
root `CONTEXT.md`, then route into the selected numbered stage without loading
unrelated workspace content.

## Reference fixture

`/home/marketing-strategies-ascencio` on the VPS. Its routing check passes with
58 references verified and no orphans.

## Scope

- Load and validate root `AGENTS.md` as Layer 0.
- Load root `CONTEXT.md` as Layer 1 routing context.
- Preserve numbered stage discovery and existing script/LLM stage paths.
- Keep workspace definitions separate from Mino runtime state.
- Preserve Mino persona binding and inject workspace persona context selectively.

## Acceptance criteria

- [ ] A workspace without `AGENTS.md` fails clearly once migrated to the new
      contract; legacy compatibility is an explicit transition decision.
- [ ] The run can identify the workspace map, root route, active stage, and
      selected persona before stage execution.
- [ ] Stage prompts receive only the current contract and declared inputs.
- [ ] Existing playbook tests and the marketing fixture's routing check remain
      green.
