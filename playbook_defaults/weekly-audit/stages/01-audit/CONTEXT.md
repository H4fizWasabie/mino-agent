# Weekly Judgment Audit

## Process

1. **Day gate**: if the authoritative local date is NOT Sunday, write `output/weekly-audit.md` with "Skipped: not Sunday" and end. No Telegram.
2. Read the week's logs on a hard budget. Glob `~/.mino/playbooks/*/runs/*/stages/*/output/*.md` for run dirs from the last 7 days, then SAMPLE: read at most the 10 most recent runs per playbook, and at most 2 output files per run. Keep the whole read/score phase within a 30-iteration budget — when the budget is spent, stop reading and score from what was read. Never continue reading past the budget.
3. Score four dimensions, evidence-based (quote the log line when citing):
   - **Angle repetition**: do any posts (same or different platforms) carry the same idea, claim, or angle within 7 days? List each repeated pair.
   - **Stale jokes**: any punchline or format repeated across posts, or any of the banned jokes (VS Code / localhost) reappearing?
   - **Image rubber-stamps**: for logs with an image critique, does the critique name concrete observed details, or is it generic approval ("perfect fit", "looks great")? List weak critiques.
   - **Schedule health**: `schedules.json` last_error values, blocked runs in the trace (`schedule_fire_failed`), and any stage that hit its iteration cap.
4. Write the recommendations: for each finding — what to change, and the exact contract/step it applies to (file + section). Mark each as MUST / SHOULD / NICE. Do not apply any change yourself.
5. Send the owner the Telegram summary EXACTLY ONCE via `send_message` with to=the owner: the score per dimension (good/needs work), the top 3 findings with quotes, and the MUST recommendations.
6. Write the DECLARED output `output/weekly-audit.md` with the full report (findings, quotes, recommendations, scores).

## Tools

- `read_file`
- `bash`
- `write_file`
- `send_message`

## Outputs

| Artifact | Location | Format |
| --- | --- | --- |
| Audit report | `output/weekly-audit.md` | Markdown |
