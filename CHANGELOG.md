# Changelog

## [Unreleased]
### Added
- Approval system: `request_approval` + `resolve_approval` tools for destructive operations (delete email, rm files, etc.)
- Pending approvals injected into system prompt across all sessions
- SELF-VERIFY prompt rule: LLM checks "did I call the tool?" before replying
- `LLMClient` interface — test seam for `RunLoop`, enables deterministic evals
- Eval test suite (9 tests): scripted fake LLM client, zero API cost, catches bluffs and regressions
- `view_image` tool — loads images into LLM's visual context
- `generate_image` tool via Pollinations.ai (free, no API key)

### Changed
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
