package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// MemoryEvalCase is a deterministic retrieval assertion. ExpectedIDs must be
// returned by the real Remember path; unwanted IDs must not be returned.
type MemoryEvalCase struct {
	Name        string   `json:"name"`
	Query       string   `json:"query"`
	Turn        string   `json:"turn,omitempty"`
	ExpectedIDs []string `json:"expected_ids"`
	UnwantedIDs []string `json:"unwanted_ids,omitempty"`
	MustContain []string `json:"must_contain,omitempty"`
}

type MemoryEvalResult struct {
	Name           string   `json:"name"`
	Passed         bool     `json:"passed"`
	ReturnedIDs    []string `json:"returned_ids,omitempty"`
	MissingIDs     []string `json:"missing_ids,omitempty"`
	UnwantedIDs    []string `json:"unwanted_ids,omitempty"`
	MissingStrings []string `json:"missing_strings,omitempty"`
}

// RunMemoryEval runs retrieval cases against a Markdown memory directory. It
// loads the graph without NewGraphMemory so no index or memory file is written.
func RunMemoryEval(memoryDir, casesPath string) int {
	cases, err := loadMemoryEvalCases(casesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memory eval: %v\n", err)
		return 1
	}
	graph, err := loadReadOnlyGraph(memoryDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memory eval: %v\n", err)
		return 1
	}

	passed := 0
	for _, c := range cases {
		result := evaluateMemoryCase(graph, c)
		if result.Passed {
			passed++
			fmt.Printf("  PASS  %s\n", result.Name)
		} else {
			fmt.Printf("  FAIL  %s — %s\n", result.Name, memoryEvalFailure(result))
		}
	}
	fmt.Printf("\nmemory eval: %d/%d passed\n", passed, len(cases))
	if passed != len(cases) {
		return 1
	}
	return 0
}

func loadMemoryEvalCases(path string) ([]MemoryEvalCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cases %s: %w", path, err)
	}
	var cases []MemoryEvalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("parse cases %s: %w", path, err)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("cases %s is empty", path)
	}
	return cases, nil
}

func loadReadOnlyGraph(dir string) (*GraphMemory, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}
	gm := &GraphMemory{
		dir: dir, facts: make(map[string]*Fact), files: make(map[string]fileStamp),
		judgedAt: make(map[string]string), communities: make(map[string]int),
		labels: make(map[string]string), parseWarned: make(map[string]bool),
	}
	gm.loadAll()
	return gm, nil
}

func evaluateMemoryCase(graph *GraphMemory, c MemoryEvalCase) MemoryEvalResult {
	output := graph.Remember(c.Query, c.Turn)
	returned := memoryEvalIDs(output)
	seen := make(map[string]bool, len(returned))
	for _, id := range returned {
		seen[id] = true
	}
	result := MemoryEvalResult{Name: c.Name, ReturnedIDs: returned, Passed: true}
	for _, id := range c.ExpectedIDs {
		if !seen[id] {
			result.MissingIDs = append(result.MissingIDs, id)
		}
	}
	for _, id := range c.UnwantedIDs {
		if seen[id] {
			result.UnwantedIDs = append(result.UnwantedIDs, id)
		}
	}
	for _, want := range c.MustContain {
		if !strings.Contains(output, want) {
			result.MissingStrings = append(result.MissingStrings, want)
		}
	}
	result.Passed = len(result.MissingIDs) == 0 && len(result.UnwantedIDs) == 0 && len(result.MissingStrings) == 0
	return result
}

func memoryEvalIDs(output string) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, line := range strings.Split(output, "\n") {
		marker := strings.LastIndex(line, "# ")
		if marker < 0 {
			continue
		}
		id := strings.TrimSpace(line[marker+2:])
		if id == "" || strings.ContainsAny(id, " \t") || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func memoryEvalFailure(result MemoryEvalResult) string {
	var parts []string
	if len(result.MissingIDs) > 0 {
		parts = append(parts, "missing expected="+strings.Join(result.MissingIDs, ","))
	}
	if len(result.UnwantedIDs) > 0 {
		parts = append(parts, "unwanted="+strings.Join(result.UnwantedIDs, ","))
	}
	if len(result.MissingStrings) > 0 {
		parts = append(parts, "missing text="+strings.Join(result.MissingStrings, ","))
	}
	if len(result.ReturnedIDs) > 0 {
		parts = append(parts, "returned="+strings.Join(result.ReturnedIDs, ","))
	}
	return strings.Join(parts, "; ")
}
