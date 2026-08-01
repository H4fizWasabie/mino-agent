package main

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestSchemaHasNestedExecutorDetection(t *testing.T) {
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
	if !schemaHasNestedExecutor(wrapper) {
		t.Fatal("nested executor schema not detected")
	}
	flat := mcp.ToolInputSchema{
		Properties: map[string]any{"caption": map[string]any{"type": "string"}},
	}
	if schemaHasNestedExecutor(flat) {
		t.Fatal("flat schema misdetected as nested executor")
	}
}

func TestWrapExecutorArgs(t *testing.T) {
	// The flat companion takes (tool_slug, arguments_json as a string), parses
	// it, and re-wraps into the nested executor shape — the model never
	// constructs nested objects.
	wrapped, err := wrapExecutorArgs("INSTAGRAM_POST_IG_USER_MEDIA",
		`{"caption":"hello","image_url":"http://x/y.jpg","ig_user_id":"123"}`)
	if err != nil {
		t.Fatal(err)
	}
	tools, ok := wrapped["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v", wrapped)
	}
	entry := tools[0].(map[string]any)
	if entry["tool_slug"] != "INSTAGRAM_POST_IG_USER_MEDIA" {
		t.Fatalf("slug = %v", entry["tool_slug"])
	}
	inner, _ := entry["arguments"].(map[string]any)
	if inner["caption"] != "hello" || inner["image_url"] != "http://x/y.jpg" || inner["ig_user_id"] != "123" {
		t.Fatalf("inner args = %v", inner)
	}
	// Invalid JSON string → clear error.
	if _, err := wrapExecutorArgs("X", "{not json"); err == nil ||
		!strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("invalid JSON should error, got %v", err)
	}
}
