Purpose: One Sunday report of the week's actual spend and output: tokens and estimated cost per model, posts published per platform, runs skipped and why.

Routing: One stage. Day-gated: runs only on Sunday (authoritative local date); on other days write the skip log and end without Telegram.

Inputs: authoritative local clock, usage.jsonl, the week's playbook run logs, schedules.json (last_error).

Outputs: a Telegram report to the owner and output/weekly-cost.md.

Safety: compute costs from usage.jsonl with the fixed per-model prices below — never estimate from memory. Report what the data shows, including zero-cost weeks.