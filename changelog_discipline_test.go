package main

import (
	"fmt"
	"path/filepath"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Public-facing discipline (AGENTS.md): changelog why-notes and wayfinder
// tickets describe the failure class and mechanism, never the owner's
// incident — no names, no personal data paths, no business specifics, no
// amounts. Enforced mechanically like the REL-04a seam checks: a banned
// pattern fails `go test ./...`, so CI blocks it before it ships.
// Genericize the wording when this fires — the mechanism is the message.
// The AGENTS.md index is mechanically verified: every relative link resolves
// to an existing file, and every #anchor matches a heading in that file. The
// index cannot drift into rot — a renamed section fails the suite.
func TestAgentsIndexLinksResolve(t *testing.T) {
	data, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	linkRe := regexp.MustCompile(`\[[^]]*\]\(([A-Za-z0-9_./-]+)(#[A-Za-z0-9_-]+)?\)`)
	headRe := regexp.MustCompile(`(?m)^#{1,4} (.+)$`)
	var bad []string
	for _, m := range linkRe.FindAllStringSubmatch(string(data), -1) {
		path := m[1]
		if _, err := os.Stat(path); err != nil {
			bad = append(bad, fmt.Sprintf("dangling file link %q (%v)", path, err))
			continue
		}
		if m[2] == "" {
			continue
		}
		anchor := strings.TrimPrefix(m[2], "#")
		target, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		found := false
		for _, h := range headRe.FindAllStringSubmatch(string(target), -1) {
			if slugifyHeading(h[1]) == anchor {
				found = true
				break
			}
		}
		if !found {
			bad = append(bad, fmt.Sprintf("missing heading %q in %s", anchor, path))
		}
	}
	if len(bad) > 0 {
		t.Fatalf("AGENTS.md index is broken:\n%s", strings.Join(bad, "\n"))
	}
}

// slugifyHeading approximates GitHub's anchor generation for headings:
// lowercase, spaces -> dashes, punctuation stripped.
func slugifyHeading(h string) string {
	h = strings.ToLower(h)
	h = strings.NewReplacer("`", "", "(", "", ")", "", ":", "", ".", "", ",", "", "'", "", "\"", "", "/", "", "?", "", "!", "", "—", "").Replace(h)
	return strings.ReplaceAll(strings.TrimSpace(h), " ", "-")
}

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

// Doc-hygiene (2026-08-14): a wayfinder ticket must close when its work ships.
// Every ticket needs a Status line with a known value, and an OPEN ticket may
// not reference a GitHub issue the CHANGELOG records as closed — the changelog
// is the shipped-work source of truth (no network in CI). This is the
// lifecycle half of the pruning discipline: tickets rot OPEN (found: CTX-021,
// TGM-001, CTX-009) unless the suite fails on the drift.
func TestWayfinderTicketsCloseOnShip(t *testing.T) {
	changelog, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("wayfinder/tickets/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no wayfinder tickets found")
	}
	known := map[string]bool{"OPEN": true, "CONFIRMED": true, "RESOLVED": true, "CLOSED": true, "IMPLEMENTED": true}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		body := string(data)
		status := ""
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "Status:") {
				status = line
				break
			}
		}
		if status == "" {
			t.Errorf("%s: missing Status line (add one: OPEN / CONFIRMED / RESOLVED / CLOSED / IMPLEMENTED)", f)
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(status, "Status:"))
		val = strings.Trim(val, "* ")
		word := strings.Trim(strings.Fields(val)[0], "*")
		
		if !known[word] {
			t.Errorf("%s: unknown Status %q (known: OPEN/CONFIRMED/RESOLVED/CLOSED/IMPLEMENTED)", f, word)
			continue
		}
		if word != "OPEN" {
			continue
		}
		// OPEN tickets may not reference a shipped issue.
		for _, iss := range regexp.MustCompile(`#(\d+)`).FindAllStringSubmatch(status, -1) {
			if regexp.MustCompile(`closes #` + iss[1] + `\b`).Match(changelog) {
				t.Errorf("%s: Status OPEN but issue #%s ships in CHANGELOG — close the ticket", f, iss[1])
			}
		}
	}
}

// Changelog hygiene (2026-08-14): release sections from before the current
// architecture era are one-line index entries — full prose lives in git
// history. This keeps the file a live queue + recent-context surface instead
// of an ever-growing archive (was 117KB, compressed 2026-08-14; the
// discipline test scans every section either way).
func TestChangelogOldEraSectionsAreOneLiners(t *testing.T) {
	data, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	var curName string
	curLines := 0
	finalize := func() {
		if curName != "" && isOldEra(curName) && curLines > 2 {
			t.Errorf("%s: pre-v2.8.0 section must be a one-line index entry (prose lives in git history)", curName)
		}
	}
	headerRe := regexp.MustCompile(`^## \[(v[^\]]+)\]`)
	for _, line := range strings.Split(string(data), "\n") {
		if m := headerRe.FindStringSubmatch(line); m != nil {
			finalize()
			curName = m[1]
			curLines = 1
			continue
		}
		if curName != "" && strings.TrimSpace(line) != "" {
			curLines++
		}
	}
	finalize()
}

func isOldEra(version string) bool {
	m := regexp.MustCompile(`^v(\d+)\.(\d+)`).FindStringSubmatch(version)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major < 2 || (major == 2 && minor < 8)
}
