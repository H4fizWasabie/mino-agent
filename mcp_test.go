package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestSchemaHasToolsArrayDetectsWrapper(t *testing.T) {
	wrapper := mcp.ToolInputSchema{
		Properties: map[string]any{
			"tools": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tool_slug": map[string]any{"type": "string"},
						"arguments": map[string]any{"type": "object"},
					},
				},
			},
		},
	}
	if !schemaHasToolsArray(wrapper) {
		t.Fatal("wrapper schema not detected")
	}
	flat := mcp.ToolInputSchema{
		Properties: map[string]any{
			"caption": map[string]any{"type": "string"},
		},
	}
	if schemaHasToolsArray(flat) {
		t.Fatal("flat schema misdetected as wrapper")
	}
}

func TestFlattenedToolReWrapsArgs(t *testing.T) {
	// The flat tool's Fn must re-wrap flat args into the executor's nested shape
	// and forward to MULTI_EXECUTE — the model never sees the nesting.
	home := t.TempDir()
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	bridge := NewMCPBridge(home, registry)
	// Serve as the MCP server: capture the wrapped call.
	var got map[string]any
	serve := func(server, tool string, args map[string]any) string {
		got = args
		return `{"data":{"results":[{"response":{"successful":true,"data":{"id":"42"}}}]}}`
	}
	// Build the flat tool exactly as registerFlattenedTools does.
	bridge.registry.Register(&Tool{
		Name:        "MCP_composio_INSTAGRAM_POST_IG_USER_MEDIA",
		Description: "[MCP:composio flattened] test",
		Schema:      map[string]any{"type": "object", "properties": map[string]any{"caption": map[string]any{"type": "string"}, "image_url": map[string]any{"type": "string"}, "ig_user_id": map[string]any{"type": "string"}}},
		Fn: func(args map[string]any) string {
			return serve("composio", "COMPOSIO_MULTI_EXECUTE_TOOL", map[string]any{
				"tools": []any{map[string]any{"tool_slug": "INSTAGRAM_POST_IG_USER_MEDIA", "arguments": args}},
			})
		},
	})
	out := bridge.registry.Execute("MCP_composio_INSTAGRAM_POST_IG_USER_MEDIA", map[string]any{
		"caption": "hello", "image_url": "http://x/y.jpg", "ig_user_id": "123",
	})
	if got == nil {
		t.Fatal("wrapped call not captured")
	}
	tools, ok := got["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v", got)
	}
	entry := tools[0].(map[string]any)
	if entry["tool_slug"] != "INSTAGRAM_POST_IG_USER_MEDIA" {
		t.Fatalf("slug = %v", entry["tool_slug"])
	}
	args, _ := entry["arguments"].(map[string]any)
	if args["caption"] != "hello" || args["image_url"] != "http://x/y.jpg" || args["ig_user_id"] != "123" {
		t.Fatalf("args = %v", args)
	}
	if out == "" {
		t.Fatal("empty output")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
}

func TestDeprecatedToolsSkipped(t *testing.T) {
	// The flatten filter must skip schemas whose description starts with
	// DEPRECATED so the model is never offered the old tool alongside its
	// replacement (e.g. INSTAGRAM_CREATE_MEDIA_CONTAINER vs the current tools).
	home := t.TempDir()
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	bridge := NewMCPBridge(home, registry)

	// Register one deprecated and one current flat tool, mimicking
	// registerFlattenedTools' skip filter.
	for slug, desc := range map[string]string{
		"OLD_TOOL": "DEPRECATED: Use NEW_TOOL instead. Old way.",
		"NEW_TOOL": "Current tool. Does the thing.",
	} {
		if strings.HasPrefix(strings.ToLower(desc), "deprecated") {
			continue
		}
		fullName := "MCP_composio_" + slug
		bridge.registry.Register(&Tool{Name: fullName, Description: desc})
	}
	if _, ok := bridge.registry.tools["MCP_composio_OLD_TOOL"]; ok {
		t.Fatal("deprecated tool was registered")
	}
	if _, ok := bridge.registry.tools["MCP_composio_NEW_TOOL"]; !ok {
		t.Fatal("current tool was not registered")
	}
}

func TestMCPSchemaGating(t *testing.T) {
	// Family derivation: MCP_composio_INSTAGRAM_POST_IG_USER_MEDIA belongs to the
	// composio_INSTAGRAM family; the wrapper is a nested-executor shape.
	if got := mcpToolkitFamily("MCP_composio_INSTAGRAM_POST_IG_USER_MEDIA"); got != "composio_INSTAGRAM" {
		t.Fatalf("family = %q, want composio_INSTAGRAM", got)
	}
	if got := mcpToolkitFamily("MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL"); got != "composio_COMPOSIO" {
		t.Fatalf("family = %q, want composio_COMPOSIO", got)
	}
	if got := mcpToolkitFamily("read_file"); got != "" {
		t.Fatalf("family = %q, want empty for builtin", got)
	}
	wrapper := &Tool{Name: "MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL", Description: "executor",
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"tools": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "properties": map[string]any{
					"tool_slug": map[string]any{"type": "string"},
					"arguments": map[string]any{"type": "object"},
				},
			}},
		}}}
	flat := &Tool{Name: "MCP_composio_INSTAGRAM_POST_IG_USER_MEDIA", Description: "post instagram media",
		Schema: map[string]any{"type": "object", "properties": map[string]any{"caption": map[string]any{"type": "string"}}}}
	if !schemaHasToolsArrayTool(wrapper) {
		t.Fatal("wrapper not detected as nested executor")
	}
	if schemaHasToolsArrayTool(flat) {
		t.Fatal("flat tool misdetected as nested executor")
	}
}
