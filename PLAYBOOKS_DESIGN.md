# Mino v2 — Playbook Architecture

## What is a playbook?

A playbook is a repeatable procedure. One folder. Numbered markdown stages.
The filesystem is the executor. Mino's loop walks the stages.

A playbook answers **"how do I handle this?"** — the scheduler/project/checkpoint
tells you **when**, the playbook tells you **how**.

## Schema

```
~/.mino/playbooks/<name>/
├── config.md       # description, schedule, shared values (optional)
├── 01-<verb>.md    # stage 1
├── 02-<verb>.md    # stage 2
├── ...             # as many stages as needed
└── output/          # Mino creates, human reviews between runs
```

### `config.md` (optional)

```markdown
description: Weekly procurement audit — fetch pending POs, analyze supplier performance, draft report
schedule: Mon 09:00
status: active
Database: {{DB_PATH}}
Stale threshold: 7 days
Critical suppliers: UNIMED SDN BHD, PAHANG PHARMACY
```

`{{PLACEHOLDER}}` values resolve from SOUL.md or session context.

### Stage files: `NN-<verb>.md`

Every stage has exactly three sections. Order is fixed.

```markdown
# <Verb phrase — what this stage does>

## Read

- `config.md` (for X and Y)
- `output/<previous-stage-output>.md` (the output from stage N-1)

## Do

1. First concrete step
2. Second concrete step
3. Last step: write results

## Write

`output/<descriptive-name>.md`
```

**Rules:**
- `## Read` lists files. One per line.
- `## Do` is numbered steps. One action per step.
- `## Write` is exactly one file path under `output/`.
- Stage 1's `## Read` points to `config.md` or is empty.
- No stage file exceeds 80 lines. Split instead.
- No branching. No circular references. Linear only.

### `output/` folder

Mino creates on first run. After each execution, `output/` contains exactly one
file per stage. Stage N+1 reads Stage N's output. A human can open and edit
any file between stages — the next stage picks up the edit.

---

## Memory as Router

Vague prompts ("send me last week's data") route to the right playbook via memory:

1. Embed the prompt → cosine similarity against all playbook `description:` fields
2. FTS5 search for keywords across playbook `config.md` files
3. Recall past sessions where similar prompts resolved to a playbook

The LLM doesn't guess. Memory routes. LLM confirms and executes.

---

## What gets replaced

| Current Mino | Playbook equivalent |
|---|---|
| `scheduler.go` (241 lines) | `config.md` `schedule:` field + cron/systemd timer |
| `checkpoint.go` (167 lines) | Stage `output/` folder IS the checkpoint state |
| `artifacts.go` — SessionArtifact struct, named artifact tracking (89 lines) | Deleted. `output/` replaces the *named session artifact* pattern. But large-tool-output compaction (`compactToolOutput`) moves into loop.go — `/tmp/mino/results/` stays as the overflow bucket. |
| Projects table + tools (~120 lines) | Playbook folder IS the project |
| Schedule tools (~80 lines) | Creating a playbook folder IS scheduling |
| ToolFilter / embedding filter (~150 lines) | `## Read` lists exactly what to load |
| Approval tools / workspace gate (~150 lines) | Stage step: "Stop here. Ask Abah." |
| Completion protocol (~80 lines) | Stage completes when `output/` file exists |
| Dedup / no-progress / streaks (~120 lines) | Stages are linear, single-pass. No dedup needed. |
| File claim verification (~60 lines) | `## Write` says the path. Runner checks existence. |
| **Total replaced: ~1,300 lines** | |

## Two folders, two purposes

| Folder | Role | Lifecycle |
|---|---|---|
| `~/.mino/playbooks/<name>/output/` | Stage-to-stage handoffs. Human-readable, git-trackable, the truth. | Survives runs. Git committed. |
| `/tmp/mino/results/` | Large tool output overflow within a single stage. Prevents context blow-up. | Ephemeral. Auto-cleaned after 24h. |

When a stage's tool call returns 50KB of SQL results, the loop writes to
`/tmp/mino/results/` and tells the LLM "read the slice you need." The playbook
doesn't know in advance how big a query result will be — the overflow bucket
handles it generically.

## Mino's 4 pillars of context management

| Pillar | Fate |
|---|---|
| **Consolidation** (memory.go) | **Stays.** Distills chat logs into durable facts. Unchanged. |
| **Tool filter** (ToolFilter, SchemasFor, embedding index) | **Replaced** by `## Read`. A stage lists exactly which files to load. No embedding, no cosine similarity. |
| **History truncation** (session.go, MaxHistoryTurns, ContextMessages) | **Stays.** Keeps last N turns. Unchanged. |
| **Artifact compaction** (`compactToolOutput`, `/tmp/mino/results/`) | **Simplified.** `artifacts.go` (89 lines, SessionArtifact struct, named artifact catalog) is deleted. `compactToolOutput()` moves into loop.go. `/tmp/mino/results/` stays as the overflow bucket. |

## What stays

All infrastructure: tools (read, write, bash, search, fetch, calendar, etc.),
providers (Anthropic, OpenAI, Gemini), memory (SQLite + FTS5 + embeddings),
gateways (Telegram, dashboard), skills, MCP, auth, alerts, extensions.

## What simplifies

- `loop.go` (767 → ~100): No completion protocol, no dedup, no streaks. Just walk stages.
- `app.go` (292 → ~150): No checkpoint manager, no scheduler bypass.
- `session.go` (259 → ~180): No completion prompt, no pending approvals section.
