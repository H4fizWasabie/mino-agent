# Mino Dashboard Design

Status: agreed product direction; not yet an implementation specification.

## Purpose

The dashboard should present Mino as a responsible, auditable personal
operator. It should lead with changes in responsibility and outcome while
keeping runtime machinery available for inspection.

The selected visual direction is prototype **B — Operator Timeline**. Its
editorial hierarchy, chronological journal, compact navigation, and
non-dominant conversation composer form the redesign foundation.

## Information architecture

- **Today** answers: What changed, what matters now, and what needs the owner?
- **Work** answers: What has Mino accepted responsibility for?
- **Conversations** answers: What did the owner and Mino discuss?
- **Memory** exposes searchable retained knowledge.
- **System** exposes the graph, tools, database, traces, providers, files, and
  settings.

Today is global across Telegram, dashboard, schedules, and background
routines. Channel is secondary metadata, not a separate workspace.

## Today is a curated operator journal

Today is not a complete runtime activity log. An entry appears or changes only
when one of these meaningful events occurs:

- The owner must approve, clarify, or choose something.
- Mino accepts and starts meaningful responsibility.
- Status, deadline, risk, or expected outcome materially changes.
- Mino becomes blocked.
- An outcome is produced and verified.
- A relevant scheduled responsibility is approaching.
- Mino observes something important enough to affect an owner decision.

Individual tool calls, model iterations, recovered retries, memory
housekeeping, normal health checks, and repeated progress heartbeats remain
behind **Inspect evidence**.

Visible status language stays owner-oriented:

- **Needs you**
- **Working**
- **Waiting**
- **Blocked**
- **Verified**

Specific conditions such as scheduled, awaiting a reply, or retrying later
explain **Waiting** rather than becoming more primary statuses.

### Adaptive entry density

Journal entries use consistent anatomy but not uniform height. The space an
entry receives reflects how much owner attention it requires:

- **Needs you** and **Blocked** entries are expanded. They show the relevant
  context, next-action owner, and immediate actions.
- **Working** and **Waiting** entries use medium density. They show the latest
  meaningful state, start or update time, and one way to open the work.
- **Verified** entries are compact. They show the outcome and verification
  channel without repeating execution detail.
- Important observations use narrative density but do not present an owner
  action unless one is genuinely required.

The stable visual order is time, outcome-oriented title, owner-oriented status,
latest meaningful state, next action when relevant, minimal actions, and
expandable Evidence.

## Living entries, immutable history

One piece of work has one living Journal Entry. Its visible summary and
position may change when a meaningful event occurs; insignificant runtime
activity does not move it.

The underlying Responsibility History is append-only. Opening an entry shows:

- The accepted outcome and current owner
- The next action and who owns it
- Relevant deadline, schedule, and execution policy
- The verification condition
- Timestamped state transitions
- Evidence and produced artifacts
- The originating conversation

The runtime records state transitions, timestamps, and Evidence. The LLM may
write the concise human-readable summary, but it does not establish truth by
itself. **Verified** requires recorded proof; otherwise the entry remains
Working, Waiting, Blocked, or explicitly unverified.

## Calendar boundaries

Today follows Mino's configured local timezone.

- Verified outcomes remain on the date they completed.
- Working entries carry into the new day with their original start time.
- Needs-you and Blocked entries carry forward and display their age.
- Scheduled entries appear when they enter a useful upcoming window.
- Earlier days remain available through date navigation.

This preserves a daily narrative without allowing unresolved responsibility
to disappear at midnight.

## Work owns responsibility

Work is not another chronological feed. It is the canonical portfolio of open
Responsibility grouped by current owner-oriented status, with recently closed
items available for continuity.

Not every Journal Entry creates a Responsibility. A meaningful observation
may appear only in Today. Anything Mino claims to own across time must appear
in Work until it is truthfully closed.

A Responsibility records:

- Desired outcome
- Current owner
- Next action and next-action owner
- Deadline or schedule when relevant
- Verification condition
- Append-only Responsibility History

## Acceptance boundary

A conversation becomes a Responsibility when at least one of these is true:

- It cannot finish within the current turn.
- It must survive a restart or later conversation.
- It has a deadline, schedule, or recurrence.
- It depends on another person or external event.
- It pauses for an owner decision.
- Mino promises to monitor, follow up, or return later.
- Completion requires future verification.

Simple questions remain conversation turns. A recurring routine is one
standing Responsibility; each execution contributes journal and history events
instead of creating another Work item.

## Owner control

Entries offer a small, consistent action set:

- Open conversation
- Inspect evidence
- Pause or resume
- More

More may expose schedule changes, outcome changes, retry, stop, and complete
history. Read-only navigation is immediate. Safe reversible changes may execute
directly and must return verified state. Consequential actions return to
conversation for an explicit human checkpoint.

Buttons initiate understandable instructions; they never silently establish
success. The runtime performs the action, reads back the resulting state, and
records the receipt.

## Still to decide

- Date navigation and search behavior
- Work grouping, filtering, and recently-closed retention
- Mobile behavior for entry detail and conversation
- Migration from existing dashboard data to Responsibility and journal state
- Which existing runtime records can support the design without duplicating
  state
