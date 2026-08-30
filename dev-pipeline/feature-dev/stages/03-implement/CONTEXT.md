# Stage 03: Implement

Build the change in the repository. Write code and tests. Record what was touched.

## Inputs

| Source | File/Location | Section/Scope | Why |
|--------|--------------|---------------|-----|
| Previous stage | `../02-design/output/` | Full file | The contract to build to |
| Shared | `../../shared/harness-invariants.md` | Full file | Rules the code may not break |
| Shared | `../../shared/project-identity.md` | "Stack" and "Commands" sections | Build and test commands |
| Reference | `references/code-conventions.md` | Full file | How code in this repository is written |
| Source | (repository) | Only files listed in the design note | The code being changed |

## Process

1. Read the design note. Build to it. If the design is wrong, stop and return to stage 02 rather than improvising.
2. Read the existing code in each file the design names. Match its structure and idiom.
3. Write the change one surface at a time. Keep each surface compiling before moving to the next.
4. Write tests alongside the code, covering every acceptance criterion from intake and every failure behaviour from the design.
5. Run the build and the test suite. Fix what fails.
6. **[Checkpoint 1]** Present the diff summary and the test results. The human reviews before the manifest is written.
7. Run the audit checks. If any fail, revise before saving.
8. Write the change manifest to `output/`.

## Checkpoints

| After Step | Agent Presents | Human Decides |
|------------|---------------|---------------|
| 5 | Files changed with a one-line reason each, plus build and test results | Whether the implementation matches the design or needs rework |

## Audit

| Check | Pass Condition |
|-------|---------------|
| Design match | Every interface in the design note exists in the code with the agreed signature |
| Build clean | The build command exits zero |
| Tests pass | The test command exits zero and the new tests are among those run |
| Criteria covered | Every acceptance criterion from intake has at least one test that would fail without the change |
| Invariants held | No invariant is violated. Any near miss is noted in the manifest |
| No stray scope | No file outside the design note's list was changed, or the extra file is justified in the manifest |

## Outputs

| Artifact | Location | Format |
|----------|----------|--------|
| Change manifest | `output/[feature-slug]-manifest.md` | Files changed with reasons, new interfaces, new config keys, test names added, build and test results, anything deferred |

Source code goes into the repository, not into `output/`. The manifest is the handoff so
stages 04 and 05 know where to look without re-reading the design.
