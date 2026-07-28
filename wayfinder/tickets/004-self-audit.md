# Self-Audit System

## Question

Build persistent, queryable audit trail of Mino's own behavior. Abah can ask "what happened?" and Mino can explain.

What to build:
- Audit event schema (SQLite — reuse existing DB)
- What gets logged: tool calls, results, errors, loop iterations, interrupts, loop detections
- Query interface: Mino can `SELECT` its own audit log and explain events
- Retention policy

## Type

wayfinder:task

## Resolution

### What was built
- **`audit.go`** (~130 lines):
  - `audit_events` SQLite table (session_id, event_type, message, iteration, created_at)
  - `Core.auditLog()` — writes events from anywhere
  - `query_audit` tool — Mino can query its own audit log with filters (session, type, limit, since)
  - `pruneOldAuditEvents()` — 30-day retention
- **`db.go`**: `initAudit()` called from `Connect()`, schema version tracked
- **`app.go`**: `query_audit` registered as BehaviorObserve tool, audit callback wired into context, daily pruning goroutine
- **`loop.go`**: audit writes for loop_detected and LLM errors
- **`nerves.go`**: audit write for interrupts

### Design
- Audit events are separate from `tool_calls` table — covers loop events, interrupts, errors
- `query_audit` also surfaces recent tool errors from `tool_calls` table
- Context callback pattern (like snapshot updater) — no new function signatures needed
- Retention: 30 days, pruned daily

## Blocks

_none_
