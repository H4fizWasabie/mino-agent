# Dashboard Live State

## Question

Wire up dashboard (web UI + SSE) to show Mino's live nervous system state:
- Current loop status (idle / thinking / tool-running)
- Recent audit events (live stream)
- Loop detection alerts
- Health summary

Build on existing `dashboard.go`. SSE already exists — extend the events pushed.

## Type

wayfinder:task

## Resolution

### What was built
- **`/api/nerves?session_id=X`** endpoint — returns live snapshot JSON: `{active, iteration, status, current_tool, tool_history, elapsed}`. Returns `{active: false}` when no loop is running.
- **Dashboard event pushes**:
  - Interrupt events pushed to `dashEventQ` (type: "interrupt", query, reply)
  - Loop detection events pushed to `dashEventQ` (type: "loop_detected", message, iteration)
- Frontend can poll `/api/nerves` for live state or consume `/api/events` for alert stream

### Surface
- Backend only — frontend (static/index.html) is a separate concern
- SSE events already existed for turn_start/turn_end; nervous system events now added to the stream

## Blocks

_none_
