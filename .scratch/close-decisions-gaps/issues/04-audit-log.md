# Immutable audit log

Status: resolved
Type: grilling
Blocked by: —

## Question

All tool calls and their outputs must be logged append-only to a separate file with restricted permissions. What's the format, where does it live, and how does it survive agent malfunction?

## Answer

1. **New file, not repurpose.** Add a second write in `Registry.ExecuteContext` (alongside the existing SQLite INSERT) to `~/.mino/audit.jsonl`. The SQLite `tool_calls` table stays for dashboard queries. Two writes per tool call is cheap.

2. **Format:** JSONL — one JSON object per line. Fields: `tool_name`, `args`, `output_summary` (first 200 chars), `session_id`, `status` (ok/error/approval_required), `approval_decision` (approved/rejected/none), `iteration`, `timestamp` (RFC3339).

3. **Permissions:** `0600` on file create. Opened with `O_APPEND|O_WRONLY|O_CREATE` — append-only at the OS level. Survives agent malfunction because writes are direct `os.File.Write` calls, not buffered through the agent's state.

4. **Rotation:** Never rotate. DECISIONS.md §14: backup is `cp -r ~/.mino`. If the file grows too large, the user handles it. No built-in rotation.

5. **Log call site:** Inside `Registry.ExecuteContext`, right after the existing SQLite INSERT. Same data, same place, just additionally flushed to the JSONL file.

File list: `tools.go` (add `auditLog *os.File` to Registry, open on init, write in ExecuteContext, close in Registry.Close if we add one).
