package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EvalCase matches eval/cases.json entries.
type EvalCase struct {
	Name            string `json:"name"`
	Prompt          string `json:"prompt"`
	ExpectedTool    string `json:"expected_tool"`
	MustNotLoop     bool   `json:"must_not_loop"`
	MustCompleteInN int    `json:"must_complete_in_n"`
	Skip            bool   `json:"skip,omitempty"`
}

// EvalResult records the outcome of a single case.
type EvalResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Reason string `json:"reason,omitempty"`
}

// RunEval executes all cases in eval/cases.json against a real LLM.
// Writes ~/.mino/eval_report.json and returns exit code (0 = all pass, 1 = any fail).
func RunEval(home string) int {
	casesPath := "eval/cases.json"
	if _, err := os.Stat(casesPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "eval: %s not found\n", casesPath)
		return 1
	}
	data, err := os.ReadFile(casesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: read %s: %v\n", casesPath, err)
		return 1
	}
	var cases []EvalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		fmt.Fprintf(os.Stderr, "eval: parse %s: %v\n", casesPath, err)
		return 1
	}

	s := LoadSettings()
	db := Connect(home)
	mem := NewMemory(db, nil, s)
	tools := BuildRegistry(db, home, s.Workspace, mem, s.Location())
	client, err := NewProviderManager(home, s, LoadAuthStore(home))
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: provider init failed: %v\n", err)
		return 1
	}

	model := s.Model
	judge := model + " @ " + time.Now().UTC().Format("2006-01-02")

	var results []EvalResult
	passed := 0
	ran := 0

	for _, c := range cases {
		if c.Skip {
			continue
		}
		ran++
		timeout := c.MustCompleteInN
		if timeout <= 0 {
			timeout = 120
		}

		result := runEvalCase(home, client, tools, model, c, timeout)
		results = append(results, result)
		if result.Passed {
			passed++
			fmt.Printf("  PASS  %s\n", c.Name)
		} else {
			fmt.Printf("  FAIL  %s — %s\n", c.Name, result.Reason)
		}
	}

	if ran == 0 {
		fmt.Println("eval: no cases to run")
		return 0
	}

	deterministic := "pass"
	if passed < ran {
		deterministic = fmt.Sprintf("fail:%d/%d", passed, ran)
	}

	report := map[string]any{
		"deterministic": deterministic,
		"judge":         judge,
		"cases":         results,
		"run_at":        time.Now().UTC().Format(time.RFC3339),
	}
	reportPath := filepath.Join(home, "eval_report.json")
	reportData, _ := json.MarshalIndent(report, "", "  ")
	os.WriteFile(reportPath, reportData, 0644)
	fmt.Printf("\neval: %d/%d passed — report written to %s\n", passed, ran, reportPath)

	if passed < ran {
		return 1
	}
	return 0
}

func runEvalCase(home string, client *ProviderManager, tools *Registry, model string, c EvalCase, timeoutSec int) EvalResult {
	system := loadSoul(home)
	messages := []Message{{Role: "user", Content: c.Prompt}}

	// run the agent loop with a timeout
	type loopOutcome struct {
		result *LoopResult
	}
	done := make(chan loopOutcome, 1)
	go func() {
		result := RunLoop(client, "eval-"+safeApprovalSlug(c.Name), system, messages, tools, 25, 16384, nil, false, nil, home, nil)
		done <- loopOutcome{result}
	}()

	select {
	case outcome := <-done:
		r := outcome.result
		// check must_not_loop
		if c.MustNotLoop && r.Status != "complete" && r.Status != "blocked" {
			return EvalResult{Name: c.Name, Passed: false, Reason: "agent did not complete (status=" + r.Status + ")"}
		}
		// check expected_tool
		if c.ExpectedTool != "" {
			found := false
			for _, tc := range r.ToolCalls {
				if tc.Name == c.ExpectedTool {
					found = true
					break
				}
			}
			if !found {
				toolNames := make([]string, len(r.ToolCalls))
				for i, tc := range r.ToolCalls {
					toolNames[i] = tc.Name
				}
				return EvalResult{Name: c.Name, Passed: false, Reason: "expected tool '" + c.ExpectedTool + "' not called; tools used: " + strings.Join(toolNames, ", ")}
			}
		}
		return EvalResult{Name: c.Name, Passed: true}
	case <-time.After(time.Duration(timeoutSec) * time.Second):
		return EvalResult{Name: c.Name, Passed: false, Reason: fmt.Sprintf("timed out after %ds", timeoutSec)}
	}
}

