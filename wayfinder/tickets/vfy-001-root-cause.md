## Resolution

Status: **RESOLVED** (historic — decided/shipped; predates the Status-line format)

**Both. SOUL.md's rule is counterproductive.**

```
Before replying, silently verify... tool evidence does.
```

"Silently verify" = think, don't check. "Tool evidence" = `"ok"` is enough. Mino runs `rm -rf`, sees `"ok"`, thinks "done" — never checks `schedules.json`.

Tool feedback is too generic: `"ok"` means the command ran, not that the task is complete. The harness must tell Mino *what changed*, not just *that something succeeded*.

## Context

Three incidents today share the same pattern:

**Incident 1: Surgery schedule removal**
User: "remove surgery-pds-reorder playbook and its scheduler"
Mino: `rm -rf ~/.mino/playbooks/surgery-pds-reorder` → success → "Done!"
Actual: `schedules.json` still has `surgery-pds-reorder` entry. Scheduler will try to fire it tomorrow.

Why Mino stopped: tool returned success for the delete. Mino didn't check `schedules.json`.

**Incident 2: Cron safety net**
User: "Option 2 — proper fix" (for playbook delivery)
Mino: wrote `delivery-safety-net.sh`, ran it once, declared "Runs every 5 minutes via system cron"
Actual: `crontab -l` is empty. Script exists but never scheduled.

Why Mino stopped: wrote the script, ran it successfully, equated "script works" with "cron installed."

**Incident 3: Playbook delivery**
User: "I didn't get the news"
Mino: diagnosed root cause (notify flag not read), created PLAYBOOK_PROTOCOL.md, patched 7 playbook stages, created delivery safety net
Actual: 6 playbooks had stale output that was never delivered. Only the safety net's first manual run caught up.

Why Mino stopped: patched the root cause but didn't verify delivery of existing outputs.

## Patterns to investigate

1. Does Mino's system prompt (SOUL.md) say anything about verification?
2. Do tool responses provide enough signal for Mino to know verification is needed?
3. Is there a common phrase Mino uses when it makes this mistake? ("Done!", "All clean!", etc.)
4. Does Mino ever call `read_file` or `bash` to verify a mutation took effect?
