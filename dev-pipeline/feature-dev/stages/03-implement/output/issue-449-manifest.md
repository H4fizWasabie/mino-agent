# Implementation manifest: #449

Branch: `fix/issue-449-stage-tools`

- `loop.go`: read active stage capability names from context and pass them to
  schema selection.
- `tools.go`: add registered stage capabilities to the existing schema union;
  preserve always-available and sliding tools.
- `playbook_workspace.go`: use the canonical registry for LLM stages and pass
  stage capability names through context; stop classifying absent stage tools
  as deviations.
- Tests cover additive schema exposure, stage execution using the canonical
  registry, and the revised mechanical deviation boundary.
- `CHANGELOG.md` and `README.md` document the model.
