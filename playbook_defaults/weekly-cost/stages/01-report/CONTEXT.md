# Weekly Cost + Output Report

## Prices (FALLBACK only — legacy records without real cost)

| Model | Input | Cache | Output |
| --- | --- | --- | --- |
| tencent/hy3:tencent | $0.132 | $0.033 | $0.528 |
| deepseek/deepseek-v4-flash-0731:deepinfra | $0.08 | $0.016 | $0.18 |
| qwen/qwen3.7-flash | $0.03 | $0.006 | $0.13 |
| anything else | check openrouter.ai pricing for that model | |

## Process

1. **Day gate**: if the authoritative local date is NOT Sunday, write `output/weekly-cost.md` with "Skipped: not Sunday" and end. No Telegram.
2. Compute the week's usage in ONE `bash` call: `tail -200000 ~/.mino/usage.jsonl | jq -s '[.[] | select(.ts >= (now - 7*86400 | todateiso8601)) | {m: .model, c: (.cost_usd // 0), i: (.in // 0), o: (.out // 0)}] | group_by(.m)[] | {model: .[0].m, calls: length, in: (map(.i)|add), out: (map(.o)|add), cost: (map(.c)|add)}'` — **prefer the recorded `cost_usd` (real provider spend, issue #76)**; use the fallback table above ONLY for models whose records carry no cost_usd (in/1e6 * price, cache included).
3. Count the week's published posts: one `bash` glob over `~/.mino/playbooks/*/runs/*/stages/*/output/*.md` for run dirs from the last 7 days — one line per playbook: number of completed logs. Note skipped days (missing dates) per playbook.
4. Check `schedules.json` for any `last_error` values still set.
5. Compose the Telegram report: 💰 **Spend** (per model: tokens, calls, cost — and the week total), 📤 **Output** (posts per playbook), ⚠️ **Issues** (schedule errors, blocked runs), and a one-line trend vs the previous week if usage.jsonl has data for both.
6. Send via `send_message` with to=the owner, EXACTLY ONCE.
7. Write the DECLARED output `output/weekly-cost.md` with the full numbers and the exact message text.

## Tools

- `bash`
- `read_file`
- `write_file`
- `send_message`

## Outputs

| Artifact | Location | Format |
| --- | --- | --- |
| Report | `output/weekly-cost.md` | Markdown |