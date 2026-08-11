package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Public-facing discipline (AGENTS.md): changelog why-notes describe the
// failure class and mechanism, never the owner's incident — no names, no
// personal data paths, no business specifics, no amounts. Enforced
// mechanically like the REL-04a seam checks: a banned pattern in
// CHANGELOG.md fails `go test ./...`, so CI blocks it before it ships.
// Genericize the wording when this fires — the mechanism is the message.
func TestChangelogPublicDiscipline(t *testing.T) {
	data, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	patterns := []struct {
		name string
		re   *regexp.Regexp
	}{
		{"owner names", regexp.MustCompile(`(?i)\b(abah|hafiz)\b`)},
		{"business/product specifics", regexp.MustCompile(`(?i)\b(chem ?15|idexx|procura)\b`)},
		{"personal data paths", regexp.MustCompile(`(?i)(/home/procura|/home/hafiz|pos_server_test)`)},
		{"telegram session ids", regexp.MustCompile(`tg:[0-9]+`)},
		{"currency amounts", regexp.MustCompile(`\bRM\s?[0-9]`)},
	}
	var bad []string
	for _, p := range patterns {
		for i, line := range strings.Split(string(data), "\n") {
			if p.re.MatchString(line) {
				bad = append(bad, fmt.Sprintf("line %d (%s): %s", i+1, p.name, strings.TrimSpace(line)))
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("CHANGELOG.md violates public-facing discipline — genericize these:\n%s", strings.Join(bad, "\n"))
	}
}
