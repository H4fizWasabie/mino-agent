# Daily-Jobs Health Alert — Never Silent, Never Spammy

Type: `wayfinder:grilling` (HITL — alert policy is the owner's call)

Blocked by: Outcome Verification (need the failure definition before alerting on it).

## Question

What does "the day's jobs are healthy" mean, and how does Mino page the owner when it isn't — without becoming a nag?

## Context

- The outbox, traces, audit, and schedules.json `missed_at` already exist. What's missing: any alert on "ran but did nothing" (stage skipped, zero outputs posted, zero replies sent).
- The owner noticed the karma freeze after 4 silent days, the missing Telegram report by hand, the GLM promo cliff by hand. The alerting gap is the owner-is-the-monitor gap.
- Alerting too eagerly has a cost: every false alarm trains the owner to ignore the channel.

## Ask

- Which events alert: stage skipped? zero posts across the day? run complete but output says failure? consecutive failures (N=2?)?
- Channel: Telegram immediately, or a daily digest (morning briefing already exists — extend it)?
- Quiet hours / dedup: one alert per playbook per day, or per run?
