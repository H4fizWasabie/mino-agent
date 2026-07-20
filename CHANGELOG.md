# Changelog

## [Unreleased]
### Added
- **Native coding agent**: 10 discovery tools (list_files, grep, glob, git_diff/status, graphify_query/explain/path, codegraph_query/sync) for language-agnostic codebase navigation
- **Coding skill**: auto-loaded phased workflow (understand→plan→edit→verify), overrides assistant STOP rule when active, mandates AGENTS.md read on first turn
- **Multi-edit support**: edit_file accepts `edits` array for multiple replacements in one call
- **Context7 MCP default**: auto-seeded config for up-to-date library documentation (no API key required)
- **read_file 16KB limit**: increased from 4KB for real source files
- **minowrap**: universal tool adapter — one JSON entry per tool, template args auto-generate JSON Schema, new tools appear instantly (Mino self-extends without restarts)
- **`reload_plugins` tool**: hot-reloads extensions.json and mcp.d/ on demand, discovers new tools without restart
- **`Reload()` on MCPBridge**: re-scans mcp.d/ for new server configs, skips already-connected servers
- **`mino version`**: prints embedded version + platform
- **`mino update`**: downloads latest GitHub release binary, replaces self atomically (data in ~/.mino/ untouched)
- **Update notification**: checks GitHub releases API once/day, prints banner if newer version available
- **Schema versioning**: `_meta` table tracks schema_version, version-gated `runMigrations()` for future DB changes
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
- Dashboard live-system SVG redesigned as a technical runtime blueprint while preserving backend-driven nodes, links, counts, and stage animations
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
