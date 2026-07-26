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
	Confidence      string `json:"confidence,omitempty"` // "manual" (blocks deploys) or "auto" (reports only)
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
	casesPath := filepath.Join(home, "eval", "cases.json")
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

	var results, autoResults []EvalResult
	passed, ran := 0, 0
	autoPassed, autoRan := 0, 0

	for _, c := range cases {
		if c.Skip {
			continue
		}
		isAuto := c.Confidence == "auto"
		timeout := c.MustCompleteInN
		if timeout <= 0 {
			timeout = 120
		}

		result := runEvalCase(home, client, tools, model, c, timeout)
		if isAuto {
			autoResults = append(autoResults, result)
			autoRan++
			if result.Passed {
				autoPassed++
			}
			fmt.Printf("  AUTO  %s: %v\n", c.Name, result.Passed)
		} else {
			results = append(results, result)
			ran++
			if result.Passed {
				passed++
				fmt.Printf("  PASS  %s\n", c.Name)
			} else {
				fmt.Printf("  FAIL  %s — %s\n", c.Name, result.Reason)
			}
		}
	}

	if ran == 0 && autoRan == 0 {
		fmt.Println("eval: no cases to run")
		return 0
	}

	deterministic := "pass"
	if ran > 0 && passed < ran {
		deterministic = fmt.Sprintf("fail:%d/%d", passed, ran)
	}

	report := map[string]any{
		"deterministic": deterministic,
		"judge":         judge,
		"cases":         results,
		"run_at":        time.Now().UTC().Format(time.RFC3339),
	}
	if len(autoResults) > 0 {
		report["auto_results"] = autoResults
		report["auto_summary"] = fmt.Sprintf("%d/%d", autoPassed, autoRan)
	}
	reportPath := filepath.Join(home, "eval_report.json")
	reportData, _ := json.MarshalIndent(report, "", "  ")
	os.WriteFile(reportPath, reportData, 0644)
	fmt.Printf("\neval: %d/%d manual passed", passed, ran)
	if autoRan > 0 {
		fmt.Printf(", %d/%d auto passed", autoPassed, autoRan)
	}
	fmt.Printf(" — report written to %s\n", reportPath)

	if ran > 0 && passed < ran {
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
		result := RunLoop(client, "eval-"+safeApprovalSlug(c.Name), system, messages, tools, 25, 16384, nil, false, home, nil)
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

// GenerateEvalCase builds an eval case from a real interaction (§22).
// confidence: "manual" (user-approved, blocks deploys) or "auto" (harvested, reports only).
func GenerateEvalCase(prompt string, toolsUsed []string, confidence string) EvalCase {
	// Name: first 50 chars of prompt, sanitized
	name := prompt
	if len(name) > 50 {
		name = name[:50]
	}
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == ' ' {
			return r
		}
		return -1
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "untitled"
	}
	if confidence == "auto" {
		name = "auto-" + name
	}

	// ExpectedTool: first mutation tool, or first tool overall
	expectedTool := ""
	for _, t := range toolsUsed {
		if t != "" {
			expectedTool = t
			break
		}
	}

	return EvalCase{
		Name:            name,
		Prompt:          prompt,
		ExpectedTool:    expectedTool,
		MustNotLoop:     true,
		MustCompleteInN: 120,
		Confidence:      confidence,
	}
}

// AppendEvalCase appends a case to eval/cases.json, deduplicating by name.
// Auto cases don't override manual cases with the same name.
func AppendEvalCase(casesPath string, c EvalCase) error {
	os.MkdirAll(filepath.Dir(casesPath), 0755)
	var cases []EvalCase
	if data, err := os.ReadFile(casesPath); err == nil {
		json.Unmarshal(data, &cases)
	}
	for i, existing := range cases {
		if existing.Name == c.Name {
			// Manual overrides auto; auto doesn't override anything
			if c.Confidence == "manual" {
				cases[i] = c
				goto write
			}
			return nil // auto case, already exists
		}
	}
	cases = append(cases, c)
write:
	data, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(casesPath, data, 0644)
}

