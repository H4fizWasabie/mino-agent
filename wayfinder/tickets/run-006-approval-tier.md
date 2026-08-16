# Runtime Self-Management — Approval tier

Status: **RESOLVED** (wayfinder ticket, RUN-006 — GitHub issue #220)

Resolved 2026-08-16: `approval.go` — the approval tier for operations
outside the privilege whitelist. Shape decided on #220 (2026-08-16): a
`request_approval` harness tool the model calls — NOT an auto-staged
queue. The docs' "no request_approval/resolve_approval protocol"
(decisions.md §8, roadmap) governs playbooks as autonomous contracts and
shaped this: no resolve tool, no persisted approval state machine, the
approval is a conversational checkpoint. The tool journals the intent
FIRST (`approval.request` op, entity = the exact command, before = target
snapshot — RUN-002 discipline), registers it pending (in-memory only:
after a restart the id is unresolvable, so a crash-orphaned request can
never execute; boot reconciliation marks such entries rolled_back),
and pages the owner through the existing outbox (telegram.go — the same
path send_message uses; no second bot, no new UI). The owner replies
"approve <id>" / "deny <id>" in any existing chat (Telegram, dashboard
chat + stream, CLI); the harness intercepts the reply BEFORE the loop at
all three entry points and executes mechanically — the model can never
approve its own op. Approved execution journals its own op
(`approval.exec`, before/after = target snapshots, undo_of = the
request); denial and timeout mark the request rolled_back. The pager is
assumed fallible (map precondition): a failed page is loud, and every
unanswered request times out into a deny (MINO_APPROVAL_TIMEOUT_MINUTES,
default 30) — the flow can never deadlock.

## Question

The whitelist (RUN-003) is the autonomous/approval boundary — but outside
it the model had no path but a hard refusal. Destructive/risky
operations need the map's promise: stage, page the owner, execute only
on explicit approval — never hanging on an unreachable owner.

## Decisions so far

- **Whitelist IS the classifier — no LLM classifier**: whitelisted ops
  run autonomously through the host tools; everything else is a candidate
  for `request_approval`. The ticket's "classifier for destructive/risky
  ops" is mechanical given the RUN-003 boundary.
- **Approval grants NO root**: the staged command runs as the Mino user
  via argv exec (no shell, no quotes — the exact op the owner approves
  is exactly what runs; no shell operators, no sudo — same tripwire
  family as the bash tool). The sudoers whitelist remains the only root
  transport and is never extended by approval — the fog escalation
  ("owner extends the whitelist") stays out of scope; RUN-003's refusal
  message now says so for privileged whitelist-misses.
- **Journal lifecycle**: approval.request (ok) → approve → approval.exec
  (ok/failed, undo_of = request); deny/timeout → request rolled_back via
  the RUN-001/002 status seam; boot reconciliation marks never-decided
  requests rolled_back (no exec child).
- **Fallible pager**: page = outbox draft (queueOutbox + existence
  verification); failure is loud and never fatal — the timeout denies.
  The model's own turn also carries the request id, so the owner can
  answer even when the page file never lands.
- **Bounded**: no whitelist extension, no new operation tools, no LLM
  classifier — the approval flow only.

## Out of scope

- Whitelist escalation for privileged ops (map fog — owner extends
  /etc/sudoers.d/mino; the refusal message names the path)
- Approval for playbook stages (playbooks remain autonomous contracts,
  decisions.md §8)
- New paging channels or UI (outbox + existing chat entry points only)
