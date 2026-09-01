package main

import (
	"strings"
	"testing"
)

func TestRenderServiceDefinitionAcrossPlatforms(t *testing.T) {
	d, err := parseServiceDefinition(map[string]any{
		"name":              "mino",
		"executable":        "/opt/mino/mino",
		"args":              []any{"--serve", "hello world"},
		"environment":       map[string]any{"MINO_ENV": "test"},
		"working_directory": "/opt/mino",
		"restart":           "always",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ goos, suffix, marker string }{
		{"linux", ".service", "ExecStart=/opt/mino/mino --serve \"hello world\""},
		{"darwin", ".plist", "<key>ProgramArguments</key>"},
		{"windows", ".service.txt", "/opt/mino/mino --serve \"hello world\""},
	} {
		name, content := renderServiceDefinition(tt.goos, d)
		if !strings.HasSuffix(name, tt.suffix) || !strings.Contains(content, tt.marker) {
			t.Fatalf("%s rendered %q: %q", tt.goos, name, content)
		}
	}
}

func TestParseServiceDefinitionRejectsUnsafeInput(t *testing.T) {
	for _, args := range []map[string]any{
		{"name": "../mino", "executable": "/bin/true"},
		{"name": "mino", "executable": ""},
		{"name": "mino", "executable": "/bin/true", "restart": "sometimes"},
		{"name": "mino", "executable": "/bin/true", "environment": map[string]any{"BAD-KEY": "x"}},
	} {
		if _, err := parseServiceDefinition(args); err == nil {
			t.Fatalf("expected rejection for %#v", args)
		}
	}
}
