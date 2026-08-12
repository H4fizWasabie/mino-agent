## [v2.8.12] — Update & alert polish (2026-08-12)
### Fixed
- The self-updater now promotes a release over a same-numeric prerelease: `parseSemver` dropped the `-rc` suffix via `Sscanf`, so a binary running `v2.8.11-rc4` compared equal to the `v2.8.11` release and `mino update` said "Already up to date", forcing a manual swap. A release now beats a prerelease at the same numeric version, so `rc` → `release` promotion works cleanly. (Why: rc staging binaries are the deploy-before-release path; the updater must let the released version supersede them.)
- The high-error-rate `[MINO ALERT]` no longer pages the owner's Telegram: it is operational detail — useful to the LLM (still in the journal and the `tool_calls` DB) but noise in the owner's DM. The silence (dead man's switch) and extension-stuck alerts stay owner-facing. (Why: a transient error spike during normal churn is self-diagnosis material for the harness, not a page for the owner.)

## [v2.8.11] — Harness self-repair: post-mortem, design-time audit, mid-flight (2026-08-12)
### Added
- `post_mortem` tool + failure-evidence injection (implements CTX-017): a failed playbook run now auto-injects a bounded evidence block into the `run_playbook` result — parse-failures with iteration numbers, outcome contradictions, rewrite streaks, iteration usage, final reply — so the LLM diagnoses from evidence instead of re-scanning (which itself churned to the iteration cap). The `post_mortem` tool provides the same extraction on demand. (Why: after a failure the harness already knows what happened; telling the LLM beats making it rediscover by scanning, which burns the exact budget it's diagnosing.)
- `audit_playbook` tool + adaptive design-time gate (implements CTX-018): deterministic agentic-principle checks on stage contracts — research boundedness, verification, grounding, size, tool references — rendered as RISK-FLAGS. The gate auto-audits ONLY when a playbook is new, its last run failed, or a stage contract changed since the last run; a stable, recently-successful playbook costs nothing extra. Risk-flags inject into the stage prompt before execution. (Why: design-time review prevents the FB-class churn preemptively, but auditing every run of a working playbook would be pure waste — the gate audits on suspicion, not habit.)
- Mid-flight discipline + redirect observability (implements CTX-019): the system prompt directs behavior-first response to signals — change approach, read session notes, or state the blocker and stop, rather than re-narrating why you're stuck (a self-explanation mid-flight is provisional). The loop logs `midflight_signal` trace events on repetition and near-cap so redirects are verifiable against outcomes (CTX-017 reads them). (Why: live correction compounds a wrong course-change, so acting on the verified signal must beat explaining it; the trace makes the redirect checkable, not assumed.)
- Action-grounding rule (implements CTX-016): claiming an action completed requires having called the tool in THIS turn and restating its exact result; `manage_memory consolidate` returns before/after evidence. (Why: the anti-fabrication discipline previously covered external IDs; it now covers action claims — a completion claim with no matching tool call is a lie, not a summary.)
### Changed
- SOUL.md: the identity now includes body-awareness (the loop, traces, session_notes, and context are its own observable/diagnosable subsystems); anti-fabrication is de-duplicated by pointing at the harness-enforced rule instead of a second drifting copy; the persist-vs-iteration-cap tension is resolved (persist by changing approach, never by repeating the same dead action).

## [v2.8.10] — Consolidation recency gate (2026-08-12)
### Fixed
- Consolidation selects by recency, not a per-session row-count floor (implements CTX-015, closes #173): `ConsolidateDue` now treats a session as eligible when its oldest unconsolidated row is ≥1h old, instead of requiring `ConsolidateEvery*2` (~6 exchanges / 12 rows) per session. Short interactive chat sessions never met that floor, so their history stayed perpetually unconsolidated (witnessed 2026-08-12: 78 rows stuck in state.db) — and a 0-result pass let the model report a fabricated "consolidated 8 sessions." Now chat history drains on the next scheduled pass, while an active conversation is still protected (recent rows aren't consolidated mid-stream). The `manage_memory consolidate` action also reports "nothing eligible" truthfully when 0 sessions qualify instead of leaving the model to invent a count. (Why: the row-count floor assumed long sessions; interactive Telegram/chat turns are short and stayed below it, so consolidation silently did nothing on chat — the gap that produced the false completion claim.)

## [v2.8.9] — Memory freshness & Telegram send fix (2026-08-12)
### Fixed
- Skill bodies are no longer auto-injected en bloc (closes #170): skills are now auto-matched only when they opt in via `auto: true` in frontmatter (currently just `image-generation`, the one needed by playbook automation). All other skills are invoke-on-demand — they load only on an explicit trigger/description mention, not on fuzzy word-overlap or semantic similarity, so task-specific skills (domain workflows, coding, cost-watch, etc.) can no longer auto-inject their full body on a near-miss request. (Why: measured 2026-08-12 via `context_diag` — playbook stage turns carried 18–28k chars (~4.6–6.9k tok) in the one-turn payload, 2–3× the entire ~2.4k static prompt, dominated by injected skill bodies; only image-generation is needed by automation, so the other skills' full bodies were pure dilute-context cost on near-miss queries. Explicit triggers still work, so no skill is invisible.)
- Iteration/retry awareness + containment (closes #171): the loop now injects a message-stream observation when the model repeats the identical tool call 3× without progress ("CHANGE APPROACH or state why you are abandoning this one … used i of N iterations") and a near-cap heads-up at N−3, plus a static system-prompt iteration-discipline rule. (Why: 2026-08-12 the FB `01-post` stage burned 45–50 of 50 iterations in a research loop (re-reading old logs) because the model had no sight of its own budget or repetition and the harness cap was silent. Awareness is the prerequisite for self-regulation — injected in the message stream, not the byte-stable system prompt, so prefix-cache warmth is preserved.)
- Memory facts surface their age at recall (implements CTX-014, closes #172): live recall now appends the fact's age to the match rationale — `age: Nd` past a 24h grace, flagged `(possibly stale)` past 30d. The `At` timestamp already existed on every `Fact` but was never read by `entryRanking`; now it's wired in so a stale-but-unrejected fact isn't trusted blindly. Ranking score is untouched — purely a visibility signal. (Why: a week-old `public_image_hosting_setup` URL (HTTP datacenter IP) kept driving FB photo-post failures because the model recalled it faithfully and had no idea it was stale; the existing `Feedback` field only drives active *rejection* expiry (MEM-08), not time-based freshness. First witnessed case for the OKF `stale_after` idea we'd skipped — and the field to support it was already there, just unwired.)
- Telegram `send_document` promoted to the essential tool set (implements CTX-013, closes #155): it is now present in every turn's schema instead of relying on keyword/semantic selection. This closes the token-leak path — without it in the schema the model believed no native tool existed and hand-rolled `curl ... api.telegram.org/bot<token>/sendDocument`, putting the bot token in bash args (observed 2026-08-11, raw token in the URL). The earlier "delete the stale curl-notes" step removed the teaching but left the tool unreachable; availability, not preference, was the actual failure. (Why: the original ticket's "no pinning" principle assumed the tool was available and the model merely preferred curl because of stale notes — production disproved that; the model had no native tool in view. A recurring owner need plus a credential-leak risk justify the one-slot schema cost; the tool's own description already forbids taking the token as an argument.)

## [v2.8.7] — Playbook & loop hardening (2026-08-12)
### Fixed
- Provider retry drops a dead `:provider` routing pin on the first retry (closes #159): when a routed (OpenRouter-style) model fails, attempt 2+ re-sends the SAME model with the `:provider` suffix stripped, so OpenRouter's `allow_fallbacks` order routes to a healthy provider instead of burning all retries on — then failing over away from — the same dead provider. Generic and config-driven: only models carrying the post-slash `:tag` are affected; direct-API models (no suffix) and unpinned models are untouched. (Why: 2026-08-12 a sick deepinfra pin made every call time out at the 120s client deadline — 3 attempts on the same dead route ≈ 6 minutes per LLM call — before falling to a different model; a stream-failure on the pinned route should degrade the pin, not the model.)
- Verify-then-claim rule added to the system prompt (closes #160): the static prompt now forbids writing a record, log, or reply containing an external identifier (post ID, order ID, file ID) unless it was received verbatim from the owning tool's actual API response — an invented or reconstructed ID is fabrication, and a failed creation call must be recorded as a failure, never padded with a fake ID. Generic, applies to any model/playbook. (Why: 2026-08-12 a FB run that never called the publish tool still produced a post log claiming a post ID — the model fabricated completion under iteration-cap pressure to paper over the failure; the harness only checks that the output file exists (missingStageOutputs), which cannot see a present-but-false record. The prompt is the generic layer for this failure class, mirroring the existing CTX-003 number-verification discipline.)
- Iteration-cap reply reports progress + a continue/abandon decision point (closes #161): a turn or playbook that hits the iteration cap now replies with which tools were completed (deduped) and an explicit "Continue, or abandon the task?" instead of a bare `(stopped after N iterations)`. (Why: the bare reply gave the user no sense of where work stopped, so every "continue" blindly re-ran the same dead task into the cap again — 2026-08-11 a stuck chat seesawed through four cap-and-continue cycles with the user unable to tell what had or hadn't been done. Surfacing completed tools lets the user and a later turn resume meaningfully.)
- Tolerant tool-call parser recovers bare `NAME({...})` markers (closes #158): when no `[tool_call:` prefixed marker exists, `extractTextToolUses` now falls back to scanning bare identifier + JSON-object calls, so a model that emits `MCP_composio_..._FLAT({"arguments_json":...})` (no prefix) is no longer spammed into a parse retry loop until the iteration cap. Only args that parse as a real JSON object are treated as calls — ordinary prose (e.g. `read_file("/tmp/x")` without a JSON object) stays unparsed. (Why: 2026-08-12 a scheduled FB publish burned ~20 of 50 iterations re-emitting the composio FLAT call in a bare, prefix-less shape the strict parser dropped; the circuit breaker (6) never fired because the model interleaved other calls, so it hit the cap instead of aborting early. The tolerant parser turns that recoverable shape into a real tool call.)
- Reasoning-only responses from providers that use the `reasoning` field are no longer dropped (closes #163): `parseResponse` and the SSE stream parser now capture BOTH `reasoning_content` (DeepSeek) and `reasoning` (e.g. qwen via OpenRouter), falling back to whichever is present when `content` is empty. JSON mode is excluded so a thinking-only reply still triggers CreateJSON's response_format retry instead of being mistaken for the JSON answer. (Why: 2026-08-12 the qwen fallback streamed its entire answer under `reasoning` with `content:null` — `cached_tokens:0`, no cache — and the parser only read `reasoning_content`, so the sole remaining fallback route returned "empty model response" and escalated to rate limits.)
- `stop`/`cancel` now marks the session context (implements the stop-boundary fix, closes #162): `Core.CancelTurn` appends a `[System: ... stopped/cancelled ...]` marker to the session history on every stop — active loop or not — so the next user message is treated as a fresh request instead of silently resuming the cancelled task. The marker persists across restarts via the chat log. (Why: a task aborted by the circuit breaker or iteration cap left its history behind; the next message — even after a `stop` — resumed the dead task and repeated the cap loop. `CancelTurn` previously only cancelled the in-flight loop's context and never touched history, so `stop` was a pause, not a reset. Locking the history append under the conversation mutex keeps it race-free with the turn.)


## [v2.8.6] — Cancel and interrupt hardened (2026-08-11)
### Changed
- Documentation restructured: AGENTS.md is now the mechanically-verified index (rules split into rules.md + coding-conventions.md, contributor workflow in CONTRIBUTING.md, design docs linked). (Why: AGENTS.md mixed process rules, code patterns, and public discipline into one 9.3K blob — an agent needing release gating read everything. The index follows the repo's link-first navigation philosophy; `TestAgentsIndexLinksResolve` fails the suite on any dangling link or missing heading, so the index cannot rot like the memory notes the Context Truth work removed.)
### Fixed
- Interrupt replies are never dropped (implements CTX-012, closes #156): the status call now passes no tool schemas and instructs plain-text replies, so a mid-task "btw status" can no longer return "(no response)" when the model answers with a tool call; a tool-only response falls back to a snapshot status line. (Why: the 2026-08-11 full-suite test showed the model answering the status query with a tool call, which extractText dropped — the snapshot already carries the state, so tools were unnecessary.)
- A stop-word anywhere now cancels (implements CTX-011, closes #155): "its fine, stop" was queued as a normal turn because the remainder guard treated the trailing "stop" as substance; stop/halt now match in any position, with a question guard so "why did you stop" still proceeds. (Why: the 2026-08-11 full-suite test sent a cancel mid-task and it silently became a queued turn.)
### Added
- Wayfinder tickets CTX-012/CTX-013 opened (docs): the full-suite live test found the interrupt path drops replies when the model answers with a tool call, and the send-document tool is evicted by the schema cap so the insecure curl workaround (and the token leak) recurred. Both are the suite's remaining findings.

## [v2.8.5] — Turn cancellation and diagnosable failover (2026-08-11)
### Changed
- Provider policy declares the flash model as main (implements CTX-008, closes #151): `providers.policy.json` now pins `deepseek/deepseek-v4-flash-0731:deepinfra` (effort max, DeepInfra routing) for main and small; the cost-watch monitored set drops the retired main model and keeps the fallback; docs and the policy-file test follow. (Why: the 2026-08-11 swap passed the task the previous main failed twice and is now permanent — the documented policy must match the live config or the next policy review reads fiction. The retired main's price row stays in the cost table as a legacy fallback for pre-swap usage records.)
### Changed
- Provider calls honor turn cancellation (implements CTX-007, closes #152): the loop's LLM and vision calls now go through the ctx-aware client path, so a client disconnect mid-turn propagates into the in-flight provider call and the loop returns instead of holding the session mutex indefinitely. (Why: 2026-08-11 live test — a dead dashboard connection left a turn inside a provider call that ignored cancellation; every later turn for that session blocked until a service restart. The HTTP layer already used request contexts; only the loop called the non-ctx variant.)
- Provider failures are logged with the error string (implements CTX-010, closes #154): every failed call logs provider/role/model/attempt/error and circuit-breaker trips log the cooldown. (Why: the same test's failover to the fallback model was silent — the manager incremented a counter and ate the error; root cause was guessed post-hoc instead of read from the journal.)

## [v2.8.4] — Context Truth lands in production (2026-08-11)
### Added
- Native send_document tool for Telegram file delivery (implements CTX-009, closes #153): queues a local file pointer in the outbox (`doc_*.json`), delivered by the outbox dispatcher as multipart to `/sendDocument`; the bot token and chat id come from config at delivery time and never appear in tool args, trails, or logs. (Why: in the 2026-08-11 live test the model had to read the bot token from env and hand-roll curl `sendDocument` after composio had no Telegram connection — the token landed in tool args, the audit log, and session trails. The outbox pattern already kept text sends token-free; documents now ride it.)
- Wayfinder ticket CTX-010 opened (docs): provider failure reasons are swallowed by the circuit breaker — the 2026-08-11 failover to the fallback model was silent, root cause guessed post-hoc. Log the error string; fallback semantics unchanged.
- Wayfinder tickets CTX-007/CTX-008 opened (docs): the session-wedge bug found in the 2026-08-11 live test (client disconnect + provider call ignoring ctx = mutex held forever, restart required) and the provider-policy doc lag after the main-model swap to the flash model. (Why: both surfaced as real gaps during the verification run — one robustness bug, one doc drift; tickets capture them before they rot.)
- Public-facing discipline enforced mechanically: `TestChangelogPublicDiscipline` fails `go test ./...` if CHANGELOG.md or wayfinder tickets contain owner names, business specifics, personal data paths, session ids, or amounts. (Why: the Context Truth batch changelog carried the owner's incident details — business data, database names, figures — until caught by review; the discipline rule existed in AGENTS.md but nothing stopped a violation from shipping. Same mechanism as REL-04a's seam presence check. Pre-existing wayfinder tickets were scrubbed to the same standard.)

- Wayfinder map "Context Truth" opened (docs): root cause ticket CTX-001 confirmed from a mid-session amnesia incident (wholesale large-message replacement wiped method knowledge between turns; a computed value that differed from the user's named one was smoothed over instead of stated; no stop signal let the run burn the iteration cap), resolved CTX-002 (head/tail preview, #145), frontier CTX-003..006 (verification discipline, working-state persistence, cancel-intent recognition, degeneration guard). (Why: the incident produced a measured, reproduced failure chain — the map is the contract the four tickets are held to, in the same format as the VFY/SCH maps.)
- Session working note (implements CTX-004, closes #146): a per-session `session_notes` SQLite row is appended by the harness (every bash command, mechanically, 200-char cap) and by the model (`note_session` tool, now essential) and injected at the start of the next turn, bounded at 1500 chars with head+tail truncation. (Why: an agent re-hunted the same database path three times in one session and started a follow-up turn at a different project's database — turn N+1 did not inherit turn N's established paths, methods, or an open numeric discrepancy. The note is ephemeral per-session working state, distinct from `save_note`'s durable graph; the harness part is judgment-free so it cannot be forgotten like a model-written memory note that lacked the path.)
- Degeneration guard counts total parse failures per turn (implements CTX-006, closes #147): the #24 circuit breaker's streak counter is now a per-turn total — the reset on successful calls is removed — so a model alternating malformed markers and successes (9 failures at scattered iterations, never 6 consecutive) aborts at 6 total instead of burning to the cap. (Why: the alternation pattern made the consecutive-only guard blind; the shipped 6-threshold is preserved so recovering turns are untouched — the 3-failure-then-done test still completes.)
- Cancel-intent recognition (implements CTX-005, closes #148): `isStopMessage` matches natural cancel phrasings anywhere (its fine, nevermind, ill do it myself, ill get this data, forget it, dont bother, lets drop it); after stripping phrases and glue words, a substantive remainder means the message is a doubt/question and the turn proceeds. Leading stop/cancel/halt unchanged; rhetorical "never mind?" still stops. (Why: a user's "its fine then, ill get this data myself" matched no stop pattern, so the turn ran to the cap instead of stopping; the doubt/cancel hybrid now runs as a cheap turn because CTX-002/004 hold the facts.)
- Number verification discipline in the system prompt (implements CTX-003, closes #149): a user-named value that differs from a computed one must be answered with BOTH numbers and the gap — never "essentially correct" — and "verified" is only claimed from a source-of-truth definition (app source/API, or the schema column defining the filter), never an invented query landing nearby. (Why: a computed value that landed near the user's named one was reported as "essentially correct" while differing materially, and a report was built on the invented method; the failure class is VFY, now stated in the static prompt.)
### Changed
- Large history messages keep a head+tail preview instead of a bare placeholder (closes #145): messages over `inputPreviewLimit` (8000 chars) now render the first 4000 + last 4000 chars with HEAD/TAIL markers instead of `[Large previous message...]`, mirroring `compactUserInput`. (Why: a follow-up question burned the iteration cap because the previous turns' replies (20-25k chars each, carrying the method in their `[tools used:]` trails) were wholesale-replaced by the placeholder — the model started from a different project's database and re-derived everything from scratch. The bare pointer deleted self-describing method knowledge that cost nothing to keep; the tail preview preserves it at the same budget.)

## [v2.8.2] — Truth in the logs (2026-08-10)


## [v2.8.1] — Never silent (2026-08-10)
### Added
- deploy.sh is bootstrap-only and the updater-less extensions ship as release assets (closes #131, REL-05c): deploy.sh no longer builds or pushes ANY binary (mino, minowrap, threads-extension) — it keeps user setup, RTK, oauth.d, extensions.json, systemd units, and the state.db backup; build-release.sh now also builds minowrap + threads-extension into the release with SHA256SUMS lines; the manual lane is documented as an emergency procedure in docs/emergency-deploy.md (raw scp + SHA-verification checklist, acceptable only when GitHub is down or a release is broken); README points at release → `mino update` as THE deploy path. (Why: the self-updater was made the only production path in REL-05, but minowrap and threads-extension had no self-updater — they stayed a local-tree push path, the exact failure class the decision was meant to kill. Extensions now have a verified release-asset path, and the emergency lane is documented rather than scripted, awkward on purpose.)
- Prompt-assembly test surface enforced (closes #134, REL-04a): `seams_test.go` names the 11 prompt/contract seams and a presence check fails `go test ./...` when any lacks a Test function naming it; the 6 uncovered seams now have table-driven tests (parseStageInputs, parseStageOutputs, workspaceInputPath, renderWorkspaceInput/Files, truncateWorkspaceInput) with the edge cases from the real 2026-08-10 fixes (triple-newline markers, absolute-path double-join, empty globs, 4000-char cap), plus real coverage tests for buildWorkspaceStagePrompt and the ## Success verification. AGENTS.md carries the rule; new seams join the list when born. (Why: two production failures were 5-line prompt-assembly bugs with zero tests on the path while 366 tests sat elsewhere — the presence check kills the silent zero mechanically, riding the existing CI gate.)
- The self-updater now verifies the downloaded binary against the release's SHA256SUMS.txt before the atomic rename, and appends a code-generated who/what/when line to `deployments.log` (closes #132, REL-05a): a mismatched checksum, a missing checksum asset, or a release without a line for the platform all refuse to install; every successful update records timestamp/version/sha256/binary in an append-only 0600 log. (Why: REL-05 made the self-updater the only production path, but "verified" was a version check + size check only — the claim rested on prose. A compromised or corrupted release asset would have installed. The ledger is written by the updater itself so it cannot rot like agent-remembered disciplines.)
- Prompt-assembly test surface codified (REL-04, closes #119): the named seam list is the rule — the 11 functions that render prompts or parse playbook contracts (parseStageInputs/Outputs/Success, buildWorkspaceStagePrompt, renderWorkspaceInput/Files, truncateWorkspaceInput, workspaceInputPath, cleanPlaybookRequest, ## Success verification) must each carry a table-driven test before any feature touching them ships. Enforcement is mechanical: a presence check in seams_test.go fails `go test ./...` if a listed seam lacks a Test function — CI already runs the suite, so no new workflow. Presence-only by design: it kills the silent zero (the actual 2026-08-10 failure class), quality stays with code review. (Why: two production failures were 5-line prompt-assembly bugs with zero tests on the path while 366 tests sat elsewhere — conventional coverage gave false confidence; the fixes landed with table-driven tests that caught triple-newline markers, path-header attribution, and empty-glob semantics within minutes. Playbook contracts count through their Go seams; no separate contract-testing regime. Follow-up with acceptance criteria: #134.)
- Deploy decision codified (REL-05, closes #120): the self-updater is the only path to production — deploy.sh is demoted to bootstrap-only (user, RTK, oauth, units, DB backup; no binary pushes) and the manual path survives only as a documented emergency procedure (scp + SHA-verification checklist). Releases are owner-only; `mino update` on the VPS is any agent, with the updater writing the who/what/when line itself to deployments.log. Builds are tag-only: version labels can never precede a tag. (Why: on 2026-08-10 GitHub, local, and VPS diverged — a manual scp shipped a stale binary caught only by checksum mismatch, cost-watch's unit was invisible to deploy.sh and never updated, and a v2.8.0 label ran before the tag existed because the release was cut from a stale master. The failure class was always "a build from an unverified local tree"; the self-updater was the only path with built-in verification, so it becomes the only path. Follow-ups with acceptance criteria: #130 tag-only builds, #131 bootstrap-only + emergency lane + extension release assets, #132 SHA256SUMS verification + deployments.log.)
- Brain Policy codified (implements REL-01, closes #126): `providers.policy.json` is the canonical provider config (main/stages `tencent/hy3:tencent` pinned, small role `deepseek/deepseek-v4-flash-0731:deepinfra` pinned, fallback `qwen/qwen3.7-flash`, main provider text-only so vision turns land on qwen) and `cost.go` is the one price table behind two review triggers: a scheduled run over $2 and a calendar month over $25 both page the owner once via the health-alert channel (per-playbook-per-day dedup for runs, once-per-month for the monthly check). Triggers are review triggers, not caps — they re-open the policy, they never kill a run. (Why: the previous brain was chosen by taste and the price table lived in a VPS playbook that didn't even list hy3; the policy and its cost guardrails are now test-enforced from one source. cost-watch runs on the VPS but its role is promo-expiry paging (alert-only since #128), so the spend triggers live in the main binary where usage.jsonl already lands. qwen cannot be pinned: the OpenRouter catalog has no suffixed variant, so fallback+vision ride the unpinned slug by evidence.)
- cost-watch is alert-only (closes #128): the autonomous swap capability is removed — `swap_model` no longer exists in /tools, the chain/provider_templates config is gone, and the monitored set defaults to the three REL-01 policy models (hy3:tencent, deepseek:deepinfra, qwen3.7-flash) at their policy prices, so promo-expiry and price-spike paging covers the actual brain. /check and the hourly timer flag spikes but can never rewrite providers.json (test-enforced). (Why: the REL series exists because autonomous model behavior kept surprising the owner — an LLM-callable brain-swap tool was the same trap wearing a different hat; cost-watch pages, humans decide. qwen/deepseek/hy3 pages are now the monitoring set instead of the retired luna-pro.)
- Daily-jobs health alert (implements REL-03, closes #124): a failed scheduled run pages the owner once per playbook per day via the outbox, with the stage and the failure reason inline; a failure on 2+ consecutive owner-local days escalates to a louder "this is broken" message. A successful run resets the streak mid-day; cancelled (owner-stopped) runs never alert or count. Counters persist in schedules.json (`fail_streak`/`last_fail_day`/`alerted_day`) where the morning briefing already reads, so trends ride the existing file. (Why: the owner was the monitoring system — the karma freeze, a missing Telegram report, and the GLM promo cliff were all caught by hand; REL-03 makes a broken job page the owner instead. One alert per playbook per day plus streak escalation keeps it from becoming a nag. The no-run case was already covered by the #74 missed-run notice, so no second "nothing ran" message exists.)
- Optional `## Success` stage contracts (implements REL-02, closes #122): a stage CONTEXT.md may declare expected outcomes as a table (`| Outcome | Required tool call |`); the harness then requires a successful call to the named tool whose result carries a 15+ digit platform ID, verified from the stage's own recorded tool log — the model cannot satisfy it by editing files. A missing outcome pushes once through the existing retry path, then fails the run with a `stage_outcome_failed` trace event. Stages without the section behave exactly as before. (Why: the owner was the monitoring system — the karma freeze, a missing Telegram report, and the GLM promo cliff were all caught by hand after silent runs; REL-02 lets the harness distinguish "ran and published" from "ran and said so" for the six must-publish playbooks.)

## [v2.8.0] — Context budget cut (2026-08-10)
### Added
- Context diagnostics measure the real payload (closes #91): `context_diag` traces now log the actual serialized tool-schema JSON bytes plus the five heaviest schemas, and /metrics exposes today's median and p90 of per-iteration input tokens with the iteration count. (Why: the old +200-char-per-schema estimate undercounted MCP executor schemas ~4×, so context-budget decisions — schema compaction and per-session schema capping — were being sized blind; the daily median/p90 is the destination metric of the context-bloat effort (map #88).)
### Changed
- The per-session tool-schema union is now capped at 20 tools (closes #105, implementing the #92 design): the 11 essential tools stay pinned; when the union exceeds the cap the least-recently-selected non-essential tool is evicted, with tools explicitly named in the current turn immune until the next turn. The wire array stays alphabetically sorted and mutates only on selection churn — one prefix-cache miss per task-phase change, then re-cache. (Why: the monotonic union converged to near-total tool coverage on long-lived sessions — 28-47 schemas per chat turn observed on the VPS — the dominant remaining term in per-iteration input after schema compaction; at ~500-800 compacted bytes per schema a 20-tool cap cuts the chat schema payload roughly another 30% toward the map #88 destination of ≤20k median / ≤25k p90 input tokens.)
- `view_image` results are converted to vision-model text instead of loading images into the main context (closes #103): the loop sends each data-URL result to the vision-capable provider (`VisionModel` role) and returns the model's text description as the tool result — `[view_image: <description>]`. The main messages never contain image bytes, so the main model no longer flips to the vision fallback for the rest of the turn, per-iteration image re-sends disappear, and unique image blobs stop breaking the provider prompt cache. The tool takes an optional `task` argument to steer the analysis (critique / describe / OCR); a failed vision call degrades to an error tool result. (Why: an image-heavy run measured 16 of 24 calls flipping to the vision fallback after the first image, re-sending 30-60k tokens of image bytes per run — the vision model is now an ephemeral one-shot description service, not a replacement main brain.)
### Changed
- deploy.sh no longer resurrects the removed external playbook dispatcher (closes #74 follow-up): the mino-playbook-dispatcher.{service,timer,sh} and mino-playbook-runner.sh files are deleted and the install/disable steps are gone — the in-process scheduler is the only scheduler. (Why: every deploy re-installed dead systemd units and scripts that the ponytail audit had just removed, recreating the clutter on each release.)
- AGENTS.md now mandates graph+codegraph navigation: agents must run `graphify query`/`path`/`explain` and `codegraph query`/`explore` before reading code, read only the line ranges the graphs surface, and re-index (`graphify update .`, `codegraph sync`) after modifying code. (Why: two navigation indexes existed and were going unused — agents were grepping the tree cold; the graphs cut token cost and find cross-file relationships grep can't.)
- Living Field region labels are now camera-bound landmarks (closes #113): Now, Work, Memory, Routines, and System move with the map, reconcile their visible-node counts against every current universe snapshot, lead at overview scale, recede during close inspection, and focus their matching lens when selected; phones start at an overview fit that keeps the complete geography between the field controls. (Why: fixed screen-space labels stayed behind while the universe panned and zoomed, leaving dense fields without a dependable sense of place.)
- Text tool-call parsing is hardened and self-diagnosing (closes #110): hand-written `[tool_call: name({...})]` args now survive markdown code fences, single-quoted strings, and unquoted keys via try-in-order lenient repairs (valid JSON is never transformed); the trace/log now carries a bounded snippet of any marker that still fails to parse. (Why: parse-failure noise cost 1-5 extra iterations per run and 543 events on 2026-08-08 — and the failing marker's shape was never logged, so the exact model sloppiness was unobservable.)
- `view_image` gains curated vision prompts for its task argument (closes #109, T8 follow-up from #103): critique/review/assess/judge map to a verdict-oriented critique prompt (PASS/REJECT + the single most important fix), OCR/extract/transcribe to a verbatim text-extraction prompt, describe/description to a factual description prompt; empty tasks keep the original describe-for-critic prompt and unknown tasks keep the free-form wrapper. (Why: the image-critique loop before publication depends on the vision prompt's verdict quality, and OCR of scanned pages needs extraction discipline — one generic prompt served neither well.)
- cost-watch extension no longer tracks or swaps to glm-5.2 (closes #107): the hardcoded defaults dropped the GLM model entry and the swap chain is now luna-pro → qwen; docs, swap-tool descriptions, and the price-spike test re-targeted. (Why: GLM's promo pricing is ending — the cliff is ~7× the promo rate — and the live stack no longer uses GLM anywhere (small model moved to deepseek-v4-flash-0731), so a cost_watch_swap could have swapped the primary model back onto the promo gamble.)
### Fixed
- Scheduled-run health counters use the fire time instead of a second wall-clock read (fixes the CI date-bomb): `fireSchedule` already receives `at` from the dispatcher, but `alertScheduleHealth` was called with a fresh `time.Now()` — two clocks in one function meant the streak-day test passed on 2026-08-10 and failed on the 11th when midnight rolled the day over between the two calls. The counters now key on the same instant the dispatcher fired (catch-up fires included). (Why: a test that passes today and fails tomorrow is a time bomb, not a test — the failure appeared as a red CI on master with no code change behind it.)
- Real spend capture (closes #76): `logUsage` records the provider-reported `cost_usd` from OpenRouter's usage object into usage.jsonl when present; `usageCost` (the $2/run and $25/month trigger pricer) prefers the real cost and falls back to the policy table only for providers that omit it — records with neither stay unpriced and counted. The weekly-cost playbook totals the recorded cost instead of a fixed table. (Why: the fixed price table made the weekly report fiction for any model outside it — an observed $138-vs-$5 divergence — while OpenRouter returns real `usage.cost` in every response; the harness had the truth in its hand and dropped it. The table survives only as a fallback, never as the source of truth.)
- Embedding recall merges paraphrases again (closes #141): the similarity gate in `entryRanking` dropped from 0.5 to 0.4, and the merge was extracted into a pure `mergeEmbeddingHits` (unit-tested without the API — 7-case table: close-paraphrase merges with a `similarity: 0.xx` signal, oblique queries stay filtered, stale vectors dropped, ranked facts boosted not duplicated, legacy sources mapped by content). Measured live: a natural close paraphrase scores 0.498 — above the old gate by 0.002 was below it — while oblique queries score ~0.27. (Why: the embedding path was mechanically sound (371 vectors, zero failures) but inert — the 0.5 gate sat exactly where useful matches live, so `remember` only surfaced near-verbatim phrasings keyword search already catches; an oblique live query returned nothing for a fact that has a vector. `mergeEmbeddingHits` joins the prompt-assembly seam list.)
- build-release.sh is now tag-only (closes #130, REL-05b): it refuses to build unless HEAD is exactly the requested tag (git describe --tags --exact-match) and the working tree is clean — a dirty tree 'at the tag' could still ship uncommitted drift, and a version label can no longer precede its tag. (Why: the v2.8.0 label ran on the VPS before the tag existed because the release was built from a local tree; the rule was decided in REL-05, now it is structural.)
- Tool results are capped with a head/tail preview mid-turn (closes #99): inline tool results are limited to 4,000 chars (was 8,000); larger results are artifacted with the first 2,000 + last 500 chars inline after the pointer. `read_file` is no longer exempt — live measurement showed eleven read_file results (up to 8k chars each, re-sent every iteration) dominating a 2.48M-token facebook run. (Why: ~93% of that run's input was re-sent accumulation; the cap shrinks iteration growth while the preview keeps the model oriented and the artifact keeps full fidelity.)
- Tool schemas are compacted in the request payload (closes #93): tool descriptions are capped at 1,000 chars (MINO_MAX_TOOL_DESC_CHARS overrides) and per-property prose (descriptions, defaults, examples, format) is stripped from parameter schemas while names, types, required, and nesting survive. (Why: composio MCP descriptions measured 261-8,770 chars each — 17.4k across seven tools — the dominant term in 28-schema chat turns at ~37k bytes. Validated against the production model: function-calling quality held or improved on composio search/execute/facebook scenarios at 40-56% fewer prompt tokens.)
- Stage prompts cap their total input budget (closes #96): rendered run inputs share a 20,000-char budget across all declared inputs (per-input 4,000 cap unchanged); inputs beyond the budget render an explicit omitted/truncated marker in declaration order. (Why: a stage declaring many inputs was unbounded in total — 10 × 4k = 40k chars — and the input section was the dominant controllable term in stage iterations, measured at ~10.6k chars on the VPS.)
- Tool trails are truncated in session history (closes #89): `[tools used:]` records in chat_log now store at most the first 500 chars of a tool result, with larger results written to the artifact store first and referenced by a `read_file` pointer — recoverable through the artifact catalog. (Why: session history is restored from chat_log on every session switch, so a long-lived session's 5-turn tail carried up to 8k chars of tool output per call; stage contexts reuse that same tail, making trail bloat the dominant term in playbook stage iterations — observed 24-26k input tokens per stage iteration on the VPS.)
- Declared workspace stage inputs now resolve (closes #86): Runtime sources render the run clock's local date, glob paths expand to their matches (newest first, each attributed with its path, bounded at the existing 4000-char input cap), and absolute declared paths resolve as-is instead of being double-joined under the playbook directory. An empty glob is a valid empty exclusion list ("No files matched."), not an error; only a genuinely broken literal path still renders `Unavailable:`. (Why: the ALL_PLATFORMS exclusion glob and the "authoritative local date" input declared by most playbook stages never resolved — `buildWorkspaceStagePrompt` read them as literal files, so the stage prompt always said `Unavailable`. An LLM's judgment on that prompt was nondeterministic: a scheduled daily post stage skipped a day because it "could not safely choose a non-reused angle" with the exclusion list missing, while the same prompt had been worked around on prior days. The harness now delivers what the contract declares; CONTEXT.md rules that "a missing input is not a skip reason" remain as defense-in-depth for genuinely broken paths.)
- run_playbook no longer forwards the main loop's tail-injected routing+clock into the stage request (closes #97): the injected "your first action MUST be run_playbook" guidance is meant for the main loop, but the stage prompt embedded it verbatim, so a stage model would obey it, find the tool whitelisted out of its toolset, and conclude it could not start — zero tool calls, "stage incomplete", no work done. The run request is now the clean user message. (Why: per-turn routing and the clock are appended to the last user message for provider prefix-cache stability; run_playbook read that whole message as its request. Manual runs of whitelisted playbooks were a lottery — a sibling playbook ignored the instruction and ran fine the same day.)

## [v2.7.2] — Living Field graph polish (2026-08-10)
### Fixed
- Dashboard conversation history could throw after its async session load because the event's `currentTarget` is unavailable after an `await` (#82). The menu now captures its anchor before loading and fails closed if it is no longer present. (Why: a chat control must not turn a delayed data read into an uncaught browser exception.)

### Changed
- Refined the Living Field's visual hierarchy around deterministic memory blooms, quiet ambient relationships, and a small set of responsibility anchors (#84). (Why: a complete universe needs readable structure, not an equally loud point-and-line mesh.)

## [v2.7.1] — Living Field composition correction (2026-08-09)
### Changed
- Rebuilt the Living Field composition around an edge-to-edge universe canvas (#80). The map now owns the primary viewport while lenses, inspection, playback, and conversation remain lightweight overlays instead of inheriting the previous dashboard shell. (Why: the first release preserved too much of the old page hierarchy and reduced the approved living-map concept to a contained widget.)

## [v2.7.0] — Living Field (2026-08-09)
### Added
- Living Field universe contract (#78): the dashboard now has a compact read-only map of durable memories, responsibilities, routines, reminders, artifacts, conversations, skills, and tools, with chronological responsibility history and non-destructive cursor-based runtime events for playback and live activity. (Why: a map-first interface needs one truthful topology contract; runtime activity must remain ephemeral while verified state remains durable.)

### Changed
- Living Field is now the dashboard's primary shell (#78): the complete universe renders as deterministic relationship geography with Universe/Now/Work/Memory/Routines/System lenses, contextual edge emphasis, historical playback that rejoins live state at Now, real-time transient work paths, new-node blooms, a contextual inspector, and a chat workbench that reflows the desktop map while remaining a dedicated mobile surface. Legacy Today, Work, and Graph links resolve into their map lenses; detailed libraries stay reachable from the inspector. The retired runtime SVG is removed, and the default field polls only the compact universe contract. (Why: Mino's durable world should be the interface itself, without confusing in-flight activity for persisted truth or paying for the full diagnostic payload every five seconds.)

### Fixed
- Scheduled playbook runs could disappear without a trace (closes #74): the dispatcher was one serial goroutine, so a slow run starved every sibling's 1-minute fire window, and a run missed while the process was down was never caught up or recorded — no trace, no audit entry, no `LastError`. Each due schedule now fires in its own goroutine with the slot claimed synchronously (no double-fires; duplicate schedule rows cannot run the same playbook concurrently); a boot pass fires same-day misses late; older misses get `missed_at` in schedules.json, one Telegram notice via the outbox, a `schedule_missed` trace, and an audit entry. `list_schedules` renders the miss. Never-run schedules are not flagged (a fresh schedule is not a miss). (Why: a schedule that never fires and never reports it is the worst failure class — the work silently does not happen; the loop already surfaced fired-but-failed runs, the never-fired case had no representation.)

## [v2.6.2] — Operation-State Visibility (2026-08-09)
### Added
- OSV-04 validation cases (closes #70): table-driven "right store, right answer" suite — (a) a reminder question ("When was my last Arachem meeting?") is answered from the reminder store with zero calendar tool calls; (b) every mutating tool result carries destination + state (9-tool table, OSV-01 pattern); (c) outcome claims contradicted by this turn's tool results are corrected (9-case table over the OSV-03 claim surface). (Why: the map's measurable — "a reminder question never triggers a calendar search; what happened to the information is answerable from the tool result" — now proven by tests, not asserted.)
- Reminder tool descriptions now carry meeting/appointment/deadline trigger words so the schema-retrieval gate offers the reminder store for reminder questions (exposed by OSV-04 case a: the FTS catalog could not match list_reminders for "when was my last meeting" — the same retrieval gap behind the Arachem detour).
### Fixed
- schedule_playbook result now names schedules.json (OSV-01 pattern; the change was claimed in #71 but never landed — OSV-04 case b caught it).
- OSV-03 audit-backed outcome verification (closes #69): the loop now corrects an operation-outcome claim that contradicts this turn's own tool results before it reaches the user — a failure claim ("the edit was rejected") against all-successful tool results, or a success claim ("I fixed the file") against all-errored results, pushes back once with the actual tool evidence. Bounded: only when a claim is made, only in-memory tool results, no log scanning; stages are exempt (their output contract governs). Mixed tool results never trigger it. (Why: 2026-08-09 — Mino claimed its threads fix "was rejected" while the harness's records showed write_file succeeded; the harness is the operation's memory and must translate its own records into correction.)
### Changed
- System prompt carries the static state map (OSV-02 decision, shipped with OSV-03): stable subsystem→store truths — reminders → SQLite (NOT the user's calendar), facts → memories/*.md, working memory → working_memory.md, schedules → schedules.json, calendar → calendar_events + calendar.ics, skills → skills/<slug>/SKILL.md, replied-comments ledger → data/threads-replies/replied-threads.md, audit → audit.jsonl; dynamic state points at system_check. Cache-friendly: system prompt stays byte-stable across turns.
- OSV-01 tool results report destination + state (closes #67): every mutating tool result now answers "what happened and where it went" — create_reminder says "stored in system reminders (SQLite), NOT your calendar" (the sentence that would have prevented the Arachem 166K-token calendar detour), cancel_reminder reports status: cancelled, create_event names both stores (calendar_events SQLite + calendar.ics), save_note/manage_memory name the memories/<id>.md file, update_soul/create_skill/add_working_memory/add_pattern name their files, manage_playbook create and schedule_playbook name playbooks/ and schedules.json. Uniform pattern: success + destination + state. (Why: the LLM is a stateless reasoner; the harness must translate state into context — a result that says only "Reminder set" leaves the model guessing where its own operation put the information.)

## [v2.6.1] — Lenient Timestamp Parse (2026-08-09)

### Fixed
- Malformed `at:` timestamp no longer drops the whole fact (issue #65): the fact loads with zero time and warns once per process run instead of failing the parse and flooding the 5-second reconciler (407 `graph memory reconciliation error` warnings in ~10 minutes observed on the VPS — Mino's own daily-schedule fact was invisible to `remember`). The rebuild pass stamps a valid timestamp on the next write — self-healing. Non-timestamp parse errors stay strict. (Why: a fact with a wrong clock is still a fact; availability over strictness, decided by the owner.)
## [v2.6.0] — Memory Intent (2026-08-09)
### Added
- Why-expiry lifecycle (MEM-08, closes #63): the judgment pass (GLM 5.2, same call/bounds as MEM-02) now also judges whether a fact's why still holds — an explicit `"expired": true` per-fact flag archives the fact instead of writing it live; absence means still valid, so a partial response degrades without wrong expiries. Expired facts move to `memories/archive/` via the existing markdown-archive machinery (never deleted; inbound edges cleaned; embedding dropped) and stay answerable through `remember`'s automatic archive fallback — live results empty or thin (< 10, less than one strong signal word) trigger it, every hit is tagged `[archived]` and rendered with its why/body/rationale as usual. Active expiry: a `reject` on manage_memory archives immediately through the `Fact.Feedback` field (negative = outdated, no waiting for the sweep) and the tool says so. A daily Telegram digest lists everything archived (`id | subject | reason`) so the owner can dispute or restore without per-fact pings; a failed send keeps the queue for the next cycle (outbox pattern). (Why: a fact whose reason died is no longer a fact — but a wrong expiry call costs nothing if the fact stays answerable; the `arachem_meeting_reminder` case — meeting passed 7 Aug 2026 — is the first real archive once the VPS deploys: "when does the meeting happen" must still answer "Friday 7 Aug", marked as historical.)
- MEM-05 validation suite: table-driven cases proving "right fact, right time, right reason" against a synthetic store — topical query returns the right fact with a correct rationale, co-authored use_when intent fires, the active turn rescues a thin query, irrelevant query is rejected (closes #55). Expired-why rejection deferred to MEM-06.
### Changed
- `memoryTokenize` drops 2-letter tokens ("me"/"is"/"do") — they matched inside longer words ("home", "memory", "misbehaves") and produced false-positive recalls; the MEM-05 suite caught it.
- `remember` output carries each matched fact's `why`, `body` (flattened, ≤200 chars), and a deterministic `matched:` rationale — per-signal word breakdown (subject/body/why/use_when/your words/similarity) from the MEM-03 ranking, no second read_file needed to verify a pulled fact (MEM-04, closes #54). Neighbors in the graph tree stay lean (subject + relation only) to bound token spend; matched facts are capped at 3 starts as before.
- `remember` now ranks by a merged signal mix (MEM-03, closes #53): substring subject/body score (unchanged weights) + why/use_when word overlap with the query + overlap with the active turn, with embedding similarity (20×cosine) merged by score when the free ranking leaves room in the top-3. Embedding hits are no longer appended after substring hits regardless of quality; the remember tool now receives the user's turn via context. (Why: use_when/why are co-authored recall intent — a word there is worth a subject word; the destination is remember returning the right facts with their reasons, and MEM-04's rationale needs a defensible order.)
- `save_note` accepts an optional `why` field carrying the user's verbatim words (MEM-07, closes #58). The seed is stored on the fact and fed verbatim to the MEM-02 judgment pass as `USER WHY:` — no asking, no paraphrase. System prompt now tells Mino to pass the user's own words as `why` on save/remember. (Why: MEM-02's judgment side already consumed a USER WHY seed, but nothing could produce one — the seed path was dead code.)
- Judgment pass now writes `why` + `use_when` on every fact (MEM-02, closes #52). The existing graph judgment call (GLM 5.2, bounded 5/pass incremental + full ~6h rebuild) now returns per-fact `why` (refines the user's seed when present, distills from content when absent) and `use_when` trigger phrases (situations where the fact should be recalled) alongside the edges. Same model call, same bounds, same failure semantics — a partial response (edges without facts meta) degrades without wiping existing why/use_when. (Why: `Fact.Why` was a schema ghost — never written, never surfaced; the memory-intent work needs both fields populated before remember ranking can use them.)
- Graph rebuild preserves fact `Source` provenance instead of stamping every fact `graph-rebuild`. (Why: 296/303 production facts lost their origin — the why work depends on provenance surviving.)
- Dedup merges keep the survivor's `why`/`use_when` instead of dropping them.

## [v2.4.0] — Quarantined Outputs (2026-08-08)

### Added
- Quarantined outputs: stage contracts may declare absolute output paths — enforced like any declared output, but resolved outside the playbook tree so they never enter the ALL_PLATFORMS glob or the distill queue. (Why: the threads-replies digest — the audit trail of what the agent replied publicly — was skipped 2/2 runs because prompt-level steps have no teeth, yet declaring it as a normal output would have distilled external comment text into memory. Absolute declared paths = enforced + quarantined.)

## [v2.4.1] — Parse-Failure Circuit Breaker (2026-08-08)

### Fixed
- Loop circuit-breaker for repeated unparseable tool calls: 3 consecutive parse failures escalate the corrective push (FLAT variant / native calls only), 6 abort with a clear diagnosis instead of burning to the iteration cap. (Why: the 13:00 facebook run on 2026-08-08 burned 16 consecutive iterations on the same broken text-marker JSON — the identical push never helped, and the run died with a useless `iteration_limit`, no post, routine blocked. A success between failures resets the counter.)
### Added
- Quarantined outputs: stage contracts may declare absolute output paths — enforced like any declared output, but resolved outside the playbook tree so they never enter the ALL_PLATFORMS glob or the distill queue. (Why: the threads-replies digest — the audit trail of what the agent replied publicly — was skipped 2/2 runs because prompt-level steps have no teeth, yet declaring it as a normal output would have distilled external comment text into memory. Absolute declared paths = enforced + quarantined.)
### Fixed
- Playbook stages with verified outputs now complete even when the final model call flakes. (Why: the 09:30 threads-daily-capability run on 2026-08-08 published the post and wrote its log, then was marked failed by `all vision providers failed: empty model response` on the wrap-up call — the Telegram report was lost and the routine went blocked. A stage's contract is its verified outputs, not the model's last word. Cancelled runs and missing outputs still fail.)
- Chat loop pushes back once when the agent claims a state change it never executed. (Why: on 2026-08-08, asked to remove a memory fact, the agent replied "Consider it deleted" with zero tool calls — the file still existed, and it doubled down when confronted. The loop now detects mutation requests (delete/remove/forget + object) answered with completion claims but no tool calls, and injects one corrective push. Chat turns only; stages stay governed by their output contract.)
- Bash tool results for non-zero exits now lead with the output. (Why: three behavioral probes on 2026-08-08 showed the agent reading `Error: exit status 1` as proof of absence while the file path sat in the Output field — `find` exits 1 on unreadable dirs while still printing matches. Error results now read `PARTIAL: command exited with error N but produced output — read it before concluding anything: <output>`.)

### Added
- `assets/playbook-authoring.SKILL.md`: new "Battle-tested patterns" section covering scheduling mechanics (schedules.json, day-gating, mino-user ownership), social-playbook patterns (ALL_PLATFORMS cross-exclusion, vision critique loop, judgment gates), external-data quarantine (playbook outputs distill into memory and feed the cross-playbook glob; external text belongs in `~/.mino/data/`), and fail-fast rules for flaky external tools. (Why: these patterns were hard-won from production failures on 2026-08-07 — the stopped-routine schedule death, the reddit 50-iteration grind, the spam-poisoning vector, and the root-owned playbook landmine — and the skill previously taught an outdated config.md scheduling mechanism that the runtime does not use.)

## [v2.3.1] — Routine Schedule Recovery (2026-08-07)

### Fixed
- Routines closed with status `stopped` can now be restarted by their scheduled fire. (Why: a manual close during the provider/cache migration cleanup on 2026-08-06 left three night routines in `stopped`, and `validResponsibilityTransition` treated `stopped` as terminal — every scheduled fire since died with `cannot move responsibility from "stopped" to "working"`, silently killing ai-news-daily, malaysian-news-daily, and facebook-daily-ai-post. The schedule had no recovery path. Routines are recurring by nature: closed = paused, next fire restarts. One-off responsibilities remain terminal.)

## [v2.5.1] — cost-watch Go Port (2026-08-09)

### Changed
- `extensions/cost-watch` ported from python to Go — a single static binary, no runtime dependencies (issue #47). Same tools, protocol, config shape, and behavior; systemd units point at the compiled binary. This removes the only non-Go extension from the family (threads, minowrap, fileingest are all Go binaries). The parser also tolerates extra pricing keys on the luna-pro page (input_cache_write, web_search between input_cache_read and discount).

## [v2.5.0] — The Price Guardian + Dashboard Redesign (2026-08-09)

### Added
- `extensions/cost-watch` — the price guardian: scrapes OpenRouter model pages, alerts on Telegram when a promotional price expires (best price > expected × threshold), and swaps providers.json down the model chain with backup + atomic restart. Exposes `cost_watch_status` / `cost_watch_check` / `cost_watch_swap` tools via the extension protocol — the first extension in the mino extension ecosystem that watches mino's own wallet.
- Dashboard redesign: Nowfield work surface, conversation workbench, truthful artifact actions (the operator-timeline direction).

### Fixed
- write_file/edit_file reject stray relative `.mino` prefixes with the corrected absolute path. (Why: the VPS reddit karma-log run on 2026-08-09 wrote to a relative `.mino/playbooks/...` path — it resolved via CWD to the right location, but the recorded arg path never matched the declared output, so stage-output attribution failed and the run went blocked. The doubled-`.mino` guard already existed; this closes the relative-prefix escape hatch — the fourth occurrence of the model mangling `.mino`-prefixed paths.)
- `run_playbook` rejects re-running the playbook it is already inside (corrective error, no recursion). (Why: 2026-08-09 the threads-community stage skipped itself with "run_playbook was unavailable" — the model tried to re-run the playbook from inside its own stage and treated the blocked tool as a skip reason. Stages with no tool whitelist would have allowed the recursion outright; the guard closes it. Cross-playbook delegation and chat calls are unchanged.)

### Fixed
- `run_playbook` rejects re-running the playbook it is already inside (corrective error, no recursion). (Why: 2026-08-09 the threads-community stage skipped itself with "run_playbook was unavailable" — the model tried to re-run the playbook from inside its own stage and treated the blocked tool as a skip reason. Stages with no tool whitelist would have allowed the recursion outright; the guard closes it. Cross-playbook delegation and chat calls are unchanged.)

### Added
- `extensions/cost-watch` — the price guardian: scrapes OpenRouter model pages, alerts on Telegram when a promotional price expires (best price > expected × threshold), and swaps providers.json down the model chain with backup + atomic restart. Exposes `cost_watch_status` / `cost_watch_check` / `cost_watch_swap` tools via the extension protocol. (Why: the provider stack runs on promos — GLM 5.2 at 93% off, luna-pro at 50% off — and an expired promo silently multiplies the bill 7-20×. The scraper is deterministic, no LLM, no API key; the restart defers during in-flight playbook runs.)
### Added
- Dashboard route-parity coverage now inventories the registered HTTP surface, primary and legacy hash navigation, and polling-preservation behavior before the Nowfield replacement. (Why: a full shell redesign must fail tests when a route, endpoint binding, deep link, or interactive state silently disappears.)
- Promo-ready PNG archive preserves every redesign concept and the rendered Nowfield desktop, tablet, mobile, and focused-Responsibility states. (Why: the selected interface and its design exploration should remain reusable for launch material without depending on ignored scratch files.)
- README: new "Recommended installs", "A task failed — now what?", and "Power tuning" sections. (Why: the README covered install/config/architecture but never told a new user which helper binaries are worth installing (rtk, markitdown, composio MCP), what to do when a turn dies with `(stopped after N iterations)`, or which env knobs to raise for more power — all verified against the VPS deployment and the code defaults.)
- `.env.example` and README config table now match the code default `MINO_MAX_TOKENS=16384`. (Why: the example shipped 4096 while `config.go` defaults to 16384, misleading users tuning output length.)

### Changed
- Today and Work now render as the full-width Nowfield surface: real Responsibility events form Past, current status forms Now, and recorded action, owner, deadline, or schedule forms Next; each Responsibility title opens the same time geometry above immutable history and evidence. (Why: the former centered editorial stream wasted wide-screen space and disconnected current ownership from its proof and next action.)
- Dashboard conversation now opens as a resizable 72/28 workbench that reflows the surface above it, with a five-line auto-growing composer, explicit Ctrl/Command+Enter submission, contextual Evidence/Actions/Links, maximize and collapse controls, and a dedicated mobile state. (Why: the former floating chat covered live content and its single-line field made long messages hard to compose, reread, and safely submit.)
- Artifact controls now use authorized, browser-native actions: folders open the VPS Files view, files open through a read-only endpoint, and Copy path/Download actions expose recovery feedback. (Why: the previous `/api/reveal` returned `{}` for every target, making visible Open folder/file controls dead on a headless VPS.)
- Removed the unsupported dashboard microphone and browser recorder until a real transcription endpoint exists. (Why: the control posted to an unregistered `/api/voice` route and could only promise a 404.)

### Fixed
- Conversation workbench minimize now hides the dock and exposes a keyboard-accessible reopen control, returning focus to that control after close. (Why: the previous close action only collapsed the dock to a 64px composer row, so the chat still occupied the dashboard surface and felt impossible to minimize.)
- Memory graph canvas now seeds nodes in a deterministic spread and caps force-layout velocity, preventing the dashboard graph from exploding and jittering during initial render. (Why: the 224-node live graph started in a tight random cluster, producing extreme all-to-all repulsion even after the animation loop learned to cool.)
- Distill responses carrying the prompt template's placeholder text (`snake_case_id_prefixed_ep_`) are rejected instead of becoming facts. (Why: 2026-08-07 the model copied the example ID verbatim, creating 7 facts with template-text IDs — one became a god node with 16 edges. The prompt example now shows a realistic ID (`ep_ai_news_daily_20260805`) and `parseDistillResponse` rejects any response containing the placeholder, in the run ID, subject, or fact IDs. The 7 poisoned facts on the VPS were renamed to clean `ep_*` IDs with edge targets rewritten across all memory files.)
- Background goroutines (schedule dispatcher, consolidation, dedup, graph maintenance, outbox, reminders, alerts, audit pruning, graph refresh) now run under a panic guard: one panic no longer kills the whole agent. (Why: the audit found zero `recover()` in the codebase against 13+ background loops — a single panic in any dispatcher would take down in-flight sessions and today's schedule until systemd restarted mino.)
- Malformed **native** `tool_calls` arguments are no longer executed with nil/garbage input: `provider.go` logs the raw string and injects `__raw_arguments__`, and the loop returns it to the model with a corrective message instead of running the tool. (Why: this is the sibling of the text-marker `\'` bug — a model emitting unparseable native arguments would have executed a tool with `Input: nil`, producing confusing errors. The dead `hasInvalidToolInput` guard is now superseded by this path.)
- Playbook stages now have a rewrite-drift tripwire: the same output path rewritten 6+ times consecutively (same tool, same path) injects a corrective push ("read it back with read_file, verify, then finish") instead of letting the run burn the full iteration cap. (Why: 2026-08-07 the reddit-karma-builder stage 1 rewrote candidates.md 26× because its tool whitelist lacked read_file — the model couldn't verify its own output, so it kept regenerating it blind until the 50-iteration cap killed the run at 50 minutes. Interleaved write/read sequences are untouched; the loop detector stays skipped inside stages for legit repeated search_web.)
- Also added `read_file` to reddit-karma-builder stage 1's whitelist (VPS config) so the model can verify its output.

## [v2.3.0] — Stage Contract Enforcement & Prompt-Cache Stability (2026-08-07)

### Fixed
- Playbook stages can no longer end "complete" without their declared outputs: the stage loop now pushes the model to write missing outputs (and re-emit unparseable text tool-call markers) instead of silently declaring done. (Why: 2026-08-07 the threads-ai-learning playbook's publish stage actually posted to Threads, then emitted its final tool call as a text marker with shell-style `\'` escapes inside the JSON args; `extractTextToolUses` dropped the call on `json.Unmarshal` failure with no log, the loop read "no tool calls = done", and the run failed with `required output "output/result.md" was not written` — the post succeeded but the report/record tail never ran. The parser now repairs common model JSON sloppiness (`\'` → `'`, trailing commas) before strict parse, reports found-but-unparseable markers so the loop pushes a corrective message, and the loop refuses to complete a stage turn while declared outputs are missing. The system prompt also tells models to prefer native function calling over text markers.)
- Loop detection no longer flags same-name tool streaks whose args differ substantially (enumeration = progress). (Why: 2026-08-06 an audit of all 7 playbooks called `manage_playbook` 7 times with different playbook names and was flagged "7 consecutive calls without progress". The same-name signal exists for real drift loops (composio steps, metrics) where args stay similar, so it now only counts entries whose args share a long common prefix — drifting args still trip it, distinct-entity enumeration does not.)
- Stop/cancel now matches any message beginning with "stop"/"cancel"/"halt" (e.g. "mino stop with the playbook"), not just exact phrases. (Why: 2026-08-06 the user's "mino stop with the playbook." was treated as a normal message, so mino kept re-investigating the playbook audit instead of stopping.)
- Dashboard chat now sends `session_id`, so "New chat" actually starts a fresh conversation instead of always hitting the "default" session. (Why: the default session context was full of the playbook-audit conversation, so every new message re-triggered it.)

### Changed
- Tool schemas are now selected ONCE per turn AND unioned monotonically per session (`SchemasForContext` takes sessionID; the selected tool set only grows within a session). (Why: even with per-turn freezing, the `tools` array drifted between turns — selection re-runs against each new user message (observed 28 schemas turn 1, 27 turn 2) — and since the provider's cache key includes the tools array, every drift invalidated the whole prefix. The monotonic union converges after a few turns and stays byte-stable, so cross-turn requests hit the cached prefix.)
- The system prompt is now byte-stable across turns: matched skills and playbook routing moved from `BuildSystem` into the user-message tail (via `BuildContext`), next to the clock. (Why: skills/playbook matches depend on the current user message, so the system prompt changed every turn and invalidated the entire prompt-prefix cache — the first bytes of the request are the system prompt. With the routing block at the tail, only the fresh user message misses; system + history stay cached across turns. Matching still runs every turn; only its position changed. This is the same cache-stability move already applied to the clock and to playbook system prompts.)
- Tool schemas are now selected ONCE per turn instead of every iteration, and OpenRouter is pinned to the DeepInfra upstream via `provider_routing`. (Why: usage.jsonl showed ~28-33% cache hit rate. Two causes: (1) `SchemasForContext` re-ran against the growing message history each iteration, so the `tools` array in the request drifted mid-turn and broke the provider's prompt-prefix cache (observed: iteration 2 cached 64/10671 tokens); (2) without routing, OpenRouter spread requests across upstreams (DeepInfra, Novita, Parasail, SiliconFlow, GMICloud), each with its own cold cache. DeepInfra bills cache reads at $0.018/M vs $0.09/M prompt — 5x.)
- Raised playbook stage iteration cap from 15 to 50 (`maxStageIterations`). (Why: 2026-08-06 the malaysian-news-daily and ai-news-daily playbooks failed their scheduled runs with `iteration_limit` — the stage's legit work (research + publish + telegram + image-gen steps) needs more than 15 tool calls, and the cap kept marking completed runs as failed.)

### Added
- Dashboard: failed playbook runs now list under each playbook with a delete button (`/api/memory` `delete_run`). (Why: 2026-08-06 the malaysian-news-daily and ai-news-daily runs failed with iteration_limit; there was no way to remove the failed runs from the UI.)

## [v2.2.0] — Cache Fixes, Loop Hard-Stop & Threads Reliability (2026-08-05)

### Changed
- OpenRouter requests no longer send `session_id`; it was empirically breaking DeepInfra prompt caching for `deepseek/deepseek-v4-flash-0731` (identical prefixes alternated hit/miss with a session id, 100% hits without it — OpenRouter's session pinning spreads requests across upstream replicas and defeats the prefix cache). Cache reads bill at $0.018/M vs $0.09/M prompt (~5x). (Why: only 10% of input tokens were cached; verified via live API tests that removing `provider_routing` (already done) plus dropping `session_id` yields reliable cache hits on repeated prefixes.)
- Loop detection now hard-stops the turn after 3 consecutive detections (status `loop`, reply hands back to the user) instead of only injecting advisory prompts. (Why: 2026-08-05 mino ignored 8+ "try a different approach" prompts while investigating a dead-end Threads extension lead and burned ~200k tokens; advisory prompts alone don't stop a stuck agent, and an iteration-limit stop is worse because it reports the wrong status.)

### Fixed
- Threads extension: `threads_publish` now retries once (fresh container, 5s) when Meta answers "The requested resource does not exist" on a freshly-created container (Meta-side propagation race — hit twice on 2026-08-05, breaking both scheduled Threads playbooks), and container creation now fails loudly on non-200/empty-ID responses instead of silently publishing with an empty `creation_id` (which produced the same misleading Meta error). (Why: the playbook contract forbids agent-level retry, so the tool must absorb the transient race; a bounded retry is safe because Meta only returns that error when it never saw the container, so no duplicate posts.)

### Added
- `generate_image` now falls back to OpenRouter (`google/gemini-3.1-flash-lite-image`, ~3.4¢/1024²) between Cloudflare and Pollinations when `MINO_OPENROUTER_KEY` is set, so image generation keeps working when the Cloudflare free neuron quota is exhausted. (Why: the free Cloudflare tier caps at 10k neurons/day; OpenRouter needs no new account — the existing key works — and quality beats the keyless Pollinations fallback.) Model overridable via `MINO_OPENROUTER_IMAGE_MODEL`.
- Pollinations fallback (last-resort image path) now requests the `flux-realism` model for better quality.

## [v2.1.0] — Operator Timeline & Image Generation

### Added

- Onboarding collects optional Cloudflare Workers AI credentials (`CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`) alongside the existing Telegram/Tavily fields, so image generation is configured at first setup instead of by hand-editing `mino.env`.
- `generate_image` now renders via Cloudflare Workers AI (free tier, `MINO_IMAGE_MODEL`, default `@cf/black-forest-labs/flux-1-schnell` ≈ 0.4 neurons/image, `@cf/leonardo/phoenix-1.0` switchable at ~100× the neurons) with Pollinations.ai as automatic fallback. Keys are `CLOUDFLARE_API_TOKEN`/`CLOUDFLARE_ACCOUNT_ID`, dashboard-savable via `mino.env` like `TAVILY_API_KEY`; any failure (missing keys, HTTP error, bad response) logs and falls through to the existing pollinations path, which stays free and keyless. Response handling accepts both raw image bytes (phoenix family) and JSON envelopes with `image`/`images` (flux family), so switching `MINO_IMAGE_MODEL` needs no rebuild. (Why: pollinations is a shared free queue with inconsistent quality; Cloudflare gives dedicated free quota ~10k neurons/day with no credit card. Gemini free tier grants zero image quota to new accounts; the paid catalog — nano-banana, gpt-image-2, recraft, imagen — needs billing.)
- Operator Timeline dashboard shell: Today-first owner journal, grouped Work/Conversations/Memory/System navigation, contextual Ask, phone-specific bottom navigation, truthful freshness and health state, and redirects that preserve legacy dashboard hashes.

### Changed

- Onboarding `/api/settings` now merges into the existing `mino.env` instead of rewriting it, so unrelated keys (`CLOUDFLARE_*`, `THREADS_*`, `MINO_OPENROUTER_KEY`, ...) survive a re-onboard; keys are written sorted for diffable files. (Why: the old rewrite silently wiped every key not in the onboarding form.)

### Fixed

- Dashboard polling no longer rebuilds unchanged views: Graph, Settings, Playbooks, and other interactive surfaces preserve canvas, disclosure, focus, and form state while five-second data polling continues; obsolete graph animation loops stop after navigation.
- Conversation History now opens above the expanded Ask surface instead of rendering invisibly underneath it.

## [v2.0.1] — Memory Graph, Playbook Hardening & MCP Flattening

### Fixed
- Distill queue no longer jams on dead rows: an artifact whose file is gone (e.g. /tmp cleaned on reboot) is tombstoned (marked distilled) so the queue advances — previously it was re-selected every pass, blocking all newer artifacts (like playbook outputs) behind it forever.
- Deterministic maintenance steps (mirror cleanup, Louvain clustering, god nodes, labels) now run even when the edge rebuild had failed LLM batches — an empty-edge batch no longer starves communities forever; failed batches retry next cycle.
- Facts are only marked judged when a graph rebuild completes with zero failed batches; a partial failure leaves facts eligible for the incremental judgment pass instead of trusting a rebuild that didn't write everything.
- The 5-minute edge-judgment pass and the 6-hour graph rebuild could race on the same fact: the 5-min pass re-checks `JudgedAt` after its model call and skips its edge write + `MarkJudged` when the 6h pass already judged the fact, so the two passes' inferred edges no longer overwrite each other nondeterministically (the 6h write wins).
- Facts without embeddings were invisible to graph edge inference: rebuild now backfills missing fact vectors before candidate generation, so migrated facts can gain edges instead of staying orphaned forever.
- Graph-rebuild-written edges were mislabeled with `Source: "consolidation"`; edge provenance now follows the writer (`graph-rebuild` vs `consolidation`).
- Mirrored inferred pairs are now resolved for any relation: when A→B and B→A are both inferred, the lower-confidence edge is dropped and explicit edges always survive (previously only `supersedes` pairs were cleaned).
- Playbook authoring now canonicalizes generated semantic stage names, legacy output bullets, and Markdown-wrapped tool names at creation time before strict contract validation.
- Tool artifact files (`compactToolOutput`) now get a unique per-write filename (`tool-<unixnano>.txt`) instead of `tool.txt` in a reused turn directory. The turn dirs in `/tmp/mino/results/<session>/<turn>/` reset across days, so old files lingered in slots that got rewritten by later runs — the model could follow an artifact path from context and read a stale result as its own. This happened in production: Mino's Gmail scan calls (Composio `GMAIL_FETCH_EMAILS` with `query`) returned correctly filtered old promotions, but it then read a 30-min-old stale artifact from the same dir and concluded the tool had no query support, stopping the cleanup with a wrong diagnosis. Unique names make a stale file unable to masquerade as a fresh result.

### Removed
- Ponytail audit cuts (refactor pass):
  - Legacy SQLite `facts`/`episodes` tables, their FTS5 indexes, triggers, and column migrations dropped from the schema (the markdown graph is authoritative; fresh installs no longer create the dead archive tables). `MigrateLegacyFacts` still migrates pre-cutover installs — it now skips cleanly when the table is absent.
  - Hand-rolled JSON-schema subset validator (~100 lines) replaced with `santhosh-tekuri/jsonschema/v6` (already in go.sum via mcp-go, now a direct dep), compiled once per tool via `sync.Once`. Tool arg validation now covers the full schema, not just type/required/enum/items.
  - `Registry.Static()`: defensive full-registry copy was dead weight — the registry is never mutated during a run (only app boot registers tools). Playbook stages pass the registry through.
  - Embedding query LRU cache: speculative exact-string cache, no evidence of repeated identical queries.
  - `SetEmbedder` inline interface → concrete `*EmbeddingStore` (one implementation, one caller).
  - `parseAnthropicStream` text-block collapse: `blocks` duplicated `fullText`; unused `stop_reason` fields removed.
  - `safeEvalName` was an identity function — now actually sanitizes session IDs (prompt text was leaking raw into session/trace records).
  - Duplicate anonymous asset struct in `update.go` named once.

### Added
- `manage_playbook` lets Mino create, inspect, validate, update, and delete autonomous playbook definitions while protecting scheduled and resumable runs from invalidation.
- Memory self-maintenance: `manage_memory` gains `status`, `consolidate`, `dedup`, `rebuild_edges`, and `clean_edges` actions, so Mino can run the same maintenance passes the CLI subcommands trigger — on its live memory, by its own judgment. SOUL.md teaches the habit.
- Playbook Layer 3 references: stages can declare a `## References` section pointing at rule/convention files (voice, brand, domain conventions). The runner injects them into the stage prompt as constraints, resolved stage-dir-first then playbook-dir, hard-capped at 4K chars per stage. Missing references warn and are skipped — advisory by design, unlike tools/outputs which fail loud.
- Playbook stage contract validation: a stage that declares a `## Tools` list must include `write_file` when it declares an output (three incidents in a week came from stages that could never write their result), and every declared tool must exist in the registry (phantom names like `invoke_llm` are rejected at run time). Load fails loud instead of after 3 LLM attempts.

### Fixed
- Scheduled playbooks silently did nothing: two loader bugs meant the real work never ran. (1) `LoadPlaybook` only scanned top-level `NN-*.md` files, so playbooks authored with a `stages/` subdir (README.md + stages/search.md etc.) loaded README.md as a fake "stage 0" that "completed" by writing an execution plan — the 08:30/09:30/10:00 MYT runs all did this while reporting success. The loader now also scans `stages/`, orders stages by their declared `# Stage N` heading (or filename), and ignores README.md / PLAYBOOK_PROTOCOL.md. (2) `## Tools` bullets kept inline prose as the tool name (`- bash (to check existence)` → `"bash (to check existence)"`), so exact-match `Registry.Only` dropped every tool but the auto-appended read_file — the 08:00 threads-ai-learning run had only read_file and failed all 3 attempts. Tool lines now take the first token and `None` bullets are dropped (no restriction). Manual runs "worked" yesterday only because Mino did the work ad-hoc in chat before calling run_playbook; the files and binary were unchanged between manual and scheduled runs.

### Fixed
- Reminders never fired in dashboard/CLI mode: the dispatcher was started only inside the Telegram gateway (`RunTelegram`), so `create_reminder` rows stayed pending forever when Mino ran without a bot connection. The dispatcher now starts in `NewCore` for every gateway mode and delivers via the raw-HTTP `sendTelegramText` path (markdown + plain-text fallback) instead of a live bot instance, so it also survives bot-init failures.
- Reminder dispatch deadlocked on the first due reminder: `db.SetMaxOpenConns(1)` combined with an `UPDATE` issued while the due-reminder rows were still open held the only connection forever (rows auto-close only at EOF). The dispatcher now collects due rows first, closes them, then sends + marks delivered.

### Added
- Table-driven `TestDispatchDueReminders` (fake bot API): due → delivered, future → stays pending, no Telegram config → untouched, API rejection → stays pending.
- `TestQueryAuditToolWithToolErrors` guards the single-connection pool invariant: a nested query after a completed `rows.Next()` loop is safe.

### Fixed
- `/api/memory` still listed episodes from the dead SQLite `episodes` table (its writer was removed in the graph migration), so the memory view was permanently empty while `delete_episode` operated on graph IDs. Both dashboard readers now share a `graphEpisodes` helper over graph episodic facts.
- MCP connections leaked on shutdown: the bridge was a `NewCore` local, so `Core.Close()` could not stop it. The bridge is now stored on `Core`, closed on shutdown, and `Close` is idempotent (`sync.Once` + mutex).

### Changed
- Removed the orphaned tool-filter comment in `NewCore` (the feature lives in `loop.go`'s `SchemasForContext`).
- Removed dead code left by the graph migration: `SearchEpisodes` (zero callers, `episodes_fts` has no writer since the SQLite episodes table stopped being written) and its two `episodes_fts` INSERT/DELETE triggers, plus the write-only `telegramCore` global.

### Fixed
- JSON-mode LLM calls (consolidation, dedup, graph edge rebuild) failed on reasoning models like DeepSeek v4 flash: forced `response_format: json_object` makes them return `content: null` (the budget goes to reasoning), which surfaced as "empty model response" and silently stalled consolidation for days. The client now retries once without `response_format`; the tolerant JSON parsers extract facts from a normal reply.
- `parseResponse`'s reasoning fallback never reached `FinalText` (it read the original empty content instead of the fallback-applied text).
- Consolidation silent-failure paths: DB query errors and successful parses that produced no facts/episode (e.g. a template-echoing small model) returned 0 without logging, making a stalled consolidation look healthy. Both paths now log.
- Telegram delivery dead-end: `send_message` only drafted to the outbox and nothing drained it, so scheduled reports never reached Telegram (the 22:00 MYT run's report sat in `outbox/` undelivered). A new in-process outbox dispatcher drains drafts to the owner's Telegram every 20s, removes delivered files, retries without Markdown parsing on rejection, and logs each delivery to the trace.
- Playbook stage chaining: stage LLMs guessed the playbook base directory when resolving sibling outputs (stage 3 tried `/home/mino/playbooks/...` instead of `/home/mino/.mino/playbooks/...`, gave up, and wrote a BLOCKED output that passed verification). `buildStagePrompt` now anchors the playbook directory explicitly.

### Added
- Outbox delivery tests (send + drain + markdown fallback) via a fake bot API.

### Fixed
- Scheduled playbooks never ran: `startRoutine` created the responsibility record without kind/title/owner, so every fire failed with a validation error that landed only in journald. `startRoutine` now carries the required fields (mirrors `startOneOff`), and fire failures are surfaced in the trace, audit log, and a new `last_error` field in `schedules.json` (visible via `list_schedules` and `system_check`) instead of failing silently.
- Playbook stage output verification could never pass for absolute output paths: `outputPath` rebased every declared path into the playbook `output/` dir via basename and never expanded `YYYY-MM-DD` templates. It now honors absolute paths (validated at load time to stay under the home dir) and expands date templates in the configured location.
- Stage output parsing trusted first-to-last backticks in `## Write`, so an appended second bullet changed the verified output path (an LLM moved the verification goalpost mid-incident). Only the first backtick pair is now authoritative.
- `system_check` now reports the mino systemd service state, recent journald errors (the real log the LLM never knew about), and per-schedule fire failures.

### Added
- Table-driven tests for output-path resolution (absolute/relative/date templates), first-backtick parsing, load-time path validation, fresh-schedule `startRoutine`, and dispatch failure visibility.

## [v1.5.0] — The Owner Cockpit Release

Mino grows from a capable agent into a more truthful personal operating system:
it remembers durable knowledge, shows what it owns, reuses expensive context,
and keeps runtime-authored playbooks safe across deployments.

### Added
- Cross-platform release builds for Linux, macOS Intel, macOS Apple Silicon, and Windows
- GitHub Actions CI for tests, race checks, vet, and the tagged production build
- Explicit System, Light, and Dark dashboard themes with a calm stone/paper Light palette
- Channel-neutral explicit capability selection and trace-visible tool schema names for registered extension tools
- Compact Option B desktop top navigation with the chat surface kept secondary
- Responsibility-aware Overview command center with current attention, verified outcomes, workspace shortcuts, and a separate runtime inspection surface
- One-off playbook Responsibility lifecycle: explicit runs now record acceptance, truthful completion or blocked outcomes, and read-back Evidence
- Truthful Routine journal slice connecting scheduled playbook results to Today, Work, dedicated history, and read-back Evidence
- Authoritative SQLite Responsibility projections, append-only history, and idempotent schedule/reminder baseline migration
- Dashboard redesign decision record covering the authoritative responsibility journal, editorial frame, truthful health and empty states, adaptive density, dated navigation, portfolio and detail views, channel conversations, mobile behavior, canonical product language, and state-first delivery sequence
- Memory graph index cache (`index.json`): O(1) startup, skips re-parsing all .md files
- Edge validation: dangling edge targets filtered on write
- Lossless SQLite fact archive and collision-safe graph migration manifest
- Graph-backed memory management, bounded consolidation edge inference, and graph edge rebuild maintenance commands

### Changed
- README now documents Markdown-authoritative graph memory, OpenRouter Exacto routing, and provider configuration without credentials
- Markdown graph claims are now authoritative for semantic memory, dashboard views, correction, forgetting, confirmation, and search; SQLite facts remain read-only diagnostics
- Interrupt status checks may use read-only tool schemas and dashboard SSE keeps the response handler alive until the interrupt reply is delivered
- State-changing tools expose verification-oriented summaries; `system_check` reports schedules, reminders, playbooks, and crontab state
- Memory graph keeps the full knowledge map visible while queries highlight matching neighborhoods, relationship labels appear on demand, and nodes use a richer deterministic color palette.
- Graph edges carry provenance and confidence; legacy generic overlap edges are rejected
- History split from chat_log: LLM sees clean reply, consolidation sees full tool trail
- `remember` tool description tuned: single call sufficient, reduces redundant queries ~60%
- **Tool schema selection tightened:** essentials shrunk 15→8 tools (+3 family), semantic threshold 0.25→0.40, cap 12→8, MCP tools keyword-gated, embedding input narrowed to last turn. Schemas per turn: 30→21 (-30%), trivial-turn tokens: ~14.5k→~12k (-17%).

### Fixed
- One-off Responsibility titles now stay concise while preserving the full accepted request as the outcome
- Scheduled Routine completion history records the actual finish time instead of copying its start timestamp
- Graph recall traverses reverse relationships and excludes ambiguous or low-confidence inferred edges from normal context
- Default session guidance and compaction markers use the graph-backed `remember` tool
- MCP tool selection excludes semantic embedding and applies a stable deterministic cap
- Consolidation now requests JSON mode and tolerates wrapped model output; graph memory also reads legacy front matter with unquoted scalar colons.
- Graph edge rebuild now uses JSON mode and never erases existing edges on empty or invalid inference output.
- Current-time grounding now repeats the configured local clock on the fresh user turn so stale history cannot override it.
- Small-model OpenRouter routing can use a separate provider route, keeping background consolidation independent from the main model route.
- Small-model reasoning settings are independent from the main model, preventing consolidation output starvation on efficient models.
- OpenRouter provider preferences now allow fallback to the next compatible host when a pinned route returns an empty response.
- Empty consolidation results now remain pending for retry instead of marking chat rows complete.
- OpenRouter calls now use opaque session IDs for sticky prompt-cache routing, and embedding/query cache usage is reused locally.
- Deployment no longer copies local playbooks; VPS playbooks remain runtime-owned and Mino-edited definitions are preserved.

### Removed
- FTS5 supplement path in graph traversal (dead code since migration to .md files)

## [v1.4.0] — Cross-Platform Scheduler & Dashboard Polish

### Changed
- Playbook scheduler: replaced systemd dispatcher with in-process Go ticker.
  `schedule_playbook` now writes `~/.mino/schedules.json` instead of systemd
  unit files. Scheduler runs cross-platform (Linux, macOS, Windows) with zero
  external dependencies. Added `list_schedules` and `cancel_schedule` tools.
  Existing systemd dispatcher is disabled on deploy.

### Fixed
- Dashboard reply cards now strip `[tools used: ...]` annotation from displayed
  replies (turnCard, executionTurn, chatTurnCard were missing `stripTools`).

## [v1.3.0] — Playbook Architecture & Production Hardening

### Added
- Playbook system — numbered markdown stages with `## Read` / `## Do` / `## Write`.
  Filesystem is the executor. Mini-loop with retries per stage. Memory routes
  vague prompts to the right playbook via embeddings + FTS5.
- Built-in `playbook-authoring` skill so Mino can create, improve, validate,
  and resume filesystem playbooks.
- Show parsed playbooks beside skills in the dashboard Memory view.\- `schedule_playbook` tool — schedule existing playbooks through the external
  systemd dispatcher without hand-editing shell or unit files.
- External systemd timer and Telegram delivery runner for scheduled playbooks.
- Persistent one-time reminders with Telegram delivery, listing, and cancellation.
- Context-aware tool selection — essential tools on every turn, specialist tools
  retrieved via FTS5 and optional embeddings.
- OpenRouter provider routing support with configurable provider preference.
- Multi-provider LLM support with priority, fallback, and circuit breaking.

### Changed
- Pass the original user request into every playbook stage so routed workflows
  retain task-specific context.
- Run playbook stages through the canonical Mino runtime instead of a second
  playbook-specific LLM/tool loop.
- Rewrite architecture and handoff documentation around playbook selection,
  the canonical runtime, filesystem state, and human checkpoints.
- Integrated Mino logo into dashboard sidebar, favicon, and onboarding screen.
- Keep dashboard tool activity rows compact; full arguments collapsed until opened.
- Increased playbook stage iterations from 8 to 10 for complex workflows.
- Default reasoning effort set to medium for Xiaomi MiMo providers.

### Fixed
- Always include `read_file` in playbook stage tools so `## Read` sections work.
- Allow playbook stages to write derived summaries of untrusted web content while
  blocking execution of instructions found in that content.
- Inject Mino's configured local date and time into playbook stages.
- Let Mino choose whether to use a matched playbook instead of bypassing reasoning.
- Keep the canonical loop as reason → tool execution → observation without a
  second deduplication cache or repeated-call state machine.
- Accept an output created or updated by the current stage attempt even when
  the model reaches its iteration limit, while rejecting unchanged old output.
- Make deploy builds reproducible and fail deployment if the VPS binary hash
  differs from the just-built local binary.
- Make scheduled runners encode API JSON safely, select only output created by
  the current run, and avoid overlapping executions per playbook.
- Remove obsolete programmatic approval tools; use explicit human checkpoints.
- Remove unused completion tool/protocol remnants.
- Retire stale checkpoints on explicit stop and consume restart recovery only once.
- Reject empty provider responses so fallback/error handling runs correctly.
- Remove dead rollback wiring and stale comments from config and tools.
- Remove dead `sessions` table query from dashboard metrics.

## [v1.2.0] — Agent Intelligence Upgrade

### Added — New capabilities
- **Safety guards** (§8) — four-layer protection: workspace boundary check, SQL gate, git auto-commit before destructive bash, immutable audit log (`~/.mino/audit.jsonl`).
- **Fan-out** (§15) — `fan_out` tool spawns N parallel delegates with WaitGroup, aggregates results.
- **Onboarding flow** (§16) — provider button grid (ChatGPT OAuth, Claude PKCE, manual key), auto-open browser, Telegram optional. No config needed to start.
- **`mino eval` CLI** (§17) — reads `eval/cases.json`, runs against real LLM, judges behavior (not output), writes `eval_report.json`, exits 0/1 for CI.
- **Observability** (§18) — alerting (error rate + dead man's switch), `/health` + `/metrics` (Prometheus), cost-per-session tracking, Telegram notifications.
- **Context-aware memory ranking** (§19) — `contextBoost` signal in `scoreFact`. Formula: `0.45×sim + 0.20×imp + 0.15×rec + 0.10×fdbk + 0.10×ctx`.
- **Structured task plans** (§20) — `TaskStep` struct + `Plan` field in `TaskSnapshot`. LLM declares plan via `complete_task`, harness persists and feeds back on restart.
- **Extension retry detection** (§21) — trigram similarity on consecutive same-tool outputs, alert when stuck ≥3 calls.
- **Eval auto-generation** (§22) — two-tier confidence (`manual`/`auto`), dashboard thumbs-up endpoint, auto-harvest from completed tasks.
- **File rollback system** — `write_file`/`edit_file` snapshot originals, `restore_files` tool for session-level recovery.
- **Text-embedded tool calls** — models without native function calling can write `[tool_call: name({...})]` in text.
- **Startup message** — `Mino is ready! Open: http://localhost:7779` printed on launch.
- **Cross-platform builds** — `build-release.sh` produces binaries for linux, darwin, windows (amd64/arm64).

### Changed
- Delegate worker upgraded with more tools, context injection, and response caching.
- Batch skill embeddings in one API call instead of N.
- Time string moved from system prompt to user message for cache stability.
- `maxReadOnlyStreak` reads `MINO_MAX_READ_ONLY_STREAK` env var (default 5).
- Dashboard and Telegram run together when port is configured.
- Telegram reply-to messages and scheduler notifications linked into session context.

### Fixed
- LLM call hangs no longer block the loop (90s per-call deadline).
- Approval keywords expanded for Telegram friendliness ("yes", "go ahead").
- `reasoning_effort` passthrough for OpenAI-compatible providers.
- Delegate registered before tool filter so it's always available.
- Alert datetime format mismatch (SQLite vs RFC3339) — false silence alerts stopped.
- `gitCommitBeforeBash` no longer pollutes project repo during tests.
- `eval/cases.json` uses `MINO_HOME` path instead of CWD-relative.
- Missing `--ink-dim` CSS variable — onboarding buttons now visible.
- Default port unified to 7779 across all components.

## [v1.1.0] — Core Upgrade

### Added
- RTK integration: automatic Bash command rewriting for compact test, Git, search, and log output (RTK installs separately — falls back to plain Bash if not present)
- Optional SQLite-backed project state tools (`project_get` and `project_update`)
- `MINO_WORKSPACE` — universal local editing boundary
- `MINO_TIMEZONE` for authoritative local time in prompts, schedules, and calendar
- `MINO_MAX_HISTORY_TURNS` — cap chat history to last N exchanges (default 5, 0 = unlimited)
- New config knobs: `MINO_BASH_TIMEOUT`, `MINO_CODING_TIMEOUT`, `MINO_SYNC_TIMEOUT`, `MINO_CONSOLIDATE_LIMIT`, `MINO_TELEGRAM_CHAT_ID`
- Scheduler: input validation, atomic file writes, one-shot jobs, duplicate prevention
- Keyword-based tool filter fallback when no embedding store is available
- Deploy script (`deploy.sh`) with configurable `VPS_HOST`
- Coding skill: absolute paths and workspace-aware staging guidance

### Changed
- Richer loop: completion protocol enforcement, no-progress detection, improved tool hygiene
- Raised default model output ceiling to 16K
- SQLite driver: mattn/go-sqlite3 → modernc.org/sqlite (pure Go, no CGo, FTS5 built-in)
- Dashboard: responsive Runtime Spine replacing static Overview
- Tool budgets per-turn reduced to six core + eight relevant, lowering prompt overhead

### Fixed
- Stop repeated-tool loops: observations preserve tool call, status, and cache state; exact duplicate actions never re-execute
- Recover from output-truncated tool calls without malformed argument execution
- Keep paired project read/write tools available together during dynamic filtering
- Action receipts recorded for every tool result (identity, status, proof, cache state)

## [v1.0.0] — Initial OSS Release

### Added
- Runtime-enforced `complete_task` protocol
- VPS-safe ChatGPT/Codex OAuth login with automatic token refresh
- Dashboard: add/remove API-key providers, OAuth login controls
- Keyless providers (`api_key_env` can be empty for Ollama, LM Studio)
- Native coding agent: 10 discovery tools + phased coding skill
- Multi-edit support: `edit_file` accepts `edits` array
- Context7 MCP default (no API key required)
- `read_file` 16KB limit (up from 4KB)
- `minowrap`: universal tool adapter for self-extending tools
- `reload_plugins` tool for hot-reloading extensions and MCP configs
- Graphify architecture index with semantic community labels
- Vision-aware provider routing (`text_only` providers skip image turns)
- Telegram rich formatting: bold, code, fences, links, headings, tables
- Tool filter: embedding-based top-K selection per turn
- `EmbedBatch`: batch embeddings (86 tools in <2s)
- Approval system: `request_approval` + `resolve_approval` for destructive ops
- `LLMClient` interface — test seam for deterministic evals
- Eval test suite (9 tests): scripted fake LLM, zero API cost
- `view_image` and `generate_image` tools (Pollinations.ai, no key needed)

### Changed
- Agent loop continues past 3 tool calls; drives recovery until completion or blocker
- Telegram auto-continues until no-tool final reply
- Codex Responses omit `max_output_tokens` for ChatGPT backend compatibility
- Dashboard provider switching per session
- `deploy.sh`: builds + ships minowrap, seeds tools.json
- Telegram: 4000-char HTML-safe chunking + plain-text fallback
- `RunLoop` accepts `LLMClient` interface instead of concrete `*ProviderManager`
- Default soul includes SELF-VERIFY and tool truth rules

## [0.1.0] — Initial release

- Agent loop with tool discipline (reason → act → observe)
- SQLite + FTS5 memory with semantic search and consolidation
- Built-in tools: read/write/edit file, bash, calendar, notes, web search, image gen
- OpenRouter embeddings for semantic recall
- Telegram bot gateway
- Web dashboard with chat, memory, tools, files, database, ops tabs
- MCP bridge for stdio-based servers
- Extension protocol for HTTP-based external tools
- Skill loader (SKILL.md) with keyword and semantic matching
- Provider manager with priority, retry, fallback, circuit breaking
- Checkpoint/resume for task survival
- Scheduler for proactive prompts
- Pollinations.ai free image generation
