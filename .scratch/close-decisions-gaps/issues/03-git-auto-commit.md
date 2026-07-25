# Git auto-commit before bash

Status: resolved
Type: grilling
Blocked by: —

## Question

Before bash executes, Mino should `git add -A && git commit -m "pre-bash snapshot"` so a destructive command can be rolled back with `git reset --hard HEAD~1`. How does this hook into the bash tool, and what's the rollback UX?

## Answer

1. **Hook point:** Inside `bash.Fn`'s `run` closure, after the `dangerousBashReason` check but before `runBashContext`. Run `git add -A && git commit -m "..."` via `exec.Command`. If it fails (no git, nothing to commit), continue silently — don't block the bash command.

2. **No git repo:** Skip silently. `git` exits non-zero, ignore it. Not every workspace is a git repo.

3. **Dirty working tree:** Snapshot everything — `git add -A` stages all changes including unrelated ones. The rollback (`git reset --hard HEAD~1`) will revert everything. This is a known tradeoff per DECISIONS.md.

4. **Rollback UX:** No new tool. The rollback is `git reset --hard HEAD~1` run via bash. If the agent needs to recover from a destructive bash command, it calls `bash git reset --hard HEAD~1`. Edge case (bash itself is broken) is out of scope for v1.

5. **Commit message format:** `pre-bash snapshot [session:<sessionID>] — <first 80 chars of command>`.

6. **Existing `snapshotIfMutate` stays.** The git commit is bash-specific. File snapshots for write_file/edit_file remain the separate mechanism they are today.

File list: `tools.go` (add git commit call in bash.Fn), `loop.go` (pass session ID into bash tool context).
