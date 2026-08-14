## Question

Status: **RESOLVED** (historic — decided/shipped; predates the Status-line format)

Which tools belong in the essential set — always included for every turn? Current essentials: 15 tools. Many are never or rarely used. What should the new set be, and what's the evidence for each?

## Resolution

**New essentials (8):** `read_file`, `write_file`, `bash`, `search_web`, `remember`, `save_note`, `list_playbooks`, `run_playbook`

**Family expansion (+3):** `edit_file`, `fetch_url`, `schedule_playbook`

**Floor: 11 tools** (down from 17).

**Dropped:** `create_event`, `list_events`, `create_reminder`, `list_reminders`, `cancel_reminder`, `list_schedules`, `cancel_schedule` — all have clear trigger words for semantic matching.

## Context

Current `essentialToolNames` (tools.go:172-177):
```
remember, read_file, write_file, edit_file, save_note, search_web,
create_event, list_events, list_playbooks, run_playbook,
create_reminder, list_reminders, cancel_reminder,
list_schedules, cancel_schedule
```

VPS usage data (600 turns, July 20-28):

| Tool | Uses | In essentials? |
|------|------|----------------|
| bash | 530 | **No** — must add |
| read_file | 397 | Yes |
| search_web | 188 | Yes |
| write_file | 73 | Yes |
| save_note | 43 | Yes |
| remember | 32 | Yes |
| run_playbook | 25 | Yes |
| list_playbooks | 14 | Yes |
| fetch_url | 8 | No (family of search_web) |
| edit_file | 7 | Borderline |
| schedule_playbook | 6 | No (family of list/run playbooks) |
| list_events | 4 | Borderline |
| list_reminders | 4 | Borderline |
| list_schedules | 2 | Borderline |
| create_reminder | 1 | Drop |
| create_event | 0 | Drop |
| cancel_reminder | 0 | Drop |
| cancel_schedule | 0 | Drop |

Notes:
- `bash` is the #1 tool by far (530 uses) but NOT in essentials. It gets in via semantic matching. Should be essential.
- `create_event`, `cancel_reminder`, `cancel_schedule` have zero uses across 11 days. Pure waste.
- Family expansion pulls in `fetch_url` (from search_web) and `schedule_playbook` (from list/run playbook) — these are fine to keep via families.
