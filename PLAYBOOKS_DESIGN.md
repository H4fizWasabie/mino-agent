# Mino — Playbook Architecture

## Purpose

A playbook is an optional filesystem state machine for a repeatable procedure.
It is not Mino's chat architecture and it does not replace normal reasoning.

Every user message enters Mino's normal runtime. A matched playbook is offered
as context, like a relevant skill or the `recall` tool. Mino decides whether the
procedure fits the current request and may call `run_playbook`. Questions,
follow-ups, and one-off actions continue normally when a playbook is unnecessary.

## Filesystem layout

```text
~/.mino/playbooks/<id>/
├── config.md       # description, status, schedule, and shared values
├── 01-<verb>.md    # stage 1
├── 02-<verb>.md    # stage 2
└── output/         # durable stage results
```

The folder name is the stable machine identifier. The description in
`config.md` explains the procedure to Mino and humans.

### `config.md`

```markdown
description: Fetch purchase orders, analyze suppliers, and draft a weekly audit
status: active
schedule: 09:00 Asia/Kuala_Lumpur
notify: true
Database: /srv/data/procurement.db
Stale threshold: 7 days
```

Shared values remain in the file. Stages list `config.md` under `## Read`, and
the model reads it through the normal file tool. The filesystem is the
resolver; there is no placeholder or template engine.

### Stage files

```markdown
# Fetch purchase orders

## Read

- `config.md`
- `output/previous.md`

## Do

1. Read the configured source.
2. Fetch the requested period.
3. Write the verified result.

## Tools

- read_file
- write_file
- bash

## Write

`output/purchase-orders.md`
```

- `## Read` names files the stage may need.
- `## Do` describes the procedure.
- `## Tools` optionally limits available capabilities; it does not prescribe
  tool order.
- `## Write` names exactly one expected output.
- Stages run in numeric order without a dependency graph or branching engine.

## One runtime

```text
user message
  → normal Mino context and reasoning
  → optional run_playbook tool call
  → numbered stage runner
  → canonical RunLoopContext for each stage
  → verified output files
```

There is no second agent or playbook-specific LLM loop. A stage uses the same
provider, reasoning setting, tool execution, artifact compaction, traces, and
session context as an ordinary Mino turn. Nested playbook suggestions are
disabled while a stage is running.

The runner gives each stage the original user request and its Markdown
instructions. A stage attempt has at most eight runtime iterations. If the
model stops without creating the declared output, the runner may retry the
stage up to three times with a correction. Within an attempt, the canonical
loop remains simple: call the model, execute its tools, return the observations,
and repeat until the model stops or the iteration limit is reached.

The runner snapshots the declared output before each attempt. A file created or
updated by that attempt is proof of stage completion even if the model reaches
its iteration limit after writing it. An unchanged file from an earlier run
never satisfies the current attempt. Cancellation and runtime errors remain
failures. Stages with external side effects must remain idempotent because a
process restart can re-enter an earlier stage.

Every completed stage adds its absolute output path to `PlaybookResult` and the
session artifact catalog. The `run_playbook` observation therefore gives later
conversation turns a concrete file to inspect instead of requiring the
procedure to run again.

Human review is part of the procedure, not a programmatic approval subsystem.
A stage that needs confirmation says `Stop here. Ask Abah.` The runner returns
`blocked`, and a later user message decides what happens next.

## Discovery and choice

`MatchPlaybook` currently works as follows:

1. Keyword overlap checks the folder identifier, description, and stage text.
2. If keyword matching finds nothing and embeddings are configured, semantic
   similarity checks descriptions.
3. A match at the hint threshold is added to Mino's system context as a
   possibly relevant procedure.
4. Mino decides whether to call `run_playbook` or handle the request normally.

A score never executes a playbook by itself. This preserves conversational
context: a follow-up about an existing result should be answered as a
follow-up, not automatically rerun as a procedure.

## Scheduling and delivery

Schedules are external orchestration, not a second task runtime:

- `schedule_playbook` writes schedule and notification values to `config.md`.
- `mino-playbook-dispatcher.timer` checks due configurations once per minute.
- `mino-playbook-runner` sends an explicit request to the running Mino service.
- The runner selects output created by that run and delivers it through
  Telegram when configured.
- `flock` prevents overlapping scheduled runs of the same playbook.

Systemd decides when to request execution. Mino still owns reasoning and stage
execution.

## Source map

- `app.go`: normal conversational entry point
- `session.go`: optional playbook candidate context
- `playbook.go`: parsing, matching, tools, and linear stage runner
- `loop.go`: canonical reasoning and tool runtime used by chat and stages
- `extensions/mino-playbook-*.sh`: external scheduling and delivery
