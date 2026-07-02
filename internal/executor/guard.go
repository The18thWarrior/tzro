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
