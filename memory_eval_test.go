package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryEvalCasesExerciseRecallAndFlagDuplicate(t *testing.T) {
	graph, err := loadReadOnlyGraph(filepath.Join("eval", "memory-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	cases, err := loadMemoryEvalCases(filepath.Join("eval", "memory_cases.json"))
	if err != nil {
		t.Fatal(err)
	}

	wantPass := map[string]bool{
		"user preference":                  true,
		"project system state":             true,
		"stale fact is visibly marked":     true,
		"archived fact remains findable":   true,
		"duplicate family does not hide":   false,
		"irrelevant query returns no fact": true,
	}
	for _, c := range cases {
		result := evaluateMemoryCase(graph, c)
		if result.Passed != wantPass[c.Name] {
			t.Errorf("%s = %+v output=%q", c.Name, result, graph.Remember(c.Query, c.Turn))
		}
	}
	if _, err := os.Stat(filepath.Join("eval", "memory-fixture", "index.json")); !os.IsNotExist(err) {
		t.Fatalf("memory evaluation wrote an index: %v", err)
	}
}

func TestMemoryEvalReportsMissingIDsAndText(t *testing.T) {
	graph, err := loadReadOnlyGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result := evaluateMemoryCase(graph, MemoryEvalCase{
		Name: "bad", ExpectedIDs: []string{"expected"}, MustContain: []string{"required"},
	})
	if result.Passed || len(result.MissingIDs) != 1 || len(result.MissingStrings) != 1 {
		t.Fatalf("result = %+v", result)
	}
}
