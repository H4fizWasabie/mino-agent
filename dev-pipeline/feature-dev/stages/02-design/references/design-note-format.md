# Design Note Format

The shape of a stage 02 output. Keep it short. A design note longer than two pages is
usually two features.

```markdown
# Design: [feature name]

## Problem
[One paragraph, carried from intake. What a user cannot do today.]

## Approach
[The chosen approach in one paragraph. Name the approaches rejected and why in one line each.]

## Interfaces
| Name | Signature | Purpose |
|------|-----------|---------|
[Every new or changed function, type, or endpoint.]

## Config Surface
| Key | Type | Default | When absent |
|-----|------|---------|-------------|
[Every new config key. "When absent" is not optional.]

## Data Flow
[Where the data enters, what transforms it, where it lands. A short list of steps or an
ASCII diagram. No code.]

## Failure Behaviour
| Failure | Behaviour |
|---------|-----------|
[Timeout, malformed response, cancellation, exhausted context. One row each.]

## Invariant Check
| Invariant | Verdict | Note |
|-----------|---------|------|
[Every invariant from harness-invariants.md, with held or resolved-by.]

## Files to Touch
[The list stage 03 builds from. Being wrong here is normal. Being vague is not.]

## Out of Scope
[Carried from intake, plus anything the design work newly excluded.]
```

## Rules

The note defines what and when. It never defines how. No function bodies, no algorithms
spelled out line by line, no variable names beyond interface signatures.

If you cannot fill in "When absent" for a config key, the key is not designed yet.

If the invariant check has a verdict other than held, the resolution goes in the note before
implementation starts. Resolving an invariant conflict during implementation is how
invariants quietly stop being invariants.
