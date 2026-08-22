# Mino v2.9.1 — Full-Capability Live Verification

**Date:** 2026-08-14 · **Environment:** production VPS (vultr, mino v2.9.1, DeepSeek v4 flash via OpenRouter/DeepInfra, qwen3.7-flash fallback) · **Method:** one live session (`full-cap-test-20260814`), 21 turns, driven via the dashboard API against the running agent — no staging, no mocks, no simulators.

This is living proof of what Mino actually does — every row below is a real task executed by the real agent against real state, with metrics from `traces/` and `usage.jsonl`. It published a real Facebook post, learned a real memory fact, and spent **≈ $0.05** doing it.

## Session ledger

| # | Task (what it proves) | Iters | Tokens in | Tokens out | Tools used | Verdict |
|---|---|---|---|---|---|---|
| 0 | `system_check` boot state (cost awareness, CTX-020) | 3 | 21,763 | 1,224 | system_check, cost_watch_status, list_schedules, list_playbooks, list_reminders | ✅ cost block + catalogue; Mino pulled cost_watch_status unprompted |
| 1 | `save_note` + `remember` (memory provenance, GIG-001/DRF-001) | 11 | 110,335 | 3,379 | save_note, remember, read_file, bash | ✅ `source: user` stamp; age markers surfaced |
| 2 | `manage_memory dedup` (honest tools, v2.9.0) | 2 | 16,736 | 132 | manage_memory | ✅ "superseded by consolidate" — no fabricated counts |
| 3 | `search_web` + `fetch_url` + `bash` + file round-trip (markitdown, RTK, essentials) | 9 | 111,145 | 3,037 | search_web, fetch_url, bash, write_file, read_file | ⚠️ see Findings 2–3 |
| 4 | `list_playbooks` + `audit_playbook` (CTX-018) | 2 | 18,058 | 815 | list_playbooks, audit_playbook | ✅ real risk flag: morning-briefing `verification: false` |
| 5 | `run_playbook` daily-ai-concept (run-cost line, distill whitelist) | 14 | 222,176 | 5,198 | run_playbook, search_web, bash, save_note, remember, send_message | ✅ **Run cost $0.0106**; fact retrievable; ⚠️ finding 1 |
| 6 | `post_mortem` (CTX-017) | 4 | 41,887 | 1,345 | post_mortem, bash, list_schedules | ✅ failure evidence extracted; cross-checked recovery |
| 7 | `run_playbook` facebook-daily-ai-post (image routing, real publish) | 21 | 395,850 | 10,896 | run_playbook, generate_image, view_image, composio, search_web, send_message, system_check | ✅ **real post published**; vision path fired; **$0.0153** |
| 8 | Mid-flight interrupt (CTX-007/011/012 stop-boundary) | 1 | — | — | — | ✅ "Stopped." at 1 iteration; next turn fresh |
| 9 | Number verification (CTX-003) | 4 | 34,088 | 458 | list_playbooks | ✅ both numbers stated; verified from source of truth |
| 10 | `cost_watch_refresh` + `send_document` + reminders (extensions) | 3 | 31,990 | 771 | cost_watch_refresh, send_document, write_file, create/cancel_reminder | ✅ 29-entry catalogue; doc delivered via outbox (trace `outbox_doc_delivered`) |
| 11 | Cleanup (forget note, delete files, verify no residue) | 3 | 9,486 | 300 | manage_memory, bash, list_reminders, list_schedules | ✅ zero residue; post-session chat_log surgically removed |

**Session totals:** ~77 iterations, ~1.01M input tokens, ~28.5k output tokens, **~$0.05** (incl. both playbook runs).

## Findings (the session earned these)

1. **Double front-matter bug reproduced (2nd occurrence).** The daily-ai-concept run's save_note stacked a second YAML block on the playbook's own front-matter, breaking fact indexing (recall returned 0). Mino self-diagnosed from memory (`memory_file_double_front_matter_breakage_20260813` — the same failure on 08-13), repaired the file, re-verified retrieval with edges. **A real bug, reproduced live, self-healed — and it's a fix candidate: save_note should detect existing front-matter.**
2. **Stale fact surfaced by recall.** `mino_runs_on_qwen3_7flash` (created 08-08) claims qwen is the main model — wrong since the 08-11 swap to deepseek flash. The age marker surfaced (`age: 6d`) but the brain trusted the fact. The visibility signal (CTX-014) works; the *judgment* on staleness is the remaining gap — DRF-001's stated frontier.
3. **fetch_url fell back to plain text with HTML entities** (`&amp;`, `&#8217;`) on one fetch — markitdown either failed or was bypassed for that page, and the fallback path doesn't unescape entities. Markitdown is installed (0.1.6); the fallback is the gap.
4. **RTK verified live.** The bash tool's `ls -la` output came back in rtk's compact shape (`644  name.md  556B`) — the rewrite hook (tools.go) is active in production, not just configured.
5. **Mid-turn self-correction.** The FB run's model misread the run-cost line, corrected itself mid-turn, and verified via system_check — the verify-then-claim discipline (#160) behaving as designed.
6. **The FB playbook that once burned 50 iterations (08-12) completed in 21 with a real image + verified publish** — the iteration-awareness work (#171) holds.

## Fixes & features proven in this session

- Cost awareness (CTX-020): system_check cost block, per-run cost line ($0.0106 / $0.0153), cost_watch_refresh (29 entries)
- Memory floor (v2.9.0): provenance stamps, honest dedup, age markers, distill whitelist live
- Harness self-repair: audit_playbook risk flags, post_mortem evidence, mid-flight stop
- Image routing: generate_image → view_image (vision model, 1,480 chars) → composio publish with URL pre-verification (HTTP 200 before posting)
- Outbox: send_document delivered token-free (`outbox_doc_delivered` trace)
- Loop discipline: interrupt at 1 iteration, fresh restart, number verification, self-correction

## Reproducibility

The full prompt script lives in the session's chat_log/traces on the VPS (session `full-cap-test-20260814`, traces `2026-08-14.jsonl`). Re-run: any of the 11 tasks above as a `/api/chat` message; metrics come from `traces/*.jsonl` (`llm` in/out, `turn_end.iterations`) and `usage.jsonl`.

*Cost honesty: the Facebook post was a real post to the 89lab page; the Telegram document was a real delivery; the memory fact (function calling concept) is real and kept. Everything else was cleaned up post-session — including the session's chat_log rows, so no test conversation can leak into consolidation.*
