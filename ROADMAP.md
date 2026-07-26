# Mino Roadmap

> Living discussion document. This roadmap describes direction and priorities;
> it is not an implementation plan until a phase is explicitly approved.

## Vision

Mino should grow from a tool-using chatbot into a personal operating system:
an owner-only, configurable digital operator that remembers a person's
commitments, monitors relevant systems, carries work across time, performs
routine actions, and asks for approval at consequential boundaries.

Mino should serve different people and working styles without becoming a
multi-tenant platform. The product provides general capabilities and trust
boundaries; each owner supplies their own services, priorities, domains, and
operating policy.

The goal is not to add the most tools. The goal is to increase the amount of
useful responsibility Mino can handle while keeping behavior observable,
recoverable, and under the user's control.

## Current baseline

Mino already provides the core needed for this direction:

- Persistent memory and session context
- Telegram and dashboard interfaces
- Scheduling and task survival across restarts
- MCP and HTTP extension boundaries
- Database access through installed command-line clients
- Approval gates, action receipts, audit traces, and verification rules
- Multi-agent delegation and tool filtering

The roadmap should extend these foundations instead of turning the core into a
large workflow platform.

## Guiding principles

1. **Responsibility before capability.** Every new capability must have a clear
   owner, boundary, failure mode, and verification path.
2. **Automatic for low-risk work.** Reading, summarizing, organizing, drafting,
   and monitoring may run automatically.
3. **Approval for consequential work.** Sending, deleting, publishing,
   recording external changes, and financial, legal, medical, or irreversible
   actions require approval.
4. **Evidence over narration.** Mino should verify that an action happened and
   report the result, not merely claim intent.
5. **Extensions before core growth.** Domain integrations belong in MCP or
   separate HTTP services when they do not belong in the runtime loop.
6. **Single-user focus.** Mino is a personal operator, not a multi-tenant
   enterprise platform.
7. **Final-result-first UX.** Intermediate tool activity should remain
   inspectable without overwhelming the user; completed tasks should lead with
   the useful final result.

## Phases

### Phase 0 — Trust and foundations

Status: **Baseline / ongoing**

Make Mino safe and dependable enough to operate continuously.

- Owner-only identity and access boundaries
- Persistent memory, project context, and task survival
- Telegram, dashboard, scheduler, MCP, and extension runtime
- Approval gates for risky actions
- Action receipts, audit traces, and truthful verification
- A personal operating policy defining automatic versus approval-gated work
- Recovery behavior for cancellation, restart, failed tools, and partial work

Exit condition: Mino can complete ordinary tasks, survive interruptions, and
clearly distinguish completed, blocked, and unverified outcomes.

### Phase 1 — Personal awareness

Status: **Proposed**

Make Mino aware of what is happening across the user's digital life.

- Unified inbox across Telegram, email, and connected services
- Daily and weekly briefings
- An answer to “What needs my attention?”
- Detection of unanswered messages and overdue follow-ups
- Tracking commitments such as “I will get back to them”
- Important-change detection across databases, files, and services
- Concise summaries with links or evidence back to the source

Exit condition: Mino can identify relevant changes and pending commitments
without requiring a manually written prompt for every check.

### Phase 2 — Persistent objectives

Status: **Proposed**

Stop treating every request as an isolated conversation.

- Long-running objectives with status, deadlines, blockers, and next actions
- Continuation across conversations, gateways, and restarts
- Scheduled check-ins and follow-up windows
- Automatic progress verification
- Escalation only when blocked or approval is required
- Final-result-only reporting for completed work
- A clear distinction between objective state and conversational history

Example:

> Help me manage this customer follow-up project.

Mino should be able to track contacts, messages, deadlines, decisions, and
outcomes over time, regardless of the owner's particular business or project.

Exit condition: Mino can maintain an objective over multiple sessions and
return to it without losing the relevant context or next action.

### Phase 3 — Domain operators

Status: **Proposed**

Add focused capabilities around real areas of the user's life. Prefer separate
connectors and extensions over special cases in the core.

Candidate domains:

- Business and customer follow-up
- Email and Telegram communication
- Documents, quotations, invoices, and sample records
- External database reporting and reconciliation
- VPS and service operations
- Calendar, travel, and appointment coordination

Each domain should define its own data sources, allowed actions, approval
boundaries, and evidence requirements.

Exit condition: at least one domain can be operated end-to-end for a real
workflow, from incoming information to verified next action.

### Phase 4 — Controlled execution

Status: **Proposed**

Allow Mino to perform routine work proactively while preserving control at the
final consequential step.

- Automatic reply drafting
- Report preparation and database update proposals
- File and record organization
- Anomaly detection
- Recurring checks and scheduled routines
- Approval requests that appear only at the final consequential action
- Post-action verification and receipts
- Retry and recovery for known transient failures

Example:

> Every Friday, check outstanding customer follow-ups, draft replies, and show
> me only the ones needing approval.

Exit condition: a recurring workflow can run unattended through observation,
planning, drafting, approval, execution, and verification.

### Phase 5 — Personal chief of staff

Status: **Long-term direction**

Make Mino a coherent interface for managing the user's digital life.

- Understand the user's priorities and working style
- Identify slipping commitments and neglected objectives
- Connect information across tools and domains
- Maintain project continuity without repeated explanation
- Suggest the next best action with reasons and evidence
- Handle routine digital work within defined trust boundaries
- Escalate ambiguity, risk, and sensitive decisions
- Produce concise reports backed by verifiable state

Exit condition: Mino reliably reduces the user's coordination and follow-up
load without becoming an opaque autonomous actor.

## First candidate milestone

The first serious operator capability should likely be general commitment
management:

> Mino tracks every personal commitment, follows up at the right time,
> gathers the relevant context, drafts the next action, and asks for approval
> only when something consequential must be sent or changed.

This builds directly on Mino's existing reminder, scheduler, memory, Telegram,
and file capabilities.

## Deliberate non-goals

Unless later discussion produces a demonstrated need, Mino should not become:

- A multi-user enterprise platform
- A plugin marketplace
- A giant workflow framework
- An autonomous sender, deleter, publisher, or financial executor
- A system that claims success without evidence
- A domain-specific monolith when an extension can own the behavior

## Open discussion decisions

These questions should be answered before implementation work begins:

1. Which first operator domain should Mino support generally: work and business
   workflows, VPS/system operations, personal communications, or another
   repeatable domain?
2. Which sources should Mino observe first: Telegram, email, databases, files,
   calendar, or VPS services?
3. What may Mino do automatically for that domain?
4. Which actions always require explicit approval?
5. What evidence must Mino retain before reporting success?
6. What is the smallest real workflow that would prove the phase valuable?

## Discussion log

### 2026-07-26

- Framed Mino as a personal operating system rather than only a chatbot.
- Proposed a progression from trust foundations, to awareness, persistent
  objectives, domain operators, controlled execution, and chief-of-staff
  behavior.
- Identified commitment management as the first candidate milestone.
- Clarified that Mino is a general product for different owners, not a product
  tailored to one user's business or the example supplier conversation.
- No implementation authorized by this document.
