package executor

import (
	"context"
	"fmt"
	"os"
	"time"
)

// UnifiedValidationResult aggregates all validation check results.
// Populated by RunUnifiedValidation and consumed by VerifyTaskOutput.
type UnifiedValidationResult struct {
	StructuralPreCheck string           `json:"structuralPreCheck"` // "passed" | "failed"
	StructuralReason   string           `json:"structuralReason,omitempty"`
	CoverageResult     *CoverageResult  `json:"coverageResult,omitempty"`  // nil if no item list in goal
	ContentIssues      []ContentIssue   `json:"contentIssues,omitempty"`   // URL/quote issues
	OverallPassed      bool             `json:"overallPassed"`
}

// RunUnifiedValidation executes all validation checks in sequence.
// Execution order (by latency):
//  1. StructuralPreCheck (existing + FM1 meta-response) — <1ms
//  2. CheckCoverage (FM5) — <10ms
//  3. ValidateContent (FM3 URLs + quotes) — ~1-2s (HTTP HEAD)
//
// If StructuralPreCheck fails, downstream checks still run to provide
// comprehensive diagnostics. The OverallPassed field reflects whether
// all checks passed.
func RunUnifiedValidation(ctx context.Context, goal, synthesis, sourceContext string) *UnifiedValidationResult {
	result := &UnifiedValidationResult{
		OverallPassed: true,
	}

	// Stage 1: Structural pre-check (<1ms)
	preCheckResult, preCheckReason := StructuralPreCheck(synthesis)
	result.StructuralPreCheck = preCheckResult
	result.StructuralReason = preCheckReason
	if preCheckResult == "failed" {
		result.OverallPassed = false
		fmt.Fprintf(os.Stderr, "[UnifiedValidation] Structural pre-check FAILED: %s\n", preCheckReason)
	}

	// Stage 2: Coverage verification (<10ms)
	// Only runs when the goal has extractable item lists.
	// Advisory only — missing items are logged and fed to the Verification Gate
	// prompt as evidence, but do not independently block. The cloud's
	// completeness score in the Verification Rubric owns that judgment.
	coverageResult := CheckCoverage(goal, synthesis)
	result.CoverageResult = coverageResult
	if coverageResult != nil && len(coverageResult.Missing) > 0 {
		fmt.Fprintf(os.Stderr, "[PreFlightValidation] Coverage advisory: %d/%d items missing\n",
			len(coverageResult.Missing), coverageResult.TotalRequired)
	}

	// Stage 3: Content validation (bounded to 2s)
	// Only runs when structural pre-check passes — no point validating
	// URLs in degenerate output. Uses a hard 2s deadline to bound
	// worst-case latency on the VTE hot path. URLs that don't respond
	// within the deadline are flagged as timeout (context cancellation).
	if preCheckResult == "passed" {
		contentCtx, contentCancel := context.WithTimeout(ctx, 2*time.Second)
		issues := ValidateContent(contentCtx, synthesis, sourceContext)
		contentCancel()
		result.ContentIssues = issues
		if len(issues) > 0 {
			fmt.Fprintf(os.Stderr, "[PreFlightValidation] Content validation found %d issues\n", len(issues))
		}
	}

	return result
}
