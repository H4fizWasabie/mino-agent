# Changelog Format

How to write the entry. The reader is a user deciding whether to upgrade, not a reviewer
reading a diff.

## Entry Shape

```markdown
## [version] - YYYY-MM-DD

### Added
- [What a user can now do, in their words.]

### Changed
- [What behaves differently, and what they should expect.]

### Fixed
- [What was broken, described by the symptom they would have seen.]

### Config
- `key.name` (default: `value`) - [what it controls, what happens when absent]
```

Omit any section with nothing in it.

## Writing Rules

Write from the user's side. "Loops now stop after a configurable number of iterations"
rather than "refactored loop bound handling into a separate function."

Describe a fix by its symptom. A user recognises "the agent stopped responding after a
provider timeout." They do not recognise the function that caused it.

Every config key added in this change appears under Config with its default and its
absent-behaviour. The design note has these already.

## Breaking Changes

A breaking change says what a user must do, in the imperative, before anything else:

```markdown
### Changed
- **Breaking:** rename `old.key` to `new.key` in your config before upgrading.
  [Then the explanation.]
```

## Known Limitations

Open concerns from the verification report go here, not into silence:

```markdown
### Known limitations
- [The concern, and what a user should do about it for now.]
```

An open concern that nobody wrote down becomes a support question nobody can answer.
