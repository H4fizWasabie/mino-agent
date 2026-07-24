package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// coding_tools.go — language-agnostic coding tools.
// All tools shell out to CLI binaries following the same pattern as runBash.

func runCoding(binary string, args ...string) (string, error) {
	return runCodingContext(context.Background(), 2*time.Minute, binary, args...)
}

func runCodingContext(parent context.Context, timeout time.Duration, binary string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = "" // relative paths resolve from working directory
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ponytail: if primary binary not found, try fallback
func runCodingFallback(primary string, primaryArgs []string, fallback string, fallbackArgs []string) string {
	return runCodingFallbackContext(context.Background(), 2*time.Minute, primary, primaryArgs, fallback, fallbackArgs)
}

func runCodingFallbackContext(ctx context.Context, timeout time.Duration, primary string, primaryArgs []string, fallback string, fallbackArgs []string) string {
	out, err := runCodingContext(ctx, timeout, primary, primaryArgs...)
	if err == nil {
		return trimOutput(string(out))
	}
	if !isNotFound(err) {
		return fmt.Sprintf("Error: %v\n%s", err, trimOutput(string(out)))
	}
	out, err = runCodingContext(ctx, timeout, fallback, fallbackArgs...)
	if err != nil {
		return fmt.Sprintf("Error: %v\n%s", err, trimOutput(string(out)))
	}
	return trimOutput(out)
}

func codingToolTimeout(values []time.Duration) time.Duration {
	if len(values) > 0 && values[0] > 0 {
		return values[0]
	}
	return 2 * time.Minute
}

func contextualTool(tool *Tool, run ContextToolFunc) *Tool {
	tool.ContextFn = run
	tool.Fn = func(args map[string]any) string { return run(context.Background(), args) }
	return tool
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

func makeListFilesTool(timeout ...time.Duration) *Tool {
	deadline := codingToolTimeout(timeout)
	run := func(ctx context.Context, args map[string]any) string {
		dir, _ := args["path"].(string)
		if dir == "" {
			dir = "."
		}
		depth := 2
		if d, ok := args["depth"].(float64); ok && d > 0 {
			depth = int(d)
		}
		out, err := runCodingContext(ctx, deadline, "find", dir, "-maxdepth", fmt.Sprint(depth), "-not", "-path", "*/\\.*", "-not", "-path", "*/node_modules/*")
		if err != nil {
			out, _ = runCodingContext(ctx, deadline, "ls", "-la", dir)
		}
		return trimOutput(out)
	}
	return contextualTool(&Tool{
		Name:        "list_files",
		Description: "List files in a directory. Use to explore project structure before reading.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string", "description": "Directory path (default: current directory)"},
				"depth": map[string]any{"type": "integer", "description": "How deep to descend (default: 2)"},
			},
		},
	}, run)
}

func makeGrepTool(timeout ...time.Duration) *Tool {
	deadline := codingToolTimeout(timeout)
	run := func(ctx context.Context, args map[string]any) string {
		pattern, _ := args["pattern"].(string)
		dir, _ := args["path"].(string)
		if dir == "" {
			dir = "."
		}
		return runCodingFallbackContext(ctx, deadline,
			"rg", []string{"--no-heading", "-n", pattern, dir},
			"grep", []string{"-rn", pattern, dir},
		)
	}
	return contextualTool(&Tool{
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
	}, run)
}

func makeGlobTool(timeout ...time.Duration) *Tool {
	deadline := codingToolTimeout(timeout)
	run := func(ctx context.Context, args map[string]any) string {
		pattern, _ := args["pattern"].(string)
		dir, _ := args["path"].(string)
		if dir == "" {
			dir = "."
		}
		out, err := runCodingContext(ctx, deadline, "find", dir, "-name", pattern, "-not", "-path", "*/\\.*", "-not", "-path", "*/node_modules/*")
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		return trimOutput(out)
	}
	return contextualTool(&Tool{
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
	}, run)
}

func makeGitDiffTool(timeout ...time.Duration) *Tool {
	deadline := codingToolTimeout(timeout)
	run := func(ctx context.Context, args map[string]any) string {
		staged, _ := args["staged"].(bool)
		gitArgs := []string{"diff"}
		if staged {
			gitArgs = append(gitArgs, "--cached")
		}
		out, err := runCodingContext(ctx, deadline, "git", gitArgs...)
		if err != nil {
			return fmt.Sprintf("Error: %v\n%s", err, trimOutput(out))
		}
		return trimOutput(out)
	}
	return contextualTool(&Tool{
		Name:        "git_diff",
		Description: "Show git diff (unstaged changes). Use 'staged' param for staged changes. Use after editing to verify what changed.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"staged": map[string]any{"type": "boolean", "description": "Show staged changes instead of unstaged (default: false)"},
			},
		},
	}, run)
}

func makeGitStatusTool(timeout ...time.Duration) *Tool {
	deadline := codingToolTimeout(timeout)
	run := func(ctx context.Context, _ map[string]any) string {
		out, err := runCodingContext(ctx, deadline, "git", "status", "--short")
		if err != nil {
			return fmt.Sprintf("Error: %v\n%s", err, trimOutput(out))
		}
		return trimOutput(out)
	}
	return contextualTool(&Tool{
		Name:        "git_status",
		Description: "Show git working tree status (short format). Use to see what files changed, are staged, or untracked.",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, run)
}

func makeGraphifyQueryTool(timeout ...time.Duration) *Tool {
	deadline := codingToolTimeout(timeout)
	run := func(ctx context.Context, args map[string]any) string {
		q, _ := args["question"].(string)
		out, err := runCodingContext(ctx, deadline, "graphify", "query", q)
		if err != nil {
			return fmt.Sprintf("graphify query failed (is graphify installed and graphify-out/ present?): %v", err)
		}
		return trimOutput(out)
	}
	return contextualTool(&Tool{
		Name:        "graphify_query",
		Description: "Query the knowledge graph for codebase questions. Use BEFORE grep or read_file — answers are token-efficient. Requires graphify-out/ to exist.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{"type": "string", "description": "Natural language question about the codebase"},
			},
			"required": []string{"question"},
		},
	}, run)
}

func makeGraphifyExplainTool(timeout ...time.Duration) *Tool {
	deadline := codingToolTimeout(timeout)
	run := func(ctx context.Context, args map[string]any) string {
		concept, _ := args["concept"].(string)
		out, err := runCodingContext(ctx, deadline, "graphify", "explain", concept)
		if err != nil {
			return fmt.Sprintf("graphify explain failed: %v", err)
		}
		return trimOutput(out)
	}
	return contextualTool(&Tool{
		Name:        "graphify_explain",
		Description: "Get a plain-language explanation of a concept/function/module from the knowledge graph.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"concept": map[string]any{"type": "string", "description": "The concept, function, or module to explain"},
			},
			"required": []string{"concept"},
		},
	}, run)
}

func makeGraphifyPathTool(timeout ...time.Duration) *Tool {
	deadline := codingToolTimeout(timeout)
	run := func(ctx context.Context, args map[string]any) string {
		src, _ := args["source"].(string)
		tgt, _ := args["target"].(string)
		out, err := runCodingContext(ctx, deadline, "graphify", "path", src, tgt)
		if err != nil {
			return fmt.Sprintf("graphify path failed: %v", err)
		}
		return trimOutput(out)
	}
	return contextualTool(&Tool{
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
	}, run)
}

func makeCodegraphQueryTool(timeout ...time.Duration) *Tool {
	deadline := codingToolTimeout(timeout)
	run := func(ctx context.Context, args map[string]any) string {
		search, _ := args["search"].(string)
		out, err := runCodingContext(ctx, deadline, "codegraph", "query", search)
		if err != nil {
			return fmt.Sprintf("codegraph query failed (is codegraph installed?): %v", err)
		}
		return trimOutput(out)
	}
	return contextualTool(&Tool{
		Name:        "codegraph_query",
		Description: "Search for symbols (functions, types, methods) in the codebase. Use for finding definitions and references.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"search": map[string]any{"type": "string", "description": "Symbol or term to search for"},
			},
			"required": []string{"search"},
		},
	}, run)
}

func makeCodegraphSyncTool(timeout ...time.Duration) *Tool {
	deadline := codingToolTimeout(timeout)
	run := func(ctx context.Context, _ map[string]any) string {
		out, err := runCodingContext(ctx, deadline, "codegraph", "sync")
		if err != nil {
			return fmt.Sprintf("codegraph sync failed: %v", err)
		}
		return trimOutput(out)
	}
	return contextualTool(&Tool{
		Name:        "codegraph_sync",
		Description: "Re-index the codebase for semantic search. Call after making code changes to keep the index fresh.",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, run)
}

// seedBuiltinSkills writes default skill and MCP config files if they don't exist.
func seedBuiltinSkills(home string) {
	os.MkdirAll(filepath.Join(home, "skills", "coding"), 0700)
	os.MkdirAll(filepath.Join(home, "mcp.d"), 0700)

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
}

//go:embed assets/coding.SKILL.md
var codingSkill string
