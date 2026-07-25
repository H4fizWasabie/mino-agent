# SQL gate

Status: resolved
Type: grilling
Blocked by: —

## Question

Destructive SQL patterns (DROP, TRUNCATE, DELETE, UPDATE without WHERE) must require approval before execution. How does the regex gate work, and how does it integrate with bash tool execution?

## Answer

1. **Patterns to add:** DELETE without WHERE, UPDATE without WHERE. Exclude ALTER TABLE (too common for legitimate migrations), CREATE, INSERT, GRANT/REVOKE. The existing `dangerousBashReason` already catches DROP and TRUNCATE on tables/databases.

2. **WHERE detection:** Simple regex — flag DELETE/UPDATE that lacks the literal string `WHERE` (case-insensitive). Pattern: `(?i)\b(delete\s+from|update\s+\w+\s+set)\b(?!.*\bwhere\b)`. Not perfect (false negative on `WHERE 1=1` is actually correct behavior — that is destructive), false positive unlikely.

3. **No config toggle.** No `MINO_SQL_GATE` env var. Users who need un-gated SQL use the existing `request_approval` flow to approve specific commands. One less config surface.

4. **Multi-statement:** Gate the whole command string. If any statement in `psql -c "DELETE FROM a; DROP TABLE b"` triggers, the whole command requires approval. No per-statement parsing.

5. **Integration:** Add patterns to the existing `destructiveBashPatterns` slice in `tools.go`. No new tool, no new flow — the bash tool's `dangerousBashReason` already wires to `request_approval`/`resolve_approval`. Reason text: `"destructive SQL without WHERE clause requires approval"`.

File list: `tools.go` (add 2 regex patterns to `destructiveBashPatterns`).
