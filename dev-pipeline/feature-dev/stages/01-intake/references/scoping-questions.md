# Scoping Questions

The questions that expose hidden scope before it becomes hidden work. Stage 01 works through
these. Not every question applies to every idea.

## The Problem

- What can a user not do today that this would let them do?
- Who hits this, and how often? Once a day, once a release, once ever?
- What do they do instead right now? If the workaround is fine, the feature may not be needed.
- What breaks if this is never built?

## The Shape

- What is the smallest version that solves the real problem?
- What would this grow into if nobody drew a line? Name it, then draw the line.
- Is this one feature or three features wearing a coat?
- Does this belong in the harness, or in a layer above it?

## The Surfaces

- Does this change the loop, the context strategy, the guardrails, or an interface?
- Does it add a config key? Every key is a permanent support obligation.
- Does it change anything a user has already integrated against?
- Does it need provider-specific behaviour? If yes, that is a design problem to solve, not a
  reason to break agnosticism.

## The Cost

- What does this make harder later?
- What has to stay true forever once this ships?
- Can it be removed later, or is it permanent once users depend on it?

## The Check

- Is this on the "do not build" list?
- Has a previous design run already settled this? Check the decision log.
