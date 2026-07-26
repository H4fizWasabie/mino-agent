# GitHub issue triage

## Read

- `config.md`

## Do

1. List open issues: `gh issue list --repo H4fizWasabie/mino-agent --state open --limit 10 --json number,title,author,createdAt,labels,comments`
2. For any issue created in the last 24h with 0 comments: read it with `gh issue view <number>`.
3. Draft a helpful reply (under 200 words, warm tone):
   - Bug reports: ask clarifying questions.
   - Feature requests: thank them, explain if it fits Mino philosophy (simple, minimal, Go stdlib).
   - Questions: answer them.
4. Write all drafted replies to `output/01-issue-replies.md` for Abah to review.
5. Send a brief Telegram summary to Abah:
   - If issues found: "🐙 GitHub: Found X new issues. Drafts ready for review."
   - If nothing new: skip Telegram (no noise).
   - Bot: `8905695639:AAGw4w08yz_AWMUXGoEb7A90-f3o00sh5yk`, chat: `1794722543`
6. STOP HERE. Do NOT post any replies. Only post if Abah explicitly approves.

## Tools

- bash

## Write

`output/01-issue-replies.md`
