# Stage 05: Ship

Write the changelog entry and update the docs a user reads. Close the loop.

## Inputs

| Source | File/Location | Section/Scope | Why |
|--------|--------------|---------------|-----|
| Previous stage | `../04-verify/output/` | Full file | Confirmation the change works and any open concerns |
| Earlier stage | `../02-design/output/` | "Config Surface" and "Interfaces" sections | What users need to be told about |
| Earlier stage | `../03-implement/output/` | "Files Changed" section | Locate the docs that describe the changed behaviour |
| Reference | `references/changelog-format.md` | Full file | The entry format this repository uses |

## Process

1. Confirm the verification report passed. If it did not, stop. Nothing ships on a failed report.
2. Write the changelog entry from the user's point of view. What can they now do, or what changed under them.
3. List every new or changed config key with its default and what happens when it is absent.
4. Update the docs that describe the changed behaviour. Find them from the manifest's file list.
5. If the change alters a documented interface, note the migration a user must perform.
6. Carry any open concern from the verification report into a known-limitations note. Do not silently drop it.
7. Run the audit checks. If any fail, revise before saving.
8. Save the release note to `output/` and apply the changelog and doc edits to the repository.

## Audit

| Check | Pass Condition |
|-------|---------------|
| Verification passed | The verification report shows a pass. A failed or missing report blocks this stage |
| User framing | The changelog entry describes what a user can do, not which functions were refactored |
| Config documented | Every config key from the design note appears in the docs with its default |
| Docs match code | No documentation now contradicts the shipped behaviour |
| Concerns carried | Every open concern from verification appears as a known limitation or is explicitly resolved |
| Migration noted | Any breaking interface change states what a user must do |

## Outputs

| Artifact | Location | Format |
|----------|----------|--------|
| Release note | `output/[feature-slug]-release-note.md` | Changelog entry, config additions, docs touched, migration notes, known limitations |

The changelog and documentation edits land in the repository. The release note in `output/`
is the record of what shipped and why, and it is the last artifact of this run.

This stage never commits, opens a PR, tags, or deploys — that's
[AGENTS.md's production path](../../../../AGENTS.md#building-and-shipping-mino-mandatory-before-you-build),
a separate, already-governed process that starts only with explicit owner approval.
