# Workspace boundary check

Status: resolved
Type: grilling
Blocked by: —

## Question

Mino needs a workspace boundary: any write, edit, or bash targeting a path outside the workspace (or Mino home) requires approval. What's the check, what counts as "outside," and how does it integrate with the existing `request_approval` flow?

## Answer

1. **Boundary:** `Settings.Workspace` + `~/.mino/`. Always allow writes to `~/.mino/rollback/` for git snapshots. Read-only tools (`read_file`) are not gated — the boundary is about writes only.

2. **Check placement:** `isUnderAllowedPath(path) bool` function. Plug into `write_file.Fn`, `edit_file.Fn`, and `sync_file.Fn`. Do NOT gate `bash` — bash is too hard to path-parse reliably, and git rollback (ticket 03) is the broader safety net for bash destruction.

3. **Approval caching:** Per-session. First out-of-bounds write triggers approval. Subsequent writes to the same directory tree (prefix match) skip the prompt. Cached in a session-scoped `map[string]bool`, not persisted.

4. **UX:** Auto-create approval. When blocked, the gate creates a `request_approval` entry automatically and returns the approval ID inline — one round trip instead of two (no need for LLM to call `request_approval` after being refused). The response format: `[APPROVAL_REQUIRED] /etc/nginx/sites-enabled is outside workspace. Approval saved as 'write-to-etc-nginx', wait for user approval then retry.`

File list to touch: `tools.go` (add `isUnderAllowedPath`, wrap write_file/edit_file/sync_file Fn), `config.go` (no changes — `Settings.Workspace` already exists).
