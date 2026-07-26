# Mino v2 — Playbook Architecture

## What is a playbook?

A playbook is a repeatable procedure. One folder. Numbered markdown stages.
The filesystem is the executor. A runner walks the stages; each stage has
a mini-loop with up to 3 retries.

```
~/.mino/playbooks/<name>/
├── config.md       # description, shared values (optional)
├── 01-<verb>.md    # stage 1
├── 02-<verb>.md    # stage 2
└── output/          # Mino creates, human reviews between runs
```

## Schema

### `config.md` (optional)

```markdown
description: Weekly procurement audit — fetch POs, analyze suppliers, draft report
status: active
Database: /srv/data/procurement.db
Stale threshold: 7 days
```

`description:` is what memory indexes for routing.
Shared values stay in `config.md`; the stage LLM reads the file through the
paths listed in `## Read`. The filesystem is the resolver—there is no template
engine or placeholder substitution.

### Stage files: `NN-<verb>.md`

Every stage has three sections:

```markdown
# <Verb phrase>

## Read

- `config.md` (for threshold)
- `output/<previous>.md` (from stage N-1)

## Do

1. Concrete step
2. Another step
3. Write results

## Write

`output/<name>.md`
```

**Rules:**
- `## Read` lists files to load before executing
- `## Do` is numbered steps
- `## Write` is exactly one output path
- No stage exceeds 80 lines
- Linear only — no branching

## Two loops

| Loop | Scope | Max tries | When |
|---|---|---|---|
| **Playbook runner** | Walks numbered stages, checks output exists | — | Structured tasks |
| **Mini-loop** | Executes tools within one stage | 3 retries | Per stage |
| **Main loop** | observe → act → observe | maxIter | Ad-hoc queries |

The main loop (ad-hoc) is simple: LLM calls tools, tools run, results fed back.
No completion protocol, no dedup, no streaks. The LLM stops when it has nothing
more to say.

## Memory as Router

Vague prompts ("send me last week's purchase data") match playbooks:

1. **Keyword** (always, free): word overlap between prompt and playbook
   description + stage content → score 0.0–1.0
2. **Embedding** (if configured and keyword matching finds nothing): cosine similarity on description → fallback

| Score | Behavior |
|---|---|
| ≥ 0.5 | Auto-run playbook, bypass LLM entirely |
| 0.3–0.49 | Hint injected into system prompt, LLM decides |
| < 0.3 | Normal flow |

## What was removed

| Removed | Lines | Replaced by |
|---|---|---|
| `scheduler.go` | 241 | Systemd timer or ad-hoc invocation |
| `checkpoint.go` | 167 | Stage `output/` folder IS the checkpoint |
| `artifacts.go` | 89 | Moved compaction to loop.go; `/tmp/mino/results/` stays |
| `delegate.go` (fan_out + delegate) | 133 | Playbooks handle multi-step tasks |
| Projects table + tools | ~120 | Playbook folder IS the project |
| Schedule tools | ~80 | Creating a playbook = scheduling |
| ToolFilter + SchemasFor | ~150 | `## Read` lists exactly what to load |
| Workspace gate | ~150 | Playbook stages and explicit approval tools handle guarded actions |
| Completion protocol + dedup + streaks | ~560 | LLM stops when done; output file = proof |
| `eval_test.go` (old loop tests) | 1086 | Removed; core tests preserved |
| **Total removed** | **~2,700** | |

## What stayed

| Component | Lines |
|---|---|
| `loop.go` (simplified) | ~300 (was 767) |
| `tools.go` (core tools only) | ~1,600 (was 2,291) |
| `playbook.go` (new) | ~500 |
| Session, memory, skills | Unchanged |
| Providers (Anthropic, OpenAI, Gemini, Mimo) | Unchanged |
| Gateways (Telegram, dashboard) | Unchanged |
| MCP, auth, alerts, extensions | Unchanged |

## State

- **Branch**: `feat/playbooks`
- **Deployed**: VPS `100.101.53.98`, mimo-v2.5
- **Runtime model**: playbook stages execute directly from the filesystem
- **Live playbook**: `procurement-audit` — auto-routes from "send me purchase data"
