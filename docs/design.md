---
name: Mino Living Field
description: An edge-to-edge cartographic universe where Mino's durable world, live activity, and inspection context remain connected.
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

# Design System: Mino Living Field

## Overview

**Creative North Star: "The world is the interface; the work is the workspace"**

Mino should feel like a calm living map, not a stack of dashboard cards. The
owner arrives to see the whole universe, locate a meaningful entity or path,
rewind how it formed, and follow current work without leaving the field. Past,
Now, and Next remain available through the timeline and inspector, but the map
owns the first viewport.

Galaxy is the durable graph expression of that world: a light, packed knowledge
globe where community density and real relationships are legible immediately.
It follows an Obsidian-style packed form in Mino's mineral visual system, never
a dark neon scene or a set of landmarks separated into decorative orbits.

The system is deliberately restrained, but not passive. Semantic status color
appears where it changes the owner's decision; contextual Ask actions and
evidence disclosures provide depth only when requested. Runtime machinery stays
available under System without competing with the owner story.

Work is the operational exception: playbooks are the primary surface there.
The Work lens uses an IDE-style shell with the playbook filesystem on the left,
the conversation workbench in the center, and the selected routed artifact on
the right. The composer remains anchored to the bottom of the conversation so
the current playbook can always be inspected and directed.

**Key Characteristics:**

- The map leads; implementation detail recedes into overlays.
- Galaxy shows dense community structure and its truthful edge mesh at rest.
- Direct sans-serif hierarchy keeps the field readable at a glance.
- Thin rules and restrained floating controls preserve spatial context.
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
state; never use them as ambient decoration. In Galaxy, saturated dot and
hairline territory colors may also identify real communities.

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

The desktop shell is an edge-to-edge canvas. A small floating lens spine, field
summary, search, inspector, and historical timeline sit above the geography;
none becomes a permanent page frame. The collapsed composer and timeline occupy
the lower edge as separate controls. Opening conversation preserves the field
camera and selected node while the workbench becomes the active lower surface;
Evidence, Actions, and Links receive the remaining context space.

Galaxy opens as one large circular packed graph. Memory communities form its
dense core; file/output and operational communities occupy the rim. Desktop and
standard mobile keep this same composition and every node in the current server
projection remains visible. Only viewports narrower than 300px replace it with
the five-branch overview.

Below 720px, each horizontal thread becomes one ordered Past, Now, Next stack,
with every section labelled in place. The header reduces to identity and health,
and a six-destination bottom navigation provides Today, Work, Inbox, Memory,
Ask, and More. Touch actions are at least 44px high.

## Elevation & Depth

The system is flat by default. Boundaries come from paper tone and hairline
rules. Shadows are reserved for temporary popovers such as health; the
conversation workbench remains part of shell layout at every desktop size.
Galaxy conveys depth through spherical perspective, node scale, and restrained
overlap—not glow, haze, or decorative particles.

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

### Packed Galaxy

- **Structure:** One circular field with a packed memory core and operational,
  file, and output communities around the rim; never detached landmark orbits.
- **Graph truth:** Draw all projected nodes and a thin mesh of real edges at
  rest. Direct selection preserves the current camera and opens the persistent
  inspector; search and deep links may focus intentionally. Neither path
  invents nodes, edges, activity, or counts.
- **Surface:** Mineral-white canvas, saturated community dots, hairline colored
  territories, matte depth, and the existing Mino search, fit, timeline,
  navigation, and inspector controls.
- **Data boundary:** File nodes expose metadata only from Mino-authorized output
  roots and approved runtime files. Configuration, credentials, and secrets
  never enter the graph.

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

### Artifact actions

Mino runs headless on a VPS, so artifact controls stay in the browser. Folder
actions open the authorized Files view; file actions open a read-only endpoint.
The file browser offers Copy path and Download, and every action reports missing,
disallowed, or unsupported targets at the initiating control.

## Do's and Don'ts

### Do:

- **Do** lead every primary surface with an owner outcome or decision.
- **Do** derive health, freshness, and counts from current backend state.
- **Do** keep evidence available behind clear disclosure controls.
- **Do** preserve semantic labels and visible keyboard focus.
- **Do** adapt navigation and Ask behavior specifically for phones.
- **Do** keep every node in the current Galaxy projection visible and preserve
  its real relationships.

### Don't:

- **Don't** promote runtime architecture into primary navigation.
- **Don't** fabricate activity, proof, health, or reassuring empty states.
- **Don't** turn the journal into a grid of interchangeable metric cards.
- **Don't** use color, tiny glyphs, or motion as the only status signal.
- **Don't** turn Galaxy into a dark or neon scene, hide its relationship mesh,
  or separate its communities into decorative landmark orbits.
- **Don't** center Work inside a narrow reading column or disconnect current
  state from its Past evidence and Next action.
