package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// coding_tools.go — language-agnostic coding tools.
// All tools shell out to CLI binaries following the same pattern as runBash.

const codingTimeout = 30 * time.Second

func runCoding(binary string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), codingTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = "" // relative paths resolve from working directory
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ponytail: if primary binary not found, try fallback
func runCodingFallback(primary string, primaryArgs []string, fallback string, fallbackArgs []string) string {
	out, err := runCoding(primary, primaryArgs...)
	if err == nil {
		return trimOutput(string(out))
	}
	if !isNotFound(err) {
		return fmt.Sprintf("Error: %v\n%s", err, trimOutput(string(out)))
	}
	out, err = runCoding(fallback, fallbackArgs...)
	if err != nil {
		return fmt.Sprintf("Error: %v\n%s", err, trimOutput(string(out)))
	}
	return trimOutput(out)
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "executable file not found") ||
		strings.Contains(err.Error(), "no such file or directory")
}

func trimOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 12000 {
		s = s[:12000] + fmt.Sprintf("\n... (truncated, %d total bytes)", len(s))
	}
	if s == "" {
		return "(no output)"
	}
	return s
}

func makeListFilesTool() *Tool {
	return &Tool{
		Name:        "list_files",
		Description: "List files in a directory. Use to explore project structure before reading.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string", "description": "Directory path (default: current directory)"},
				"depth": map[string]any{"type": "integer", "description": "How deep to descend (default: 2)"},
			},
		},
		Fn: func(args map[string]any) string {
			dir, _ := args["path"].(string)
			if dir == "" {
				dir = "."
			}
			depth := 2
			if d, ok := args["depth"].(float64); ok && d > 0 {
				depth = int(d)
			}
			out, err := runCoding("find", dir, "-maxdepth", fmt.Sprint(depth), "-not", "-path", "*/\\.*", "-not", "-path", "*/node_modules/*")
			if err != nil {
				// fallback: ls -la (depth 1 only)
				out, _ = runCoding("ls", "-la", dir)
			}
			return trimOutput(string(out))
		},
	}
}

func makeGrepTool() *Tool {
	return &Tool{
		Name:        "grep",
		Description: "Search for a text pattern in files. Uses ripgrep (rg) if available, falls back to grep.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Text or regex pattern to search for"},
				"path":    map[string]any{"type": "string", "description": "Directory or file to search in (default: .)"},
			},
			"required": []string{"pattern"},
		},
		Fn: func(args map[string]any) string {
			pattern, _ := args["pattern"].(string)
			dir, _ := args["path"].(string)
			if dir == "" {
				dir = "."
			}
			return runCodingFallback(
				"rg", []string{"--no-heading", "-n", pattern, dir},
				"grep", []string{"-rn", pattern, dir},
			)
		},
	}
}

func makeGlobTool() *Tool {
	return &Tool{
		Name:        "glob",
		Description: "Find files matching a glob pattern. Use to locate files by name (e.g., '*.go', '**/*_test.go').",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern to match (e.g., '*.go')"},
				"path":    map[string]any{"type": "string", "description": "Directory to search in (default: .)"},
			},
			"required": []string{"pattern"},
		},
		Fn: func(args map[string]any) string {
			pattern, _ := args["pattern"].(string)
			dir, _ := args["path"].(string)
			if dir == "" {
				dir = "."
			}
			out, err := runCoding("find", dir, "-name", pattern, "-not", "-path", "*/\\.*", "-not", "-path", "*/node_modules/*")
			if err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			return trimOutput(string(out))
		},
	}
}

func makeGitDiffTool() *Tool {
	return &Tool{
		Name:        "git_diff",
		Description: "Show git diff (unstaged changes). Use 'staged' param for staged changes. Use after editing to verify what changed.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"staged": map[string]any{"type": "boolean", "description": "Show staged changes instead of unstaged (default: false)"},
			},
		},
		Fn: func(args map[string]any) string {
			staged, _ := args["staged"].(bool)
			var out string
			var err error
			if staged {
				out, err = runCoding("git", "diff", "--cached")
			} else {
				out, err = runCoding("git", "diff")
			}
			if err != nil {
				return fmt.Sprintf("Error: %v\n%s", err, trimOutput(out))
			}
			return trimOutput(out)
		},
	}
}

func makeGitStatusTool() *Tool {
	return &Tool{
		Name:        "git_status",
		Description: "Show git working tree status (short format). Use to see what files changed, are staged, or untracked.",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Fn: func(args map[string]any) string {
			out, err := runCoding("git", "status", "--short")
			if err != nil {
				return fmt.Sprintf("Error: %v\n%s", err, trimOutput(string(out)))
			}
			return trimOutput(string(out))
		},
	}
}

func makeGraphifyQueryTool() *Tool {
	return &Tool{
		Name:        "graphify_query",
		Description: "Query the knowledge graph for codebase questions. Use BEFORE grep or read_file — answers are token-efficient. Requires graphify-out/ to exist.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{"type": "string", "description": "Natural language question about the codebase"},
			},
			"required": []string{"question"},
		},
		Fn: func(args map[string]any) string {
			q, _ := args["question"].(string)
			out, err := runCoding("graphify", "query", q)
			if err != nil {
				return fmt.Sprintf("graphify query failed (is graphify installed and graphify-out/ present?): %v", err)
			}
			return trimOutput(string(out))
		},
	}
}

func makeGraphifyExplainTool() *Tool {
	return &Tool{
		Name:        "graphify_explain",
		Description: "Get a plain-language explanation of a concept/function/module from the knowledge graph.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"concept": map[string]any{"type": "string", "description": "The concept, function, or module to explain"},
			},
			"required": []string{"concept"},
		},
		Fn: func(args map[string]any) string {
			c, _ := args["concept"].(string)
			out, err := runCoding("graphify", "explain", c)
			if err != nil {
				return fmt.Sprintf("graphify explain failed: %v", err)
			}
			return trimOutput(string(out))
		},
	}
}

func makeGraphifyPathTool() *Tool {
	return &Tool{
		Name:        "graphify_path",
		Description: "Find the relationship or shortest path between two concepts in the knowledge graph.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source": map[string]any{"type": "string", "description": "Starting concept"},
				"target": map[string]any{"type": "string", "description": "Ending concept"},
			},
			"required": []string{"source", "target"},
		},
		Fn: func(args map[string]any) string {
			src, _ := args["source"].(string)
			tgt, _ := args["target"].(string)
			out, err := runCoding("graphify", "path", src, tgt)
			if err != nil {
				return fmt.Sprintf("graphify path failed: %v", err)
			}
			return trimOutput(string(out))
		},
	}
}

func makeCodegraphQueryTool() *Tool {
	return &Tool{
		Name:        "codegraph_query",
		Description: "Search for symbols (functions, types, methods) in the codebase. Use for finding definitions and references.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"search": map[string]any{"type": "string", "description": "Symbol or term to search for"},
			},
			"required": []string{"search"},
		},
		Fn: func(args map[string]any) string {
			s, _ := args["search"].(string)
			out, err := runCoding("codegraph", "query", s)
			if err != nil {
				return fmt.Sprintf("codegraph query failed (is codegraph installed?): %v", err)
			}
			return trimOutput(string(out))
		},
	}
}

func makeCodegraphSyncTool() *Tool {
	return &Tool{
		Name:        "codegraph_sync",
		Description: "Re-index the codebase for semantic search. Call after making code changes to keep the index fresh.",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Fn: func(args map[string]any) string {
			out, err := runCoding("codegraph", "sync")
			if err != nil {
				return fmt.Sprintf("codegraph sync failed: %v", err)
			}
			return trimOutput(string(out))
		},
	}
}

// seedBuiltinSkills writes default skill and MCP config files if they don't exist.
func seedBuiltinSkills(home string) {
	os.MkdirAll(filepath.Join(home, "skills", "coding"), 0700)
	os.MkdirAll(filepath.Join(home, "mcp.d"), 0700)
	os.MkdirAll(filepath.Join(home, "oauth.d"), 0700)

	// coding skill
	skillPath := filepath.Join(home, "skills", "coding", "SKILL.md")
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		os.WriteFile(skillPath, []byte(codingSkill), 0644)
	}

	// context7 MCP
	mcpPath := filepath.Join(home, "mcp.d", "context7.json")
	if _, err := os.Stat(mcpPath); os.IsNotExist(err) {
		os.WriteFile(mcpPath, []byte(`{"name":"context7","command":"npx","args":["-y","@upstash/context7-mcp"]}`), 0644)
	}

	// OAuth providers — seed from embedded bundle so dashboard shows login buttons
	os.MkdirAll(filepath.Join(home, "oauth.d"), 0700)
	entries, err := embeddedOAuth.ReadDir("oauth.d")
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			dst := filepath.Join(home, "oauth.d", e.Name())
			if _, err := os.Stat(dst); os.IsNotExist(err) {
				data, _ := embeddedOAuth.ReadFile("oauth.d/" + e.Name())
				os.WriteFile(dst, data, 0644)
			}
		}
	}
}

const codingSkill = `---
name: coding
description: "Full coding agent discipline — understand, plan, edit, verify. Use when writing, fixing, refactoring, or debugging code in any language."
triggers:
  - code
  - fix
  - refactor
  - debug
  - test
  - build
  - implement
  - bug
  - error
  - deploy
  - .go
  - .py
  - .js
  - .ts
  - .rs
  - .rb
  - .java
  - .c
  - .cpp
  - .sh
  - Makefile
  - Dockerfile
---

# Coding Agent

You are in coding mode. When this skill is active, the assistant-mode STOP rule ("after 3 tool calls, STOP") does NOT apply. Coding tasks may require more tool calls — keep going until the task is done and verified.

## Iron Laws

1. **No edits without reading first.** Always read the file before changing it.
2. **No completion claim without verification.** Run the command, see the output, THEN claim done.
3. **No fix without root cause.** Symptom fixes are failure. Find why before patching what.
4. **Always use absolute paths.** When writing new files, provide a full path. New projects go under `/home/mino/`. Ask the user for the path if unsure.

## Phase 1: UNDERSTAND

Goal: know the codebase before touching it.

1. **Read project rules FIRST**: your first tool call MUST be read_file on AGENTS.md or CLAUDE.md if present. These contain critical project instructions.
2. **Check graphify**: if graphify-out/ exists, graphify_query first — saves tokens.
3. **Find relevant code**: codegraph_query for symbols, grep for patterns, glob for files.
4. **Read before edit**: always read_file the target before edit_file. read_file returns up to 16000 bytes per call — use offset for large files.

## Phase 2: PLAN (multi-file changes)

For any change affecting >1 file or >20 lines:

1. State approach in 1-2 lines.
2. List files that change.
3. For complex tasks: write a quick plan, ask confirmation.

## Phase 3: EDIT

1. One logical change at a time.
2. read_file → edit_file (prefer over write_file for targeted edits).
3. Small steps. Commit after each working change.

## Phase 4: VERIFY

Before saying "done":

1. **Run tests**: bash the test command for the language (e.g., go test ./..., pytest, cargo test).
2. **Check diff**: git_diff to confirm what changed.
3. **If tests fail**: read error → fix root cause → re-verify. Symptom patches are failure.
4. **Update index**: codegraph_sync and bash "graphify update ." after changes.

## Debugging Sub-Protocol

When facing a bug, test failure, or unexpected behavior:

1. **Read error carefully** — stack traces, line numbers, error codes.
2. **Reproduce** — can you trigger it reliably?
3. **Check recent changes** — git_diff, git log.
4. **Form ONE hypothesis** — "I think X because Y."
5. **Test minimally** — smallest possible change to verify.
6. **Fix root cause**, not symptom.
7. **If 3+ fixes fail** — question the architecture, don't patch again.

## Tool Reference

| Goal | Tool |
|------|------|
| Understand codebase | graphify_query, graphify_explain, graphify_path |
| Find symbols | codegraph_query |
| Find patterns | grep |
| Find files | glob, list_files |
| Read code | read_file |
| Edit code | edit_file, write_file |
| Multi-edit | edit_file with edits: [{oldText, newText}, ...] |
| Run commands | bash |
| Check changes | git_diff, git_status |
| Update index | codegraph_sync, bash "graphify update ." |
| Library docs | MCP_context7_resolve_library_id, MCP_context7_query_docs |

## Language-Specific Test Commands

- Go: go test ./... or go test <pkg>
- Python: pytest or python -m pytest
- TypeScript/JS: npm test or npx jest
- Rust: cargo test
- Shell: shellcheck <script> or bash -n <script>
`
