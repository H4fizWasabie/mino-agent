# Ship note: #449

Stage-aware tool declarations now expand the canonical loop's tool surface
instead of restricting execution to a stage-only registry. The LLM retains
its always-available and sliding/contextual choices while gaining the tools
declared useful for the active stage. Runtime registration, risk, approval,
output verification, and iteration boundaries remain authoritative.

Release/tag/deployment intentionally not performed. This change is for the
batch release after the remaining approved Wayfinder issues are merged.
