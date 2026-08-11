# Context Truth — Provider policy docs lag the main-model change

Status: **RESOLVED** (closes GitHub issue #151, commit pending)

## Question

The main brain was swapped to `deepseek/deepseek-v4-flash-0731` (unpinned) and passed the task hy3 failed twice. Does the documented policy need to follow?

## Evidence (2026-08-11 live test)

- hy3 (T1–T4): never found the chart formula; died at the guard (22 iters) and the cap (30 iters); 4–6 parse failures per turn.
- deepseek flash (T5–T6): zero parse failures; systematic hypothesis testing; answered June in-house consumption = 15,576.01 and saved the definition to memory.
- VPS `providers.json` already live: main = `deepseek/deepseek-v4-flash-0731`, no `provider_routing` (unpinned per the bet). Backup: `providers.json.bak-pre-dsv4-main-20260811`. deploy.sh does not touch providers.json.
- Repo docs still say main = `tencent/hy3:tencent` (REL-01 policy, changelog references).

## Resolution

The swap is permanent (owner decision, 2026-08-11): the flash model passed the task hy3 failed twice and stays as main. `providers.policy.json` now declares main/small = deepseek-v4-flash-0731:deepinfra pinned (effort max, DeepInfra routing); cost-watch's monitored set dropped hy3 and keeps deepseek+qwen (price 0.08/0.03 mirror cost.go); SKILL/README docs updated; the policy-file test now asserts the deepseek main. The VPS `providers.json` already matched (pinned 2026-08-11, backups on disk); deploy.sh does not touch providers.json.

Follow-up if the swap is ever reverted:
- Update the policy doc (`providers.policy.json` canonical form) to main = `deepseek/deepseek-v4-flash-0731`, unpinned
- Verify `cost.go` prices the flash model as main (it already prices it as the small role — confirm coverage, don't assume)
- Note the win factors in the doc: the 1M window was NOT the deciding variable; exploration discipline was

## Acceptance criteria

- [ ] Policy doc updated iff the swap is permanent (decision owner: owner)
- [ ] cost-watch price table verified to cover the flash model as main
- [ ] No deployment impact
