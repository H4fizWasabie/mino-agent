# Mino Dashboard Design

Status: agreed product direction and implementation contract.

## Purpose

The dashboard should present Mino as a responsible, auditable personal
operator. It should lead with changes in responsibility and outcome while
keeping runtime machinery available for inspection.

The selected visual direction is **Nowfield**. Its stable Past / Now / Next
geometry, compact navigation, full-width Responsibility lanes, and reserved
conversation-workbench region form the redesign foundation. The workbench is a
separate implementation issue.

## Visual language

Nowfield uses a direct operational typography system:

- Strong system sans-serif for page titles and Responsibility names
- Neutral system sans-serif for summaries, navigation, actions, and controls
- Restrained monospace only for timestamps, identifiers, and Evidence
  references

The palette stays quiet: a crisp mineral background, white surfaces, near-black
text, and subtle rules provide most structure. Mino blue anchors the Now axis
and primary actions, amber for Needs you, red for genuine blocks or failures,
and green for Verified outcomes. Decorative card shadows and technical
monospace styling do not define the main interface.

### Desktop frame

Desktop uses a compact sticky top navigation:

**Mino**, **Today**, **Work**, **Conversations**, **Memory**, **System**, and a
truthful runtime-health indicator.

There is no permanent sidebar or command-centre heading. Today and Work use the
available viewport as one ruled time field; wide-screen space carries Past and
Next context instead of becoming empty margins.

System uses local tabs for Overview, Runtime, Tools, Database, Files, and
Settings. Memory uses local tabs for Search, Knowledge, Episodes, and Graph.
Provider and model identity belong in System rather than global chrome; a
provider problem appears in Today only when it materially affects
responsibility or outcome.

The lower region is the conversation workbench. Its collapsed composer occupies
a shell row; opening it reflows the time field to roughly 55% height and gives
conversation 72% of the lower width. Evidence, Actions, and Links share the
remaining 28%. The workbench is pointer- and keyboard-resizable, collapsible,
restorable, and maximizable.

### Truthful health

The global health indicator reports runtime health rather than owner workload:

- **Operational** means essential runtime surfaces are healthy.
- **Degraded** means a surface is impaired but fallback or recovery keeps
  Responsibility moving.
- **Attention** means a system problem has blocked or jeopardized a
  Responsibility.
- **Connection lost** means the dashboard cannot obtain current state.

A Needs-you Responsibility does not make Mino unhealthy. When degradation
materially changes a Responsibility, the runtime also creates or updates the
relevant Today entry.

Opening health shows core uptime, database integrity and backup state,
provider and fallback availability, scheduler heartbeat, Telegram gateway,
extensions and MCP connections, and data freshness. The header shows the last
successful refresh and never continues displaying Operational when its state
is stale or unreachable.

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

## Work owns responsibility in time

Work is the canonical portfolio of Responsibility expressed as horizontal
threads. Past records the latest meaningful event, Now names current truth, and
Next shows the recorded next action, owner, schedule, or deadline.

Work does not use a kanban board or generic cards. Needs you and Blocked sort
first, followed by active and closed states; recency resolves order within a
state. Search and status filtering operate on the same real Responsibility
payload. On narrow screens, every lane becomes a labelled Past / Now / Next
stack rather than squeezing the horizontal geometry.

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

Selecting a Responsibility title in Today or Work opens a dedicated full-width
focus. The dashboard does not use a modal or permanent side drawer.

The focus uses:

- A Past / Now / Next axis for the latest event summary, current outcome, and next action
- A main history region for immutable Responsibility events and evidence
- A narrower policy region for kind, schedule, deadline, last run, and
  verification condition

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

The bottom navigation is **Today**, **Work**, **Inbox**, **Memory**, **Ask**, and
**More**. Inbox is the mobile label for Conversations; More opens System. Only
Needs-you items receive a navigation count badge.

Today is a single-column adaptive journal. Selecting an entry opens a
full-screen detail surface, not a side panel. Needs-you and Blocked details may
keep their primary action near the bottom while Responsibility History and
Evidence remain expandable.

Ask opens a full-screen workbench rather than a cramped split layout. From a
Responsibility detail it carries that Responsibility as visible context; from
global navigation it starts without a Work scope. Returning from conversation
restores the previous journal date and scroll position.

Mobile uses explicit controls. It does not use hidden swipe actions for state
changes.

## Truthful empty states

Production never uses sample Responsibilities, fake journal activity,
placeholder metrics, invented alerts, or synthetic health events.

For a new owner, Today states that nothing needs attention and Mino has not
accepted any Responsibilities. It may offer clearly labelled example prompts
that distinguish asking a question, assigning Responsibility, and creating a
Routine.

A quiet day with existing Work says that nothing has materially changed,
summarizes how many Responsibilities are proceeding normally, and identifies
the next Routine. This is distinct from Mino owning nothing.

Empty Work explains that a request appears there when Mino must carry an
outcome across time, Conversations, dependencies, or future verification.

After Responsibility migration, the first real Journal Entry is the visible
baseline describing imported Routines and pending reminders and pointing to
historical traces under System.

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

## Conversations

Conversations is one owner-wide library of truthful channel-specific threads,
not a fabricated unified transcript. Telegram and dashboard threads share
Mino's runtime and may link to the same Responsibility, but retain their actual
session and channel identity.

Desktop may use a two-pane thread list and selected transcript within the
Conversations page only. Dashboard threads are interactive. Telegram threads
are initially viewable communication history with **Open related
Responsibility** and **Continue in Telegram** actions; the dashboard does not
claim it can deliver a Telegram reply until explicit outbound routing and
delivery verification exist.

A Responsibility composer creates or continues a dashboard Conversation
visibly linked to that Responsibility. Global Ask starts or continues a
dashboard Conversation without silently inheriting an unrelated Telegram
session. Tool activity stays collapsed and final replies lead.

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

## Delivery sequence

Implementation proceeds through ordered, separately reviewable milestones:

1. **Responsibility foundation** adds the SQLite migration, authoritative
   module, append-only Responsibility Events, current projections, Routine and
   reminder import, migration baseline, and behavioral tests without removing
   existing dashboard behavior.
2. **Truthful Routine slice** proves one scheduled playbook from imported
   Routine through Working, completion or block, linked Evidence, Today, Work,
   and dedicated detail history.
3. **One-off Responsibility** adds the conversational acceptance boundary,
   owner-oriented transitions, carry-forward, controls, and contextual
   Conversation.
4. **Complete Nowfield information architecture** installs the compact frame,
   Conversations library, Memory and System regrouping, truthful health, and
   retires the obsolete Overview, Gateway, Loop, and Active Tasks
   destinations.
5. **Mobile and refinement** completes phone-specific navigation and detail,
   date navigation, journal search, accessibility, keyboard behavior, loading,
   stale-data handling, and empty-state polish.

The truthful Routine slice is the first user-visible proof. The redesign is
not considered working until a real scheduled Routine produces a living
Journal Entry with linked Evidence.
