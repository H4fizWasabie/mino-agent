## Resolution

**Enrich tool responses with what actually changed.** Three targets:

1. **`cancel_schedule`** — return `"removed 'X' from schedules.json. N schedules remaining."` instead of generic success.

2. **`bash` for cron** — when Mino pipes to `crontab`, include verification hint: `"crontab updated. Verify with: crontab -l"`

3. **New `system_check` tool** — lightweight, checks schedules, cron, reminders, playbooks. Returns structured summary. Mino calls it before declaring "done" on state-mutating turns.

Fix SOUL.md while at it: change `"silently verify"` → `"verify with tools before replying."`

## Options

**A. Richer tool feedback**
When `bash` returns success, include a hint about what changed vs what didn't. Example: instead of `"ok"`, return `"ok — wrote 3 lines to /etc/crontab"`. For destructive operations, return what was affected.

Risk: changing tool output format might break existing LLM parsing. Some tools return structured output already (e.g., `write_file` reports byte count).

**B. Verification tool**
A dedicated `verify` tool that checks common state: schedules, cron, file existence, playbook status. `verify("schedule", "surgery-pds-reorder")` → "present in schedules.json". `verify("cron", "delivery-safety-net")` → "not found in crontab".

Risk: unbounded — infinite verification targets for infinite state. But a small set of common checks (schedule, cron, file, playbook) might cover 90% of cases.

**C. Loop-level verification gate**
Before `turn_end`, if the turn involved state mutation, inject a system message: "List the changes you made and verify each one is in place." This forces an extra iteration but ensures verification.

Risk: adds latency to every mutating turn. Might cause loops if verification itself mutates state.

**D. Better bash output conventions**
Mino's bash tool already supports RTK for output compression. Could add a post-execution hook that checks for common "missed step" patterns: "script written but not in crontab," "directory deleted but schedule entry remains."

Risk: fragile — pattern matching on bash output. High maintenance.
