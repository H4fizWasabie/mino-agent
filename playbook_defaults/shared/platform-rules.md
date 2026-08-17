# Platform Rules — shared contract boilerplate

## Authoritative clock
- Use the run timestamp in the run header as the authoritative local date.
  A missing date input is not a skip reason.

## Exclusion list
- The ALL_PLATFORMS glob input lists recent published logs across every
  platform. Do not reuse an idea, angle, or punchline from any of them within
  the last 7 days. If the recent logs are unavailable, that is NOT a skip
  reason — read this playbook's own run log first, treat the rest of the
  exclusion list as empty, and proceed.

## Missing inputs / tools are not skip reasons
- You are ALREADY inside the playbook stage execution. Do NOT call
  run_playbook or manage_playbook to start or re-run this playbook. If a
  declared input or tool is unavailable, continue with the steps you can do;
  a missing input is not a skip reason.

## Telegram report
- Send the owner the report EXACTLY ONCE via `send_message` with to=the owner.
  Never re-send on retry or failure. Include the post ID (or skip reason).
