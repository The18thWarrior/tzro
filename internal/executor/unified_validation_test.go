package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunUnifiedValidation_AllChecks_Pass(t *testing.T) {
	goal := `Analyze:
1. The compiler module
2. The executor module
3. The inference module`

	synthesis := `## Architecture Analysis

### Compiler Module
The compiler transforms abstract graphs into execution layers.

### Executor Module  
The executor processes DAG layers with tool dispatch.

### Inference Module
The inference module routes between local and cloud models.`

	result := RunUnifiedValidation(context.Background(), goal, synthesis, "source context")

	if result.StructuralPreCheck != "passed" {
		t.Errorf("expected structural pre-check 'passed', got %q", result.StructuralPreCheck)
	}
	if result.CoverageResult == nil {
		t.Fatal("expected non-nil coverage result")
	}
	if result.CoverageResult.Covered != 3 {
		t.Errorf("expected coverage 3/3, got %d/%d", result.CoverageResult.Covered, result.CoverageResult.TotalRequired)
	}
	if !result.OverallPassed {
		t.Error("expected OverallPassed=true when all checks pass")
	}
}

func TestRunUnifiedValidation_StructuralFail(t *testing.T) {
	// Empty synthesis should fail structural pre-check
	result := RunUnifiedValidation(context.Background(), "some goal", "", "source")

	if result.StructuralPreCheck != "failed" {
		t.Errorf("expected structural pre-check 'failed', got %q", result.StructuralPreCheck)
	}
	if result.OverallPassed {
		t.Error("expected OverallPassed=false when structural check fails")
	}
}

func TestRunUnifiedValidation_CoverageAdvisory(t *testing.T) {
	goal := `Check these items:
1. First important thing to verify
2. Second important aspect of the system
3. Third critical component to examine
4. Fourth architectural consideration here`

	// Synthesis only covers 1 of 4 items — coverage is advisory, not blocking
	synthesis := `## Analysis

### First Important Thing
The first thing is that the system uses DAG execution.

That's all we found. The rest of the items were not explored.
Additional padding to make this long enough to pass structural checks.
More content to ensure the synthesis is above the minimum length threshold.`

	result := RunUnifiedValidation(context.Background(), goal, synthesis, "source")

	// Coverage is advisory — OverallPassed should still be true
	if !result.OverallPassed {
		t.Error("expected OverallPassed=true (coverage is advisory, not blocking)")
	}
	if result.CoverageResult == nil {
		t.Fatal("expected non-nil coverage result")
	}
	if len(result.CoverageResult.Missing) < 2 {
		t.Errorf("expected at least 2 missing items logged, got %d", len(result.CoverageResult.Missing))
	}
}

func TestRunUnifiedValidation_WithDeadURL(t *testing.T) {
	// Dead server
	deadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer deadSrv.Close()

	synthesis := `## Report

The documentation can be found at ` + deadSrv.URL + `/docs for reference.
This report contains analysis of the system architecture and components.
Additional content to pass the structural pre-check minimum length.`

	result := RunUnifiedValidation(context.Background(), "write a report", synthesis, "source context")

	if result.StructuralPreCheck != "passed" {
		t.Errorf("expected structural pre-check 'passed', got %q", result.StructuralPreCheck)
	}
	if len(result.ContentIssues) != 1 {
		t.Errorf("expected 1 content issue (dead URL), got %d", len(result.ContentIssues))
	}
	if len(result.ContentIssues) > 0 && result.ContentIssues[0].Type != "dead_url" {
		t.Errorf("expected dead_url issue, got %q", result.ContentIssues[0].Type)
	}
}

func TestRunUnifiedValidation_NoGoalItems(t *testing.T) {
	// Goal without item list → coverage check skipped
	synthesis := `The system architecture follows a modular design pattern with clear boundaries.
Each component communicates through well-defined interfaces. The compiler produces
execution plans that the executor consumes.`

	result := RunUnifiedValidation(context.Background(), "explain the architecture", synthesis, "source")

	if result.CoverageResult != nil {
		t.Errorf("expected nil coverage result for goal without items, got %+v", result.CoverageResult)
	}
	if !result.OverallPassed {
		t.Error("expected OverallPassed=true when goal has no items to check")
	}
}
