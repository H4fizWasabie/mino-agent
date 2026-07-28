## Resolution

**Not the primary fix.** Harness-level enrichment (VFY-002) is more reliable. One companion change: fix SOUL.md's counterproductive rule — `"silently verify"` → `"verify with tools before replying"`. That's it. No skill, no examples, no token bloat.

## Context

Mino already has SOUL.md as its persona/system prompt. It already follows playbook protocols. The question: can a behavioral rule solve this without touching Go code?

## Options

**A. SOUL.md verification rule**
Add to SOUL.md:
```
## Verification rule
After any state-mutating operation (writing files, deleting directories, 
modifying schedules, installing cron jobs), you MUST verify the change:
- Read back the file you wrote
- List the directory you modified
- Check the registry you updated (schedules, crontab, reminders)
Do NOT declare "done" until verification confirms the change is in place.
```

Risk: adds tokens to every turn. Mino might over-verify trivial operations (replying "hello" doesn't need verification).

**B. Verification skill**
Create a skill that triggers on state-mutation keywords: "delete", "remove", "install", "schedule", "setup", "configure". The skill would instruct Mino to verify after each mutation.

Risk: same as SOUL.md rule but selective — only activates on mutating turns, saving tokens.

**C. Post-turn self-check pattern**
Teach Mino a 2-step pattern: "mutate → verify → declare." Build it into the playbook protocol as a convention. Show examples in PLAYBOOK_PROTOCOL.md.

Risk: only applies to playbook execution, not ad-hoc turns.

**D. Few-shot examples in SOUL.md**
Include 2-3 concrete examples of the verification pattern in SOUL.md. Mimo v2.5 responds well to examples.

Risk: examples take tokens. Need to keep them concise.
