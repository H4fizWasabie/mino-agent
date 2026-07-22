# Graph Report - mino-oss  (2026-07-22)

## Corpus Check
- 50 files · ~50,967 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 728 nodes · 1502 edges · 32 communities (30 shown, 2 thin omitted)
- Extraction: 90% EXTRACTED · 10% INFERRED · 0% AMBIGUOUS · INFERRED: 145 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `ea9a0388`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- [[_COMMUNITY_Community 0|Community 0]]
- [[_COMMUNITY_Community 1|Community 1]]
- [[_COMMUNITY_Community 2|Community 2]]
- [[_COMMUNITY_Community 3|Community 3]]
- [[_COMMUNITY_Community 4|Community 4]]
- [[_COMMUNITY_Community 5|Community 5]]
- [[_COMMUNITY_Community 6|Community 6]]
- [[_COMMUNITY_Community 7|Community 7]]
- [[_COMMUNITY_Community 8|Community 8]]
- [[_COMMUNITY_Community 9|Community 9]]
- [[_COMMUNITY_Community 10|Community 10]]
- [[_COMMUNITY_Community 11|Community 11]]
- [[_COMMUNITY_Community 12|Community 12]]
- [[_COMMUNITY_Community 13|Community 13]]
- [[_COMMUNITY_Community 14|Community 14]]
- [[_COMMUNITY_Community 15|Community 15]]
- [[_COMMUNITY_Community 16|Community 16]]
- [[_COMMUNITY_Community 17|Community 17]]
- [[_COMMUNITY_Community 18|Community 18]]
- [[_COMMUNITY_Community 19|Community 19]]
- [[_COMMUNITY_Community 20|Community 20]]
- [[_COMMUNITY_Community 21|Community 21]]
- [[_COMMUNITY_Community 22|Community 22]]
- [[_COMMUNITY_Community 23|Community 23]]
- [[_COMMUNITY_Community 24|Community 24]]
- [[_COMMUNITY_Community 25|Community 25]]
- [[_COMMUNITY_Community 26|Community 26]]
- [[_COMMUNITY_Community 27|Community 27]]
- [[_COMMUNITY_Community 30|Community 30]]
- [[_COMMUNITY_Community 31|Community 31]]

## God Nodes (most connected - your core abstractions)
1. `BuildRegistry()` - 39 edges
2. `RunLoop()` - 28 edges
3. `ProviderManager` - 26 edges
4. `Tool` - 26 edges
5. `NewCore()` - 23 edges
6. `handleDataAPI()` - 22 edges
7. `esc()` - 21 edges
8. `ResponseWriter` - 18 edges
9. `Request` - 18 edges
10. `Memory` - 18 edges

## Surprising Connections (you probably didn't know these)
- `NewCore()` --calls--> `OpenBrowser()`  [INFERRED]
  app.go → oauth.go
- `makeWorkingMemoryTool()` --calls--> `AppendWorkingMemory()`  [INFERRED]
  tools.go → adapters.go
- `makePatternTool()` --calls--> `AddPattern()`  [INFERRED]
  tools.go → adapters.go
- `NewCore()` --calls--> `PruneRecentFixes()`  [INFERRED]
  app.go → adapters.go
- `makeWorkingMemoryTool()` --calls--> `PruneRecentFixes()`  [INFERRED]
  tools.go → adapters.go

## Import Cycles
- None detected.

## Communities (32 total, 2 thin omitted)

### Community 0 - "Community 0"
Cohesion: 0.08
Nodes (58): cosineSimilarity(), Tool, isNotFound(), makeCodegraphQueryTool(), makeCodegraphSyncTool(), makeGitDiffTool(), makeGitStatusTool(), makeGlobTool() (+50 more)

### Community 1 - "Community 1"
Cohesion: 0.06
Nodes (38): AuthStore, BotAPI, DB, Memory, ProviderManager, Registry, Scheduler, Settings (+30 more)

### Community 2 - "Community 2"
Cohesion: 0.07
Nodes (39): DB, EmbeddingStore, Memory, Mutex, ProviderManager, Settings, NewMemory(), Request (+31 more)

### Community 3 - "Community 3"
Cohesion: 0.12
Nodes (43): chatPending(), countRows(), databaseSnapshot(), DB, Request, ResponseWriter, handleActiveTasks(), handleAuthAPI() (+35 more)

### Community 4 - "Community 4"
Cohesion: 0.13
Nodes (39): ContentBlock, LLMResponse, Message, Registry, T, ToolDef, makeEvalTools(), makeTestHome() (+31 more)

### Community 5 - "Community 5"
Cohesion: 0.12
Nodes (21): ModelRole, ProviderConfig, providerFile, ProviderManager, ProviderOption, providerPreference, providerState, AuthStore (+13 more)

### Community 6 - "Community 6"
Cohesion: 0.06
Nodes (17): animateStage(), CHAT, DB_DESC, evQueue, lastFetch, memConsolidation(), micBuf, oauthProviders (+9 more)

### Community 7 - "Community 7"
Cohesion: 0.09
Nodes (25): artifactFromOutput(), CleanupArtifacts(), compactToolOutput(), compactUserInput(), Duration, prepareToolOutput(), safePath(), T (+17 more)

### Community 8 - "Community 8"
Cohesion: 0.10
Nodes (23): LoadAuthStore(), codexAccountID(), LLMResponse, Message, Client, Reader, ToolDef, parseCodexSSE() (+15 more)

### Community 9 - "Community 9"
Cohesion: 0.12
Nodes (21): AddPattern(), AppendWorkingMemory(), ftsTerms(), DB, Duration, Memory, LoadPatterns(), LoadWorkingMemory() (+13 more)

### Community 10 - "Community 10"
Cohesion: 0.11
Nodes (17): AuthEntry, OAuthEngine, OAuthProvider, pendingOAuth, tokenResponse, exchangeCodexToken(), AuthStore, Mutex (+9 more)

### Community 11 - "Community 11"
Cohesion: 0.14
Nodes (16): skCandidate, Skill, SkillLoader, usageEntry, EmbeddingStore, Mutex, Time, inputWords() (+8 more)

### Community 12 - "Community 12"
Cohesion: 0.16
Nodes (20): downloadTelegramFile(), chunkHTML(), escapeHTML(), formatPipeTables(), formatTelegramHTML(), BotAPI, ToolCall, renderPipeTable() (+12 more)

### Community 13 - "Community 13"
Cohesion: 0.15
Nodes (13): Mutex, ListActiveTasks(), NewCheckpointManager(), CheckpointManager, Conversation, SessionManager, TaskSnapshot, CheckpointManager (+5 more)

### Community 14 - "Community 14"
Cohesion: 0.16
Nodes (18): Core, RunDashboard(), Core, main(), runCLI(), updateCache, CheckForUpdate(), DoUpdate() (+10 more)

### Community 15 - "Community 15"
Cohesion: 0.24
Nodes (14): ContentBlock, LLMResponse, Message, streamTool, ToolDef, UsageInfo, Client, Reader (+6 more)

### Community 16 - "Community 16"
Cohesion: 0.23
Nodes (17): failCall(), Client, LLMResponse, ProviderManager, T, TestAllTextOnlyVisionFails(), TestCircuitOpenAndRecovery(), TestFallback() (+9 more)

### Community 17 - "Community 17"
Cohesion: 0.21
Nodes (9): Client, Mutex, Registry, NewMCPBridge(), toolSchema(), mcpActive, MCPBridge, mcpServerConfig (+1 more)

### Community 18 - "Community 18"
Cohesion: 0.15
Nodes (16): archSVG(), databaseOverview(), databaseTableView(), dbQueryView(), dbTable(), esc(), memSkills(), memSoul() (+8 more)

### Community 19 - "Community 19"
Cohesion: 0.16
Nodes (16): chatTurnCard(), executionTurn(), gateBadge(), gateSplit(), historicalCard(), mdInline(), memOverview(), money() (+8 more)

### Community 20 - "Community 20"
Cohesion: 0.13
Nodes (14): API key, Architecture, Commands, Configuration, Extensions, Free AI stack, License, Local LLMs (Ollama, LM Studio, vLLM) (+6 more)

### Community 21 - "Community 21"
Cohesion: 0.21
Nodes (13): addProvider(), delMem(), postJSON(), qFill(), refresh(), removeProvider(), runQuery(), saveFact() (+5 more)

### Community 22 - "Community 22"
Cohesion: 0.20
Nodes (11): applyStreamEvent(), closeSessMenu(), newChat(), openConversation(), render(), sendChat(), switchSession(), syncChatLogs() (+3 more)

### Community 23 - "Community 23"
Cohesion: 0.38
Nodes (9): ResponseWriter, ExtensionTool, interpolate(), loadTools(), main(), runCommand(), toExtensionTool(), writeError() (+1 more)

### Community 24 - "Community 24"
Cohesion: 0.33
Nodes (5): [0.1.0] — Initial release, Added, Changed, Changelog, [v1.0.0] — First stable release

### Community 25 - "Community 25"
Cohesion: 0.60
Nodes (6): Connect(), DB, migrateChatLog(), migrateEpisodes(), migrateFacts(), runMigrations()

### Community 26 - "Community 26"
Cohesion: 0.40
Nodes (4): Contributing, Project layout, Rules, Setup

### Community 27 - "Community 27"
Cohesion: 0.67
Nodes (4): encodeWAV(), micHint(), stopMic(), toggleMic()

## Knowledge Gaps
- **93 isolated node(s):** `Duration`, `embeddedDoc`, `Settings`, `DB`, `ProviderManager` (+88 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **2 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `NewCore()` connect `Community 1` to `Community 0`, `Community 2`, `Community 5`, `Community 7`, `Community 8`, `Community 9`, `Community 10`, `Community 11`, `Community 13`, `Community 14`, `Community 17`?**
  _High betweenness centrality (0.375) - this node is a cross-community bridge._
- **Why does `NewProviderManager()` connect `Community 5` to `Community 1`, `Community 2`?**
  _High betweenness centrality (0.107) - this node is a cross-community bridge._
- **Why does `BuildRegistry()` connect `Community 0` to `Community 1`, `Community 2`, `Community 4`?**
  _High betweenness centrality (0.104) - this node is a cross-community bridge._
- **Are the 12 inferred relationships involving `BuildRegistry()` (e.g. with `NewCore()` and `makeEvalTools()`) actually correct?**
  _`BuildRegistry()` has 12 INFERRED edges - model-reasoned connections that need verification._
- **Are the 12 inferred relationships involving `RunLoop()` (e.g. with `addDelegateTools()` and `TestBluffingDoesNotCreateArtifact()`) actually correct?**
  _`RunLoop()` has 12 INFERRED edges - model-reasoned connections that need verification._
- **Are the 20 inferred relationships involving `NewCore()` (e.g. with `NewEmbeddingStore()` and `PruneRecentFixes()`) actually correct?**
  _`NewCore()` has 20 INFERRED edges - model-reasoned connections that need verification._
- **What connects `Duration`, `embeddedDoc`, `Settings` to the rest of the system?**
  _93 weakly-connected nodes found - possible documentation gaps or missing edges._