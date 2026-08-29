# Playbooks -- autonomous recovery boundaries

Status: **OPEN** (child of PB-001)

## Destination

Allow one Mino agent to recover from playbook failures using workspace
evidence, without blind retries or silent contract mutation.

## Recovery table

| Failure | Mino may do | Mino must not do |
|---|---|---|
| Missing or malformed route/input | inspect, repair a safe workspace reference, or stop | invent missing source data |
| Mechanical script failure | inspect evidence and retry when idempotent | rewrite a script and claim success without verification |
| LLM judgement/audit failure | revise the artifact and re-audit | treat self-certification as mechanical proof |
| Missing/invalid output | correct the artifact path/content and re-verify | rubber-stamp the stage |
| Tool/provider failure | change approach or retry when safe | repeat an unsafe side effect blindly |
| Unknown external receipt | inspect receipt/idempotency evidence or stop | publish/update again on assumption |
| Consequential authority ambiguity | stop and notify the owner | self-authorize |

## Acceptance criteria

- [ ] Every recovery action records the evidence and reason.
- [ ] Retry safety is decided before side-effecting retries.
- [ ] Contract changes are explicit and apply to future runs unless the owner
      authorizes repair of the active contract.
- [ ] Exhausted recovery ends with a truthful failure report and resumable run.

