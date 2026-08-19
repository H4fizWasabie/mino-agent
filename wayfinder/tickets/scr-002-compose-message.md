# LLM-Synthesized Telegram Reports for Script-Backed Playbooks (SCR-002)

Status: **OPEN** — follow-up to SCR-001; owner feedback 2026-08-19: scripted
playbooks' Telegram messages "are not the same since last time" (template-shaped
vs LLM-synthesized).

## Question

Can the human-facing Telegram message be LLM-synthesized per run without
re-introducing the loop, the token bloat, or the degeneration failure class the
scripts eliminated?

## Design

**`compose_message` registry tool** (Go-side, reachable via `mino exec` — works
with the Phase-1 runner today, no hybrid stage needed):

- **Input**: one `digest` string — the script's already-extracted, verified data
  (bounded ~1–3KB; never raw tool output).
- **Output**: the synthesized Telegram message (~200–300 token cap).
- **Mechanics**: ONE provider call through the existing client (main model,
  deepseek-v4-flash-0731), fixed system prompt — Mino's report voice, the
  verification discipline: *numbers come from the digest only; never fabricate,
  never infer beyond it*. No tools array, no iteration loop, no serialization —
  the degeneration class is structurally impossible in a single text-in →
  text-out turn.
- Every call lands in `tool_calls` + audit like any tool (session attribution
  via `MINO_EXEC_SESSION`).

**Cost**: ~2–4k tokens/run vs the 104k–381k LLM-run baselines — the ~99% cut
survives; only the human-facing sentence is LLM-shaped again. This is the
intended layering (Jake #1): code underneath, LLM at the interface.

**Deprecation path**: when the hybrid stage runner lands (instagram design,
SCR-001), synthesis becomes an explicit LLM observation stage; `compose_message`
remains the mechanism scripted playbooks use until then — and after, if
convenient. Not a throwaway: the tool is the bounded primitive either way.

## Changes

1. `compose_message` tool in the registry: schema `{digest: string}`; handler
   does one `CreateContext` call (fixed system prompt, `maxTokens` ~300,
   no tools); returns the message text or `Error:`.
2. Script updates (all three pilots + future conversions): notify leg becomes
   `MSG=$(mino exec compose_message "$(jq -nc --arg d "<digest>" '{digest: $d}')")`
   → `send_message {"message": $MSG, "to": "<owner>"}`. Digest per playbook:
   gmail = count + IDs summary; weekly-cost = spend table + posts + issues;
   daily-ai-concept = concept + snippet + v-number.
3. Tests: tool test with a fake client (prompt shape, bounded output, Error on
   provider failure); seam test (REL-04 — new named seam).

## Measurable

- Reports read like Mino again (owner judgement — the actual acceptance bar).
- Runs stay ~0 tool calls; per-run LLM tokens bounded (~2–4k).
- No new failure class: single-turn, no tools, no loop.

## Out of scope

- Hybrid stage runner (SCR-001 follow-up) — synthesis works without it.
- Changing what scripts verify — the digest is the only input, by construction.
