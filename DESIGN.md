---
name: Mino Nowfield
description: A full-width Past, Now, and Next field where responsibility, attention, and evidence remain connected.
colors:
  paper: "#f3f4f2"
  surface: "#ffffff"
  rule: "#d9dcde"
  rule-strong: "#bcc2c6"
  ink: "#171b1e"
  ink-muted: "#505960"
  ink-faint: "#737c82"
  indigo: "#1557d6"
  indigo-soft: "#e8efff"
  indigo-deep: "#0c43b3"
  verified: "#237d52"
  verified-soft: "#e7f4ec"
  blocked: "#c53a32"
  blocked-soft: "#fae9e7"
typography:
  headline:
    fontFamily: "-apple-system, BlinkMacSystemFont, Segoe UI, sans-serif"
    fontSize: "clamp(26px, 3vw, 38px)"
    fontWeight: 700
    lineHeight: 1
    letterSpacing: "-0.035em"
  title:
    fontFamily: "-apple-system, BlinkMacSystemFont, Segoe UI, sans-serif"
    fontSize: "14px"
    fontWeight: 700
    lineHeight: 1.3
    letterSpacing: "0"
  body:
    fontFamily: "-apple-system, BlinkMacSystemFont, Segoe UI, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.55
  label:
    fontFamily: "ui-monospace, SF Mono, Menlo, monospace"
    fontSize: "11px"
    fontWeight: 700
    lineHeight: 1.3
rounded:
  control: "8px"
  panel: "14px"
  pill: "99px"
spacing:
  xs: "8px"
  sm: "12px"
  md: "18px"
  lg: "24px"
  xl: "34px"
components:
  button-primary:
    backgroundColor: "{colors.indigo}"
    textColor: "#ffffff"
    rounded: "{rounded.control}"
    padding: "8px 12px"
    height: "38px"
  button-secondary:
    backgroundColor: "transparent"
    textColor: "{colors.indigo}"
    rounded: "{rounded.control}"
    padding: "8px 12px"
    height: "38px"
  field:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "10px 13px"
---

# Design System: Mino Nowfield

## Overview

**Creative North Star: "Responsibility has a position in time"**

Mino should feel like a calm operational field, not a stack of dashboard cards.
The owner arrives to understand what happened, what is true now, and what comes
next. Past, Now, and Next therefore remain visible as one horizontal
Responsibility thread, anchored by a stable ultramarine Now axis.

The system is deliberately restrained, but not passive. Semantic status color
appears where it changes the owner's decision; contextual Ask actions and
evidence disclosures provide depth only when requested. Runtime machinery stays
available under System without competing with the owner story.

**Key Characteristics:**

- Owner outcomes lead; implementation detail recedes.
- Direct sans-serif hierarchy keeps dense Responsibility lanes scannable.
- Thin rules and aligned time columns organize content without card grids.
- Status is stated in words and reinforced, never replaced, by color.
- Evidence and diagnostics are pull-based.

## Colors

The palette pairs crisp mineral neutrals with one measured ultramarine action color
and explicit semantic colors for verified, attention, and blocked states.

### Primary

- **Measured Indigo:** Used for the current route, links, explicit actions, and
  keyboard focus. Its restraint keeps every appearance meaningful.

### Neutral

- **Mineral Field:** The quiet application surround.
- **White Surface:** The Responsibility field, controls, and diagnostics.
- **Graphite Ink:** Primary reading and outcome text.
- **Quiet Ink:** Supporting explanations and metadata.
- **Hairline Rule:** Separates journal entries and regions without boxing them.

### Named Rules

**The Earned Color Rule.** Accent and semantic color must communicate action or
state; never use them as ambient decoration.

**The Truth Before Reassurance Rule.** Green is reserved for evidence-backed,
fresh operational or verified state. Stale or failed reads revoke it.

## Typography

**Display Font:** system sans-serif

**Body Font:** system sans-serif
**Label/Mono Font:** system monospace

**Character:** Direct sans-serif type keeps the time field compact and legible,
while monospace is reserved for evidence, timestamps, and machine identity.

### Hierarchy

- **Headline:** Responsive bold sans-serif for the current owner surface.
- **Title:** Bold sans-serif for Responsibility names and meaningful results.
- **Body:** Regular system sans-serif with a comfortable reading measure of
  roughly 66–72 characters.
- **Label:** Compact, bold monospace for status, freshness, and evidence metadata.

### Named Rules

**The Time Label Rule.** Monospace belongs to timestamps, status, and evidence
metadata, never to narrative outcomes or controls.

## Layout

The desktop shell uses a compact sticky header and a full-width Work and Today
field. Past, Now, and Next use a 32/36/32 split; the vertical Now rule stays
stable while Responsibility lanes scroll. Memory and System retain bounded
reading widths where their diagnostic material benefits from them. The
collapsed composer is a 64px shell row, not an overlay. Opening it reflows the
field into a roughly 55/45 vertical split; conversation receives 72% of the
workbench and Evidence, Actions, and Links receive 28%.

Below 720px, each horizontal thread becomes one ordered Past, Now, Next stack,
with every section labelled in place. The header reduces to identity and health,
and a six-destination bottom navigation provides Today, Work, Inbox, Memory,
Ask, and More. Touch actions are at least 44px high.

## Elevation & Depth

The system is flat by default. Boundaries come from paper tone and hairline
rules. Shadows are reserved for temporary popovers such as health; the
conversation workbench remains part of shell layout at every desktop size.

### Named Rules

**The Flat Journal Rule.** Journal entries never become floating cards at rest.

## Shapes

Controls use gently rounded corners; temporary panels use a slightly larger
curve; status labels are pills. Journal entries themselves remain rectangular
and are shaped by horizontal rules, preserving the field-journal rhythm.

## Components

### Buttons

- **Shape:** Compact rounded controls using the control radius.
- **Primary:** Indigo fill with white text for the single strongest action.
- **Hover / Focus:** Deepen the fill on hover and expose a visible indigo focus
  outline. Motion is short and disabled when reduced motion is requested.
- **Secondary:** Transparent with an indigo border and text.

### Chips

- **Style:** Transparent, outlined, compact monospace labels.
- **State:** Text always names the state; semantic color reinforces it.

### Cards / Containers

- **Corner Style:** Panels use the panel radius; journal rows do not.
- **Background:** Raised paper for popovers and diagnostic groups.
- **Shadow Strategy:** Only floating layers receive shadow.
- **Border:** One hairline rule.
- **Internal Padding:** Usually medium to large spacing.

### Inputs / Fields

- **Style:** Warm paper fill, hairline border, and a compact radius.
- **Focus:** Indigo border plus a soft, visible focus halo.

### Navigation

Desktop navigation is one short row of owner concepts. Active destinations use
a soft indigo field; counts appear only when actionable. Mobile navigation is a
fixed six-item bar with authored line icons, labels, and the same active color.

### Responsibility Field

Each lane connects the latest meaningful Past event summary to current state and
the real next action. Needs-you and blocked lanes sort first. Its title opens a
full-width focus state with the same time grammar above immutable history and
policy evidence.

### Conversation Workbench

The desktop workbench opens from the bottom shell row and never covers the
surface above it. Its height is pointer- and keyboard-resizable, restorable, and
maximizable. The multiline composer begins at roughly five lines, grows to a
180px cap, and uses Ctrl/Command+Enter to send; Enter remains a newline so long
messages can be reviewed without accidental submission. Mobile uses a dedicated
full-height conversation state and hides the contextual side strip.

## Do's and Don'ts

### Do:

- **Do** lead every primary surface with an owner outcome or decision.
- **Do** derive health, freshness, and counts from current backend state.
- **Do** keep evidence available behind clear disclosure controls.
- **Do** preserve semantic labels and visible keyboard focus.
- **Do** adapt navigation and Ask behavior specifically for phones.

### Don't:

- **Don't** promote runtime architecture into primary navigation.
- **Don't** fabricate activity, proof, health, or reassuring empty states.
- **Don't** turn the journal into a grid of interchangeable metric cards.
- **Don't** use color, tiny glyphs, or motion as the only status signal.
- **Don't** center Work inside a narrow reading column or disconnect current
  state from its Past evidence and Next action.
