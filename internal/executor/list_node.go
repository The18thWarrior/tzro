package executor

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"tzro/internal/config"
	"tzro/internal/embeddings"
	"tzro/internal/inference"
)

// ---------------------------------------------------------------------------
// List Node — extraction-only node type (ADR-0090)
// ---------------------------------------------------------------------------

// extractionPrototypes are semantic prototype sentences for detecting
// extraction/enumeration goals via embedding cosine similarity.
// These represent the "model points, harness copies" intent — tasks where
// source fidelity matters more than comprehension.
var extractionPrototypes = []string{
	"List every exported function, type, and variable with their exact signatures",
	"Extract all API endpoints and their request/response schemas from the source files",
	"Enumerate all error codes, constants, and configuration keys defined in the package",
	"Catalog every struct field, interface method, and type definition with signatures",
	"Index all exported symbols with their file locations and documentation strings",
	"List all function signatures and type declarations from the source code files",
}

// IsExtractionGoal detects whether a goal requires verbatim extraction
// (List Node) vs exploration+synthesis (Probe Node). Uses the Embedding
// Sidecar with extraction prototype vectors — no keyword heuristics
// (Principle 1, SOLUTION_APPROACH.md).
func IsExtractionGoal(ctx context.Context, goal string) bool {
	threshold := config.GetExtractionIntentThreshold()
	if threshold <= 0 {
		threshold = 0.65
	}

	// Primary: Neural Embedding Sidecar
	if inference.GlobalEmbeddingSidecar != nil && inference.GlobalEmbeddingSidecar.IsAvailable() {
		goalVec, err := inference.GlobalEmbeddingSidecar.Embed(ctx, goal)
		if err == nil && len(goalVec) > 0 {
			for _, proto := range extractionPrototypes {
				protoVec, pErr := inference.GlobalEmbeddingSidecar.Embed(ctx, proto)
				if pErr == nil && len(protoVec) > 0 {
					sim := inference.GlobalEmbeddingSidecar.CosineSimilarity(goalVec, protoVec)
					if float64(sim) >= threshold {
						fmt.Fprintf(os.Stderr, "[ListNode] IsExtractionGoal: matched prototype %q (sim=%.3f >= %.3f)\n", proto, sim, threshold)
						return true
					}
				}
			}
			return false
		}
	}

	// Fallback: Pure-Go bag-of-words cosine similarity
	for _, proto := range extractionPrototypes {
		sim := embeddings.CosineSimilarity(goal, proto)
		if sim >= threshold {
			fmt.Fprintf(os.Stderr, "[ListNode] IsExtractionGoal (fallback): matched prototype %q (sim=%.3f >= %.3f)\n", proto, sim, threshold)
			return true
		}
	}

	return false
}

// identifierPattern matches PascalCase and camelCase identifiers that are
// likely to be specific function/type/variable names mentioned in the goal.
var identifierPattern = regexp.MustCompile(`\b([A-Z][a-zA-Z0-9]+(?:[A-Z][a-zA-Z0-9]*)*)\b`)

// CheckListCoverage verifies that extracted snippets contain expected
// identifiers from the goal. Returns a list of missing identifiers.
// Only checks identifiers that look like code symbols (PascalCase).
//
// When the goal mentions no identifiable symbols, returns nil (no check).
func CheckListCoverage(goal string, extractedOutput string) []string {
	// Extract PascalCase identifiers from the goal
	matches := identifierPattern.FindAllString(goal, -1)
	if len(matches) == 0 {
		return nil
	}

	// Filter to identifiers that look like code symbols (not common English words)
	commonWords := map[string]bool{
		"List": true, "Extract": true, "All": true, "The": true,
		"Find": true, "Get": true, "Set": true, "From": true,
		"Including": true, "Every": true, "With": true, "And": true,
	}

	var identifiers []string
	seen := make(map[string]bool)
	for _, m := range matches {
		if !commonWords[m] && !seen[m] {
			seen[m] = true
			identifiers = append(identifiers, m)
		}
	}

	if len(identifiers) == 0 {
		return nil
	}

	// Check presence in extracted output
	var missing []string
	for _, id := range identifiers {
		if !strings.Contains(extractedOutput, id) {
			missing = append(missing, id)
		}
	}

	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "[ListNode] Coverage check: %d/%d identifiers missing: %v\n",
			len(missing), len(identifiers), missing)
	}

	return missing
}
