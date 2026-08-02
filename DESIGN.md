---
name: Mino Operator Timeline
description: A quiet owner journal where outcomes, attention, and evidence outrank machinery.
colors:
  paper: "#e7e5de"
  surface: "#f0eee8"
  rule: "#c9c8c1"
  rule-strong: "#babbb6"
  ink: "#26313a"
  ink-muted: "#5f6870"
  ink-faint: "#7b8388"
  indigo: "#5264b8"
  indigo-soft: "#dde2f5"
  indigo-deep: "#3f509e"
  verified: "#21845b"
  verified-soft: "#e8f6ef"
  blocked: "#c0392b"
  blocked-soft: "#faeceb"
typography:
  headline:
    fontFamily: "Iowan Old Style, Palatino Linotype, Palatino, Georgia, serif"
    fontSize: "clamp(31px, 4.2vw, 52px)"
    fontWeight: 600
    lineHeight: 1.04
    letterSpacing: "-0.025em"
  title:
    fontFamily: "Iowan Old Style, Palatino Linotype, Palatino, Georgia, serif"
    fontSize: "18px"
    fontWeight: 600
    lineHeight: 1.25
    letterSpacing: "-0.012em"
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

# Design System: Mino Operator Timeline

## Overview

**Creative North Star: "The Operator's Field Journal"**

Mino should feel like a calm, precise journal kept by a capable operator. The
owner arrives to understand what changed, what needs a decision, and what proof
exists. The interface therefore uses quiet paper surfaces, ink-led hierarchy,
editorial outcome typography, and thin rules instead of command-center theater.

The system is deliberately restrained, but not passive. Semantic status color
appears where it changes the owner's decision; contextual Ask actions and
evidence disclosures provide depth only when requested. Runtime machinery stays
available under System without competing with the owner story.

**Key Characteristics:**

- Owner outcomes lead; implementation detail recedes.
- Editorial headlines sit above practical sans-serif body copy.
- Thin rules and tonal layers organize content without card grids.
- Status is stated in words and reinforced, never replaced, by color.
- Evidence and diagnostics are pull-based.

## Colors

The palette pairs warm mineral neutrals with one measured indigo action color
and explicit semantic colors for verified, attention, and blocked states.

### Primary

- **Measured Indigo:** Used for the current route, links, explicit actions, and
  keyboard focus. Its restraint keeps every appearance meaningful.

### Neutral

- **Warm Paper:** The uninterrupted application field.
- **Raised Paper:** Controls, popovers, and contained diagnostic surfaces.
- **Graphite Ink:** Primary reading and outcome text.
- **Quiet Ink:** Supporting explanations and metadata.
- **Hairline Rule:** Separates journal entries and regions without boxing them.

### Named Rules

**The Earned Color Rule.** Accent and semantic color must communicate action or
state; never use them as ambient decoration.

**The Truth Before Reassurance Rule.** Green is reserved for evidence-backed,
fresh operational or verified state. Stale or failed reads revoke it.

## Typography

**Display Font:** Iowan Old Style (with Palatino and Georgia fallbacks)  
**Body Font:** system sans-serif  
**Label/Mono Font:** system monospace

**Character:** Editorial type gives owner outcomes gravity. Neutral sans-serif
copy keeps actions readable, while monospace is reserved for evidence,
timestamps, and machine identity.

### Hierarchy

- **Headline:** Responsive, semibold serif for the day's owner-facing outcome.
- **Title:** Semibold serif for Responsibility names and meaningful results.
- **Body:** Regular system sans-serif with a comfortable reading measure of
  roughly 66–72 characters.
- **Label:** Compact, bold monospace for status, freshness, and evidence metadata.

### Named Rules

**The Outcome Type Rule.** Serif type belongs to outcomes and Responsibility
titles, never to telemetry labels or controls.

## Layout

The desktop shell uses a compact sticky header, a centered reading column up to
1040px, and a bottom-anchored Ask composer. Memory and System may widen to
1240px because their diagnostic material benefits from space. The journal is a
single ruled stream rather than a dashboard grid.

Below 720px, the header reduces to identity and health, the journal becomes a
single column, and a five-destination bottom navigation provides Today, Work,
Inbox, Ask, and More. Ask opens as a full-screen conversation. Touch actions are
at least 44px high and content remains ahead of navigation in the first viewport.

## Elevation & Depth

The system is flat by default. Boundaries come from paper tone and hairline
rules. Shadows are reserved for floating layers—the health popover and expanded
Ask surface—so elevation consistently means temporary content above the journal.

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
fixed five-item bar with authored line icons, labels, and the same active color.

### Responsibility Journal

Each entry moves from time to named state, outcome, next action, contextual
owner controls, and disclosed evidence. Needs-you and blocked entries gain more
space and direct action; verified entries remain compact.

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
- **Don't** make the secondary dashboard conversation permanently dominate the
  desktop workspace.
