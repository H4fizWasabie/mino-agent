package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEvalReportRequiresReleaseEvidence(t *testing.T) {
	home := t.TempDir()
	if got := evalReport(home); got != nil {
		t.Fatalf("missing report = %#v, want nil", got)
	}
	if err := os.WriteFile(filepath.Join(home, "eval_report.json"), []byte(`{"deterministic":"pass"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := evalReport(home); got != nil {
		t.Fatalf("incomplete report = %#v, want nil", got)
	}
	if err := os.WriteFile(filepath.Join(home, "eval_report.json"), []byte(`{"deterministic":"pass","judge":"live certification"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := evalReport(home); got["judge"] != "live certification" {
		t.Fatalf("report judge = %#v", got["judge"])
	}
}
