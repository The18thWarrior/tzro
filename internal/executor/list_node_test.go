package executor

import (
	"context"
	"testing"
)

// ---------------------------------------------------------------------------
// Slice 1: IsExtractionGoal — embedding-based extraction intent detection
// ---------------------------------------------------------------------------

func TestIsExtractionGoal_ExtractionPrompts(t *testing.T) {
	ctx := context.Background()

	extractionGoals := []string{
		"List every exported function type and variable with their exact signatures from the package",
		"Extract all API endpoints and their request/response schemas from the handler files",
		"Enumerate all error codes defined across the package",
		"Catalog every struct field and interface method with their exact signatures",
		"Index all exported symbols with file locations and documentation strings",
		"List all function signatures and type declarations in the source files",
	}

	for _, goal := range extractionGoals {
		name := goal
		if len(name) > 60 {
			name = name[:60]
		}
		t.Run(name, func(t *testing.T) {
			result := IsExtractionGoal(ctx, goal)
			if !result {
				t.Errorf("IsExtractionGoal(%q) = false, want true", goal)
			}
		})
	}
}

func TestIsExtractionGoal_SynthesisPrompts(t *testing.T) {
	ctx := context.Background()

	synthesisGoals := []string{
		"Explain the architecture of the executor package",
		"Summarize how the Kahn compiler works",
		"Describe the relationship between Probe and Recall nodes",
		"Write a comprehensive README for this project",
		"How does the caching layer improve performance?",
		"Debug the probe execution failure in v5",
	}

	for _, goal := range synthesisGoals {
		name := goal
		if len(name) > 60 {
			name = name[:60]
		}
		t.Run(name, func(t *testing.T) {
			result := IsExtractionGoal(ctx, goal)
			if result {
				t.Errorf("IsExtractionGoal(%q) = true, want false", goal)
			}
		})
	}
}
