# Changelog

## [Unreleased]
### Added
- `MINO_WORKSPACE` establishes a universal local editing boundary: local files are edited in place while remote files are staged, verified, and synchronized back once.
- Runtime workspace context overrides legacy hardcoded paths without overwriting customized skill files during upgrades.
- Explicit `MINO_TIMEZONE` (default `Asia/Kuala_Lumpur`) for authoritative local time in prompts, schedules, and calendar queries.
- `MINO_MAX_HISTORY_TURNS` (default 5): cap chat history to last N exchanges instead of unlimited char-budget.
  Cuts input tokens from ~37k to ~12k per LLM call. 0 = unlimited (old behavior).
- Keyword-based tool filter fallback when no embedding store is available (no OPENROUTER_KEY)
- Port OSS guardrails: tool hygiene descriptions, TOOL HYGIENE prompt, verifyFileClaims, untrusted content rule
- Port OSS refinements: pure-Go SQLite, loadEnvFile, embedded OAuth fallback, schema versioning
- Tavily key in dashboard onboarding, readEnvFile for mid-session key injection
- Drop DuckDuckGo fallback; web search now requires Tavily API key
- Coding skill guidance requiring absolute paths and the configured local workspace for new or staged projects.

### Changed
- Raised the default model output ceiling to 16K and taught the coding workflow to use local project copies and chunk large writes before syncing remote files.
- Reframed Overview as a responsive cognitive core: Mino's neural brain now illuminates live request, context, memory, tool, response, and trace paths while detailed telemetry remains in its dedicated views.
- SQLite driver: mattn/go-sqlite3 → modernc.org/sqlite (pure Go, no CGo, FTS5 built-in)

### Fixed
- Record a generic action receipt for every tool result (action identity, status, proof, and cache state), so the observe cycle can reuse successful evidence instead of repeating side effects.
- Recover from output-truncated tool calls without executing malformed arguments; validate required tool fields, reject empty Bash commands, and keep checkpoints anchored to the original task instead of recursively nesting resume prompts.
- Stop repeated-tool loops generically: observations now preserve the tool call, explicit `ok`/`error` status, and cache state; exact duplicate actions are never re-executed and three consecutive no-progress turns stop early.
- Restore Codex GPT-5.6 model and reasoning choices in the dashboard for existing VPS sessions without requiring a new OAuth login.

### Added
- Runtime-enforced `complete_task` protocol: ordinary model text is provisional and every gateway receives a final reply only after explicit completion or a genuine blocker
- VPS-safe ChatGPT/Codex login using native device-code OAuth, automatic token refresh, and the Codex Responses transport
- Dashboard controls for adding and removing API-key providers and starting OAuth logins
- **Keyless providers**: `api_key_env` can be empty in `providers.json` for local LLMs (Ollama, LM Studio) that don't require auth
- **Native coding agent**: 10 discovery tools (list_files, grep, glob, git_diff/status, graphify_query/explain/path, codegraph_query/sync) for language-agnostic codebase navigation
- **Coding skill**: auto-loaded phased workflow (understand→plan→edit→verify), overrides assistant STOP rule when active, mandates AGENTS.md read on first turn
- **Multi-edit support**: edit_file accepts `edits` array for multiple replacements in one call
- **Context7 MCP default**: auto-seeded config for up-to-date library documentation (no API key required)
- **read_file 16KB limit**: increased from 4KB for real source files
- **minowrap**: universal tool adapter — one JSON entry per tool, template args auto-generate JSON Schema, new tools appear instantly (Mino self-extends without restarts)
- **`reload_plugins` tool**: hot-reloads extensions.json and mcp.d/ on demand, discovers new tools without restart
- **`Reload()` on MCPBridge**: re-scans mcp.d/ for new server configs, skips already-connected servers
- Graphify architecture index with semantic community labels and refreshed CodeGraph metadata
- Vision-aware provider routing: `text_only` providers skipped for image turns; separate sticky bucket keeps text sessions on the main model
- Telegram rich formatting: bold, code, fences, links, headings, bullets, strikethrough, pipe tables as aligned <pre> (ported from Crow's pipeline)
- Tool filter: embedding-based top-K tool selection per turn — only relevant tools sent to the LLM (cuts context waste)
- `EmbedBatch`: batch embeddings in one request (86 tools in <2s vs 49s sequential)
- Approval system: `request_approval` + `resolve_approval` tools for destructive operations (delete email, rm files, etc.)
- Pending approvals injected into system prompt across all sessions
- SELF-VERIFY prompt rule: LLM checks "did I call the tool?" before replying
- `LLMClient` interface — test seam for `RunLoop`, enables deterministic evals
- Eval test suite (9 tests): scripted fake LLM client, zero API cost, catches bluffs and regressions
- `view_image` tool — loads images into LLM's visual context
- `generate_image` tool via Pollinations.ai (free, no API key)

### Changed
- Reduced the default per-turn tool budget to six core tools plus eight relevant tools, lowering prompt schema overhead.
- Agent completion rules no longer stop after three tool calls; failed and artifact-backed results now drive recovery until completion or a genuine blocker
- Telegram now keeps any reply accompanied by tool executions in the progress message and auto-continues until a no-tool final reply
- Codex Responses requests omit the public-API-only `max_output_tokens` field rejected by the ChatGPT backend
- Dashboard provider switching now selects Codex models and reasoning effort per session; Codex defaults to GPT-5.6 Sol with Luna for small-model work
- **deploy.sh**: builds + ships minowrap alongside Mino, seeds tools.json, configures systemd unit
- Telegram sends unified into sendTelegramReply: 4000-char HTML-safe chunking + plain-text fallback; notify path no longer truncates
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
