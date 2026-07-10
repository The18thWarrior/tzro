package executor

import (
	"context"
	"strings"
)

// Candidate represents an action candidate generated during multi-branch
// Edge Thought evaluation. The Local Model produces K of these in a single
// inference call, each representing a different approach to achieving the
// current node's objective.
type Candidate struct {
	Action    string                 `json:"action"`         // Human-readable action description
	ToolName  string                 `json:"toolName"`       // Target tool name
	Args      map[string]interface{} `json:"args,omitempty"` // Tool arguments
	Reasoning string                 `json:"reasoning"`      // Why this approach was chosen
	SelfScore float64                `json:"selfScore"`      // Model's self-assessed score (0.0-1.0)
	Score     float64                `json:"-"`              // Value Function score (set externally)
	Output    string                 `json:"-"`              // Tool output (set during rollout evaluation)
}

// ValueFunction evaluates candidate action quality during multi-branch
// Edge Thought evaluation (ADR-0045). Produces a continuous reward signal
// in [0, 1] for ranked candidate selection.
type ValueFunction interface {
	Score(ctx context.Context, candidate Candidate, output string, goalPrompt string) (float64, error)
}

// HeuristicValueFunction provides zero-inference candidate scoring.
// Default for all multi-branch nodes. Scores based on four signals:
//  1. Output quality (non-empty, non-error)
//  2. Key term coverage from goal prompt
//  3. GoalProgressGuard anti-hallucination check
//  4. Dampened model self-assessment
type HeuristicValueFunction struct{}

// Score evaluates a candidate's output against the goal prompt using
// heuristic signals. Returns a score in [0.0, 1.0].
func (h *HeuristicValueFunction) Score(ctx context.Context, candidate Candidate, output string, goalPrompt string) (float64, error) {
	score := 0.0

	// Signal 1: Output quality (weight: 0.3)
	// Non-empty, non-error output indicates a successful tool execution.
	if output != "" && !containsErrorMarkers(output) {
		score += 0.3
	}

	// Signal 2: Key term coverage from goal prompt (weight: 0.3)
	// Measures how well the output addresses the stated objective.
	goalTerms := extractKeyTerms(goalPrompt)
	if len(goalTerms) > 0 {
		covered := countCoveredTerms(output, goalTerms)
		score += 0.3 * float64(covered) / float64(len(goalTerms))
	}

	// Signal 3: Anti-hallucination check (weight: 0.2)
	// GoalProgressGuard detects premature success claims.
	if output != "" && !detectHallucinatedSufficiency(output) {
		score += 0.2
	}

	// Signal 4: Dampened self-assessment (weight: 0.2)
	// The model's self-score is dampened by 0.7x to prevent overconfident
	// candidates from always winning.
	selfScore := candidate.SelfScore
	if selfScore < 0 {
		selfScore = 0
	}
	if selfScore > 1.0 {
		selfScore = 1.0
	}
	score += 0.2 * selfScore * 0.7

	// Clamp to [0, 1]
	if score < 0 {
		score = 0
	}
	if score > 1.0 {
		score = 1.0
	}

	return score, nil
}

// containsErrorMarkers checks if output contains common error indicators.
func containsErrorMarkers(output string) bool {
	lower := strings.ToLower(output)
	markers := []string{
		`"error"`,
		`"error":`,
		"error:",
		"failed:",
		"failure:",
		"exception:",
		"traceback",
		"panic:",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// extractKeyTerms pulls meaningful words from a goal prompt.
// Filters out common stop words and short words.
func extractKeyTerms(goal string) []string {
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"is": true, "in": true, "to": true, "for": true, "of": true,
		"with": true, "from": true, "by": true, "on": true, "at": true,
		"it": true, "this": true, "that": true, "be": true, "as": true,
		"are": true, "was": true, "were": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "shall": true, "can": true, "need": true,
		"all": true, "each": true, "every": true, "both": true, "few": true,
		"find": true, "get": true, "set": true, "use": true, "make": true,
	}

	words := strings.Fields(strings.ToLower(goal))
	var terms []string
	for _, w := range words {
		// Strip punctuation
		w = strings.Trim(w, ".,;:!?\"'`()[]{}/-")
		if len(w) < 3 {
			continue
		}
		if stopWords[w] {
			continue
		}
		terms = append(terms, w)
	}
	return terms
}

// countCoveredTerms counts how many goal terms appear in the output.
func countCoveredTerms(output string, terms []string) int {
	lower := strings.ToLower(output)
	count := 0
	for _, term := range terms {
		if strings.Contains(lower, term) {
			count++
		}
	}
	return count
}

// detectHallucinatedSufficiency checks for patterns that suggest the model
// is falsely claiming task completion without evidence.
func detectHallucinatedSufficiency(output string) bool {
	lower := strings.ToLower(output)
	// Common hallucination patterns
	hallucinations := []string{
		"task is complete",
		"goal has been achieved",
		"successfully completed all",
		"no further action needed",
	}
	for _, h := range hallucinations {
		if strings.Contains(lower, h) {
			return true
		}
	}
	return false
}
