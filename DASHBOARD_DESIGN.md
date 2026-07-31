# Mino Dashboard Design

Status: agreed product direction; not yet an implementation specification.

## Purpose

The dashboard should present Mino as a responsible, auditable personal
operator. It should lead with changes in responsibility and outcome while
keeping runtime machinery available for inspection.

The selected visual direction is prototype **B — Operator Timeline**. Its
editorial hierarchy, chronological journal, compact navigation, and
non-dominant conversation composer form the redesign foundation.

## Visual language

B uses an editorial-operational typography system:

- Editorial serif for page titles and meaningful journal outcomes
- Neutral sans-serif for summaries, navigation, actions, and controls
- Restrained monospace only for timestamps, identifiers, and Evidence
  references

The palette stays quiet: a warm off-white background, near-black text, and
subtle rules provide most structure. Mino blue is reserved for Working state
and primary actions, amber for Needs you, red for genuine blocks or failures,
and green for Verified outcomes. Decorative card shadows and technical
monospace styling do not define the main interface.

### Desktop frame

Desktop uses a compact sticky top navigation:

**Mino**, **Today**, **Work**, **Conversations**, **Memory**, **System**, and a
truthful runtime-health indicator.

There is no permanent sidebar, command-centre heading, or chat dock. The
journal stays within one readable central column and wide-screen space remains
intentionally quiet.

System uses local tabs for Overview, Runtime, Tools, Database, Files, and
Settings. Memory uses local tabs for Search, Knowledge, Episodes, and Graph.
Provider and model identity belong in System rather than global chrome; a
provider problem appears in Today only when it materially affects
responsibility or outcome.

Desktop keeps a restrained, fixed, single-line conversation composer at the
bottom centre. It expands on focus or during conversation, replacing the
former permanent chat dock without turning Today into a chat page.

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

### Date navigation and journal search

The Today Journal presents one local calendar day at a time. Compact previous
and next-day controls sit around the current date; when the owner browses the
past, a direct **Today** action returns to the current day. Clicking the date
opens a calendar popover for distant navigation without turning Today into a
calendar dashboard.

Journal search works across days and groups results chronologically. It may
match Responsibility titles, journal summaries, owner-oriented statuses,
dates, Evidence labels, and channel metadata.

Journal search does not search general Memory or raw Conversations. Those
surfaces retain their own search boundaries so an audit query does not become
an unstructured global search.

## Work owns responsibility

Work is not another chronological feed. It is the canonical portfolio of open
Responsibility grouped by current owner-oriented status, with recently closed
items available for continuity.

Work uses three scopes:

- **Current** contains active one-off Responsibilities grouped vertically as
  Needs you, Blocked, Working, and Waiting.
- **Routines** contains standing recurring Responsibilities. Each row leads
  with its next run and most recent verified outcome.
- **Closed** contains the complete searchable Responsibility archive.

Work does not use a kanban board. The vertical portfolio preserves B's
editorial character and remains usable on narrow screens.

Current may include a compact **Recently verified** section for the previous
seven days. Older closed Responsibilities leave the default workspace but
remain accessible through Closed and journal search.

Filters remain limited to owner-oriented status, due or overdue state,
one-off or recurring kind, and outcome or Responsibility search. Channel is
not a primary filter because it is a communication path rather than an
ownership domain.

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

### Responsibility detail

Selecting a Journal Entry title or Work row opens a dedicated Responsibility
page. The dashboard does not use a modal or permanent side drawer for complete
Responsibility inspection.

The detail page uses:

- A main column for current state, next action, and immutable Responsibility
  History
- A narrower column for policy, schedule, Evidence summary, artifacts, and
  controls
- A contextual composer labelled with the current Responsibility

A small inline disclosure may reveal the latest Journal Entry summary, but
complete history and Evidence remain on the dedicated page. Browser back
navigation restores the prior journal date and scroll position.

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

## Mobile behavior

Mobile preserves the same Responsibility and journal model through
phone-specific navigation rather than shrinking the desktop layout.

The bottom navigation is **Today**, **Work**, **Inbox**, **Ask**, and **More**.
Inbox is the mobile label for Conversations; More contains Memory and System.
Only Needs-you items receive a navigation count badge.

Today is a single-column adaptive journal. Selecting an entry opens a
full-screen detail surface, not a side panel. Needs-you and Blocked details may
keep their primary action near the bottom while Responsibility History and
Evidence remain expandable.

Ask opens a full-screen composer rather than a persistent chat dock. From a
Responsibility detail it carries that Responsibility as visible context; from
global navigation it starts without a Work scope. Returning from conversation
restores the previous journal date and scroll position.

Mobile uses explicit controls. It does not use hidden swipe actions for state
changes.

## Authoritative responsibility state

Mino needs one small authoritative module for Responsibility state. Its narrow
interface records a Responsibility Event and reads the resulting owner view.
The implementation owns:

- Responsibility identity
- Append-only Responsibility History
- Current projection and valid status transitions
- Journal inclusion and carry-forward rules
- References to originating Conversations, Routines, traces, artifacts, and
  Evidence

Existing systems retain their current roles: playbooks execute procedures,
schedules trigger Routines, the canonical loop reasons and uses tools, traces
retain machinery detail, and receipts and artifacts retain Evidence. The
Responsibility module does not execute work or create another agent loop.

The dashboard reads the authoritative projection. It does not reconstruct
ownership from chat, traces, or tool calls.

### Migration boundary

At the first deployment of authoritative Responsibility state:

- Each current schedule becomes one Routine with its existing next-run and
  last-run information.
- Each pending reminder becomes a Waiting Responsibility.
- Historical Conversations and traces remain inspectable under their existing
  surfaces but are not inferred into past Responsibility History.
- The obsolete `projects` table is not imported.
- The Today Journal begins with a visible baseline event describing what was
  imported.

This deliberately avoids fabricated history. The journal becomes authoritative
from the migration point forward.

See [ADR 0001](docs/adr/0001-authoritative-responsibility-journal.md).
