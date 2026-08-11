package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Public-facing discipline (AGENTS.md): changelog why-notes and wayfinder
// tickets describe the failure class and mechanism, never the owner's
// incident — no names, no personal data paths, no business specifics, no
// amounts. Enforced mechanically like the REL-04a seam checks: a banned
// pattern fails `go test ./...`, so CI blocks it before it ships.
// Genericize the wording when this fires — the mechanism is the message.
func TestChangelogPublicDiscipline(t *testing.T) {
	paths := []string{"CHANGELOG.md"}
	entries, err := os.ReadDir("wayfinder")
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				paths = append(paths, "wayfinder/"+e.Name())
			}
		}
		if tickets, err := os.ReadDir("wayfinder/tickets"); err == nil {
			for _, e := range tickets {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
					paths = append(paths, "wayfinder/tickets/"+e.Name())
				}
			}
		}
	}
	var bad []string
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, p := range bannedPatterns {
			for i, line := range strings.Split(string(data), "\n") {
				if p.re.MatchString(line) {
					bad = append(bad, fmt.Sprintf("%s line %d (%s): %s", path, i+1, p.name, strings.TrimSpace(line)))
				}
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("public-facing docs violate the discipline — genericize these:\n%s", strings.Join(bad, "\n"))
	}
}

var bannedPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"owner names", regexp.MustCompile(`(?i)\b(abah|hafiz)\b`)},
	{"business/product specifics", regexp.MustCompile(`(?i)\b(chem ?15|idexx|procura)\b`)},
	{"personal data paths", regexp.MustCompile(`(?i)(/home/procura|/home/hafiz|pos_server_test)`)},
	{"telegram session ids", regexp.MustCompile(`tg:[0-9]+`)},
	{"currency amounts", regexp.MustCompile(`\bRM\s?[0-9]`)},
}
