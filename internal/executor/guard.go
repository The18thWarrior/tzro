package executor

import (
	"context"
	"strings"
	"tzro/internal/memory"
)

// GoalProgressGuard provides programmatic and heuristic verification of task progress.
type GoalProgressGuard struct{}

// VerifySufficientProgress checks if the source output actually contains the content
// required to fulfill the goal, preventing "sufficiency hallucinations".
func (g *GoalProgressGuard) VerifySufficientProgress(ctx context.Context, goal string, output string, et *memory.EdgeThought) bool {
	// If the model already says it's not achieved and low confidence, we agree.
	if !et.GoalAchieved && et.GoalConfidence < 0.5 {
		return true
	}

	// HEURISTIC 3: Data pipeline detection
	// If the goal requires analysis/computation but the output is raw data
	// (CSV rows, JSON records) without analysis results, the goal isn't met.
	if isAnalysisGoal(goal) && et.GoalAchieved && isRawDataOutput(output) {
		return false
	}

	// HEURISTIC 1: Content vs Metadata
	// If the goal implies synthesis/documentation but output is just a file list.
	isDiscoveryOutput := strings.Contains(output, "success\":true") && (strings.Contains(output, "entries\":[") || strings.Contains(output, "totalCount\":"))
	requiresContent := isSynthesisGoal(goal)

	if requiresContent && isDiscoveryOutput && et.GoalAchieved {
		// This is a likely hallucination: listed files but haven't read them.
		return false
	}

	// HEURISTIC 2: Placeholder detection
	lowerOutput := strings.ToLower(output)
	placeholders := []string{
		"no files found",
		"parent node was skipped",
		"contents have not yet been read",
		"complete implementation details... are not available",
	}
	for _, p := range placeholders {
		if strings.Contains(lowerOutput, p) && et.GoalAchieved {
			return false
		}
	}

	return true
}

func isSynthesisGoal(goal string) bool {
	g := strings.ToLower(goal)
	keywords := []string{"read", "extract", "synthesize", "compile", "summarize", "index", "docs", "documentation"}
	for _, k := range keywords {
		if strings.Contains(g, k) {
			return true
		}
	}
	return false
}

// isAnalysisGoal returns true if the goal implies computation, aggregation,
// or analytical output beyond just reading/retrieving data.
func isAnalysisGoal(goal string) bool {
	g := strings.ToLower(goal)
	keywords := []string{"analyze", "analysis", "breakdown", "distribution", "aggregate", "compute", "calculate", "chart", "plot", "statistics", "count", "group by", "top ", "rank"}
	for _, k := range keywords {
		if strings.Contains(g, k) {
			return true
		}
	}
	return false
}

// isRawDataOutput returns true if the output appears to be raw tabular data
// (CSV rows, JSON arrays of records) without analysis/aggregation results.
func isRawDataOutput(output string) bool {
	lower := strings.ToLower(output)
	// Positive signals: raw data indicators
	hasRawData := strings.Count(output, ",") > 20 || // CSV-like comma density
		strings.Contains(lower, "data profile") ||
		strings.Contains(lower, "column") && strings.Contains(lower, "row")

	// Negative signals: analysis results present
	hasAnalysis := strings.Contains(lower, "total:") ||
		strings.Contains(lower, "average:") ||
		strings.Contains(lower, "distribution:") ||
		strings.Contains(lower, "breakdown:") ||
		strings.Contains(lower, "result:") && strings.Contains(lower, "count")

	return hasRawData && !hasAnalysis
}
