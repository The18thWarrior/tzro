package executor

import (
	"context"
	"testing"
)

// TestHeuristicValueFunctionNonEmptyOutput verifies that non-empty, non-error
// output scores positively on the output quality signal.
func TestHeuristicValueFunctionNonEmptyOutput(t *testing.T) {
	vf := &HeuristicValueFunction{}
	score, err := vf.Score(context.Background(), Candidate{SelfScore: 0.5}, "file contents here", "read the config file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score <= 0 {
		t.Errorf("expected positive score for non-empty output, got %f", score)
	}
}

// TestHeuristicValueFunctionEmptyOutput verifies that empty output scores
// lower than non-empty output.
func TestHeuristicValueFunctionEmptyOutput(t *testing.T) {
	vf := &HeuristicValueFunction{}
	emptyScore, err := vf.Score(context.Background(), Candidate{SelfScore: 0.5}, "", "read the config file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	nonEmptyScore, err := vf.Score(context.Background(), Candidate{SelfScore: 0.5}, "file contents here", "read the config file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if emptyScore >= nonEmptyScore {
		t.Errorf("expected empty output score (%f) < non-empty score (%f)", emptyScore, nonEmptyScore)
	}
}

// TestHeuristicValueFunctionErrorOutputPenalized verifies that output
// containing error markers scores lower.
func TestHeuristicValueFunctionErrorOutputPenalized(t *testing.T) {
	vf := &HeuristicValueFunction{}
	errorScore, err := vf.Score(context.Background(), Candidate{SelfScore: 0.5}, `{"error": "file not found"}`, "read the config file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cleanScore, err := vf.Score(context.Background(), Candidate{SelfScore: 0.5}, `{"content": "some data"}`, "read the config file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if errorScore >= cleanScore {
		t.Errorf("expected error output score (%f) < clean output score (%f)", errorScore, cleanScore)
	}
}

// TestHeuristicValueFunctionKeyTermCoverage verifies that output covering
// key terms from the goal prompt scores higher.
func TestHeuristicValueFunctionKeyTermCoverage(t *testing.T) {
	vf := &HeuristicValueFunction{}
	goal := "find the database connection string and port number"

	// Output that covers goal terms
	coveredScore, err := vf.Score(context.Background(), Candidate{SelfScore: 0.5}, "database connection string is postgres://localhost:5432 port number is 5432", goal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Output that covers no goal terms
	uncoveredScore, err := vf.Score(context.Background(), Candidate{SelfScore: 0.5}, "the weather today is sunny", goal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if uncoveredScore >= coveredScore {
		t.Errorf("expected uncovered score (%f) < covered score (%f)", uncoveredScore, coveredScore)
	}
}

// TestHeuristicValueFunctionSelfScoreDampened verifies that the model's
// self-assessed score is included but dampened to prevent overconfident
// candidates from always winning.
func TestHeuristicValueFunctionSelfScoreDampened(t *testing.T) {
	vf := &HeuristicValueFunction{}
	goal := "do something"
	output := "result"

	highSelfScore, err := vf.Score(context.Background(), Candidate{SelfScore: 1.0}, output, goal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lowSelfScore, err := vf.Score(context.Background(), Candidate{SelfScore: 0.1}, output, goal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if highSelfScore <= lowSelfScore {
		t.Errorf("expected high self-score (%f) > low self-score (%f)", highSelfScore, lowSelfScore)
	}

	// Verify dampening: the difference should be less than the raw difference (0.9)
	diff := highSelfScore - lowSelfScore
	if diff >= 0.9 {
		t.Errorf("self-score dampening not applied: diff %f should be < 0.9", diff)
	}
}

// TestHeuristicValueFunctionScoreBounded verifies that scores stay in [0, 1].
func TestHeuristicValueFunctionScoreBounded(t *testing.T) {
	vf := &HeuristicValueFunction{}

	// Best case: all signals positive
	score, err := vf.Score(context.Background(), Candidate{SelfScore: 1.0}, "database connection port number string", "find the database connection string and port number")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score < 0 || score > 1.0 {
		t.Errorf("score %f is outside [0, 1] bounds", score)
	}

	// Worst case: all signals negative
	score, err = vf.Score(context.Background(), Candidate{SelfScore: 0.0}, "", "find the database")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score < 0 || score > 1.0 {
		t.Errorf("score %f is outside [0, 1] bounds", score)
	}
}
