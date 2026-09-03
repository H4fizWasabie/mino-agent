# Ship note: playbook dispatcher verification

Source fix and regression coverage are complete. The change is not deployed:
the repository release lane and VPS deployment require separate owner approval.

Before release, rerun the focused tests and resolve or separately ticket the
existing `verifyNewBinary` test timeout so the full suite can be certified.
