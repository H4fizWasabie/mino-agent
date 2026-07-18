package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const artifactInlineLimit = 500
const inputPreviewLimit = 8000

// prepareToolOutput keeps an explicit, bounded read_file slice in context.
// Other large results become artifacts so the model can choose the slice it needs.
func prepareToolOutput(home, sessionID string, turn int, tool, output string) string {
	if tool == "read_file" {
		return output
	}
	return compactToolOutput(home, sessionID, turn, tool, output)
}

func compactToolOutput(home, sessionID string, turn int, tool, output string) string {
	if len(output) <= artifactInlineLimit {
		return output
	}
	dir := filepath.Join("/tmp/mino/results", safePath(sessionID), fmt.Sprintf("%d", turn))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return output[:artifactInlineLimit] + "\n[artifact write failed]"
	}
	path := filepath.Join(dir, safePath(tool)+".txt")
	if err := os.WriteFile(path, []byte(output), 0600); err != nil {
		return output[:artifactInlineLimit] + "\n[artifact write failed]"
	}
	return fmt.Sprintf("[artifact: %s → %d chars at %s; use read_file with offset and limit]", tool, len(output), path)
}

func compactUserInput(sessionID, input string, preview int) (string, SessionArtifact) {
	if len(input) <= preview || preview <= 0 {
		return input, SessionArtifact{}
	}
	dir := filepath.Join("/tmp/mino/results", safePath(sessionID), "input-"+fmt.Sprint(time.Now().UnixNano()))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return input[:preview], SessionArtifact{}
	}
	path := filepath.Join(dir, "user.txt")
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		return input[:preview], SessionArtifact{}
	}
	head := preview / 2
	tail := preview - head
	return fmt.Sprintf("[large user input: %d chars at %s; use read_file with offset and limit]\nHEAD:\n%s\n...\nTAIL:\n%s", len(input), path, input[:head], input[len(input)-tail:]), SessionArtifact{Label: "user input", Path: path, Size: len(input)}
}

var artifactOutput = regexp.MustCompile(`^\[artifact: (.+?) → ([0-9]+) chars at (.+?);`)

func artifactFromOutput(output string) (SessionArtifact, bool) {
	matches := artifactOutput.FindStringSubmatch(output)
	if len(matches) != 4 {
		return SessionArtifact{}, false
	}
	size, err := strconv.Atoi(matches[2])
	if err != nil {
		return SessionArtifact{}, false
	}
	return SessionArtifact{Label: matches[1], Path: matches[3], Size: size}, true
}

func safePath(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, s)
	return s
}

func CleanupArtifacts(maxAge time.Duration) {
	root := "/tmp/mino/results"
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > maxAge {
			os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}
