# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Mino serves one owner per installation. The owner uses Telegram as the primary
interface and the private dashboard to understand what Mino owns, what changed,
what needs attention, and what evidence supports an outcome.

## Product Purpose

Mino is a responsible personal AI operator. It interprets requests with an LLM
while its runtime owns state, scheduling, permissions, recovery, verification,
and truthful completion. Success means the owner can quickly act on important
changes and trust that claimed outcomes are backed by current runtime evidence.

## Positioning

Mino turns agent activity into durable Responsibility: an accepted outcome,
current owner, next action, verification condition, append-only history, and
inspectable Evidence. The dashboard presents that owner truth without making
raw model or tool activity the product.

## Operating Context

Mino runs as a single Go binary on a Linux VPS. Telegram, scheduled Routines,
playbooks, dashboard Conversations, memory, tools, and extensions share one
canonical runtime. The dashboard is accessed through a trusted private network
and must remain useful on desktop and mobile.

## Capabilities and Constraints

- Vanilla JavaScript, CSS, and embedded HTML; no frontend framework.
- Preserve the single-binary build and existing backend routes.
- Preserve legacy dashboard hashes while consolidating their presentation.
- SQLite owns operational state; Markdown graph memory owns semantic claims.
- Dashboard state must come from real backend data and must expose staleness.
- Telegram remains primary; dashboard conversation is contextual and secondary.
- Runtime diagrams, traces, database access, tools, files, providers, and
  settings remain available under System rather than primary navigation.

## Brand Commitments

The product name is Mino. Existing logo assets under `static/assets/` remain
authoritative. Voice is concise, calm, owner-oriented, and explicit about
uncertainty, evidence, and recovery. The accepted dashboard direction is
documented in `dashboard-design.md` as Nowfield.

## Evidence on Hand

Real dashboard data comes from `/api/data`, `/api/responsibilities`, trace and
usage files, the configured memories directory, and SQLite state. Production
must not fabricate Responsibilities, metrics, health events, alerts, or proof.

## Product Principles

1. Owner outcomes before runtime machinery.
2. Truthful current state before reassuring decoration.
3. One living Responsibility with immutable history.
4. Progressive disclosure for evidence and diagnostics.
5. Preserve capability while simplifying navigation.

## Accessibility & Inclusion

Primary dashboard workflows must work with keyboard navigation, visible focus,
semantic controls, accessible names, non-color status labels, reduced motion,
and responsive layouts at phone and desktop widths.
