# Onboarding Questionnaire

<!-- Agent instructions: this workspace's onboarding already ran (2026-08-30) — the answers
     are baked into shared/project-identity.md, shared/harness-invariants.md, and
     shared/decision-log.md. This file is kept as the record of what was asked and answered,
     per Pattern 8's "ask once, never again." Re-run only if the owner explicitly asks to
     reconfigure the pipeline itself (not a single feature — that's stage 01's job). -->

### Q1: Describe Mino in one sentence.
- Placeholder: `{{PROJECT_ONE_LINER}}`
- Files: `shared/project-identity.md`
- Answer: A model-agnostic agent harness with loops, context management, guardrails, playbooks, and a Telegram interface, built as a single Go binary with local SQLite state.

### Q2: Beyond loops, context management, guardrails, and Telegram, what capability areas does Mino cover?
- Placeholder: `{{ADDITIONAL_CAPABILITIES}}`
- Files: `shared/project-identity.md`
- Answer: Playbooks (autonomous repeatable workflows), memory (Markdown-authoritative graph memory with episodic/semantic split), an extension protocol (HTTP-based, for capabilities not every owner needs), and a web dashboard.

### Q3: What language is Mino written in?
- Placeholder: `{{PRIMARY_LANGUAGE}}`
- Files: `shared/project-identity.md`
- Answer: Go
- Derived: `stages/03-implement/references/code-conventions.md` points at `docs/coding-conventions.md`.

### Q4: What framework or codebase is Mino built on?
- Placeholder: `{{BASE_FRAMEWORK}}`
- Files: `shared/project-identity.md`
- Answer: none — this pipeline lives inside mino-oss itself. See the "Run this pipeline inside mino-oss, not a separate repository" entry in `shared/decision-log.md`.

### Q5: Where does Mino keep its state?
- Placeholder: `{{STATE_STORAGE}}`
- Files: `shared/project-identity.md`
- Answer: Local SQLite file, `~/.mino/state.db`, WAL mode, single connection.

### Q6: Which providers must Mino work with?
- Placeholder: `{{TARGET_PROVIDERS}}`
- Files: `shared/project-identity.md`
- Answer: Anthropic (Claude, OAuth), OpenAI/Codex (OAuth), GitHub Copilot, xAI/Grok, OpenRouter (fallback + routing).
- Note: Stage 04 verifies changed paths against at least two of these.

### Q7: Which interface surfaces does Mino expose?
- Placeholder: `{{INTERFACE_SURFACES}}`
- Files: `shared/project-identity.md`
- Answer: CLI, web dashboard, Telegram.

### Q8: What are the build, test, lint, and run commands?
- Placeholders: `{{BUILD_COMMAND}}`, `{{TEST_COMMAND}}`, `{{LINT_COMMAND}}`, `{{RUN_COMMAND}}`
- Files: `shared/project-identity.md`
- Answer: `go build ./...`, `go test ./...`, `go vet ./...`, `./mino`

### Q9: Where do the source root, changelog, and user docs live?
- Placeholders: `{{REPO_ROOT}}`, `{{CHANGELOG_PATH}}`, `{{DOCS_PATH}}`
- Files: `shared/project-identity.md`
- Answer: repository root (flat package structure), `CHANGELOG.md`, `docs/`.

### Q10: Is there a rule specific to Mino that no change may ever break, beyond the six already listed?
- Files: `shared/harness-invariants.md`
- Answer: None yet beyond the six listed (model agnosticism, loop termination, context managed, guardrails not optional, failure explicit, state local/inspectable, single binary/no framework).

### Q11: Is there anything already decided NOT to build?
- Files: `shared/decision-log.md`
- Answer: provider-specific feature flags; an embedded scripting language or plugin runtime in the core binary. See the "Do Not Build" table.

---

## After Onboarding

Stage 01 (`stages/01-intake/CONTEXT.md`) is the entry point for a new feature idea or
non-trivial bug. `status` shows pipeline progress for the feature currently in flight at any
time.
