---
status: accepted
---

# Keep authoritative responsibility and journal state

Mino will persist Responsibility identity, append-only Responsibility Events,
and the resulting owner projection in one small authoritative module. Existing
traces, schedules, playbooks, conversations, artifacts, and receipts remain
evidence or execution systems; the dashboard will neither reconstruct
responsibility from them nor introduce a second workflow or agent loop.

At migration, current schedules become Routines and pending reminders become
Waiting Responsibilities. Historical conversations and traces remain
inspectable but are not retroactively interpreted as Responsibility History,
so the trustworthy journal begins at an explicit baseline instead of inventing
past ownership.
