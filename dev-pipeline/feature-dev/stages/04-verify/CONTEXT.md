# Stage 04: Verify

Prove the change does what intake asked and breaks nothing it promised not to break.

## Inputs

| Source | File/Location | Section/Scope | Why |
|--------|--------------|---------------|-----|
| Previous stage | `../03-implement/output/` | Full file | What changed and where |
| Earlier stage | `../01-intake/output/` | "Acceptance Criteria" section | What the change was supposed to achieve |
| Shared | `../../shared/harness-invariants.md` | Full file | The full invariant list to check |
| Reference | `references/verification-matrix.md` | Full file | What to check for each kind of surface |

## Process

1. Read the manifest and the acceptance criteria.
2. Run the full test suite, not only the new tests. Record the result.
3. Exercise each acceptance criterion against a running harness. Record observed behaviour, not intent.
4. Walk every invariant and record held or broken with the evidence that settles it.
5. Test the failure paths the design named. Force each one and observe what happens.
6. Check model agnosticism directly: run the changed path against at least two providers.
7. Note any regression, any surprise, and any behaviour that is technically correct but would confuse a user.
8. Run the audit checks. If any fail, return to stage 03 rather than writing a passing report.
9. Save the verification report to `output/`.

## Audit

| Check | Pass Condition |
|-------|---------------|
| Suite green | The full test suite passes, not only the tests added for this change |
| Criteria observed | Every acceptance criterion has recorded observed behaviour, not a claim that it should work |
| Invariants walked | Every invariant has a held or broken verdict with evidence |
| Failure paths forced | Every failure behaviour from the design was triggered and observed |
| Provider parity | The changed path was exercised on at least two providers with matching behaviour |
| Honest report | Failures and surprises appear in the report. A report with nothing to note is treated as incomplete until confirmed |

## Outputs

| Artifact | Location | Format |
|----------|----------|--------|
| Verification report | `output/[feature-slug]-verification.md` | Test results, criterion by criterion observations, invariant verdicts, failure path results, provider parity, open concerns |

If verification fails, the report still gets written. It records what failed and why, and
stage 03 runs again. A failed report is a normal artifact, not an error.
