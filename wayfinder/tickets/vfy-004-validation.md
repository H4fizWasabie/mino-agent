## Resolution

Status: **RESOLVED** (historic — decided/shipped; predates the Status-line format)

**Regression test + trace observation.** Create a test playbook, ask Mino to remove it, verify traces show:
1. Delete + schedule cancel + crontab check (if applicable)
2. Before final reply: read-back or system_check call
3. Reply includes confirmation of what was actually removed/changed

No eval framework needed — observe the next natural state-mutation task after deploy.

## Context

Unlike the schema optimization (measurable: schemas dropped from 30→21), this is a behavioral change. Harder to measure quantitatively.

## Options

**A. Regression test: repeat the incidents**
Ask Mino to "remove a test playbook and its schedule" and observe traces. Check that it:
1. Deletes the playbook folder
2. Reads `schedules.json`
3. Removes the entry
4. Verifies by reading `schedules.json` again
5. Only then declares done

**B. Trace pattern analysis**
After deploying the fix, monitor traces for `read_file` or `bash` calls that happen AFTER state mutations but BEFORE the final reply. These would be verification steps.

**C. SOUL.md audit**
Before/after of Mino's self-description. Does SOUL.md now contain a verification rule that Mino follows?

**D. Observe in production**
Deploy, wait for natural usage patterns. Watch for the next time Mino mutates schedules, crons, or playbooks. Compare behavior to today's incidents.
