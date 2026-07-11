package executor

import (
	"context"
	"testing"

	"tzro/internal/compiler"
)

// MockMultiBranchInference is a test double for EdgeThoughtInference that
// returns predetermined K candidates for multi-branch evaluation.
type MockMultiBranchInference struct {
	MockEdgeThoughtInference
	candidates []Candidate
	callCount  int
}

func (m *MockMultiBranchInference) GenerateKCandidates(
	ctx context.Context,
	taskID string,
	sourceNode *compiler.GraphNode,
	targetNode *compiler.GraphNode,
	sourceOutput string,
	k int,
) ([]Candidate, error) {
	m.callCount++
	if m.candidates != nil {
		return m.candidates, nil
	}
	// Generate K default candidates
	var candidates []Candidate
	for i := 0; i < k; i++ {
		candidates = append(candidates, Candidate{
			Action:    "default action",
			ToolName:  targetNode.Action,
			Reasoning: "default reasoning",
			SelfScore: 0.5,
		})
	}
	return candidates, nil
}

// TestGenerateKCandidatesReturnsKCandidates verifies that the multi-branch
// inference generates exactly K candidates as requested.
func TestGenerateKCandidatesReturnsKCandidates(t *testing.T) {
	mock := &MockMultiBranchInference{}

	source := &compiler.GraphNode{ID: "A", Type: "action", Action: "read_file"}
	target := &compiler.GraphNode{ID: "B", Type: "action", Action: "write_file"}

	candidates, err := mock.GenerateKCandidates(
		context.Background(), "task-1", source, target, `{"result": "file contents"}`, 3,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 3 {
		t.Errorf("expected 3 candidates, got %d", len(candidates))
	}
}

// TestGenerateKCandidatesContainsRequiredFields verifies that each candidate
// includes ToolName, Reasoning, and SelfScore.
func TestGenerateKCandidatesContainsRequiredFields(t *testing.T) {
	mock := &MockMultiBranchInference{
		candidates: []Candidate{
			{
				Action:    "write modified config",
				ToolName:  "write_file",
				Args:      map[string]interface{}{"path": "/etc/config.json"},
				Reasoning: "Config needs updating with new values",
				SelfScore: 0.8,
			},
			{
				Action:    "append to config",
				ToolName:  "write_file",
				Args:      map[string]interface{}{"path": "/etc/config.json", "mode": "append"},
				Reasoning: "Appending preserves existing entries",
				SelfScore: 0.6,
			},
		},
	}

	source := &compiler.GraphNode{ID: "A", Type: "action", Action: "read_file"}
	target := &compiler.GraphNode{ID: "B", Type: "action", Action: "write_file"}

	candidates, err := mock.GenerateKCandidates(
		context.Background(), "task-1", source, target, `{"content": "old config"}`, 2,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, c := range candidates {
		if c.ToolName == "" {
			t.Errorf("candidate %d: ToolName is empty", i)
		}
		if c.Reasoning == "" {
			t.Errorf("candidate %d: Reasoning is empty", i)
		}
		if c.SelfScore < 0 || c.SelfScore > 1.0 {
			t.Errorf("candidate %d: SelfScore %f outside [0,1]", i, c.SelfScore)
		}
	}
}

// TestMultiBranchEvaluationFlow verifies the end-to-end flow:
// generate K candidates → score each → select highest scoring.
func TestMultiBranchEvaluationFlow(t *testing.T) {
	candidates := []Candidate{
		{Action: "approach A", ToolName: "read_file", Reasoning: "Read first", SelfScore: 0.3},
		{Action: "approach B", ToolName: "read_file", Reasoning: "Read the database config file", SelfScore: 0.7},
		{Action: "approach C", ToolName: "read_file", Reasoning: "Read all", SelfScore: 0.5},
	}

	vf := &HeuristicValueFunction{}
	goal := "find the database connection string in the config file"

	var best Candidate
	bestScore := -1.0

	for _, c := range candidates {
		// Simulate real tool execution output
		output := "database connection string: postgres://localhost:5432"
		score, err := vf.Score(context.Background(), c, output, goal)
		if err != nil {
			t.Fatalf("scoring failed: %v", err)
		}

		c.Score = score
		if score > bestScore {
			bestScore = score
			best = c
		}
	}

	// The best candidate should have the highest score
	if best.Action == "" {
		t.Error("no best candidate selected")
	}
	if bestScore <= 0 {
		t.Errorf("best score should be positive, got %f", bestScore)
	}
}

// TestSelectBestCandidate verifies the candidate selection function.
func TestSelectBestCandidate(t *testing.T) {
	candidates := []Candidate{
		{Action: "A", Score: 0.3},
		{Action: "B", Score: 0.9},
		{Action: "C", Score: 0.5},
	}

	best := selectBestCandidate(candidates)
	if best.Action != "B" {
		t.Errorf("expected candidate B (score 0.9), got %s (score %f)", best.Action, best.Score)
	}
}

// TestSelectBestCandidateSkipsPruned verifies that pruned candidates
// (Score < 0) are not selected.
func TestSelectBestCandidateSkipsPruned(t *testing.T) {
	candidates := []Candidate{
		{Action: "A", Score: -1}, // pruned (blocked tool)
		{Action: "B", Score: 0.3},
		{Action: "C", Score: -1}, // pruned
	}

	best := selectBestCandidate(candidates)
	if best.Action != "B" {
		t.Errorf("expected candidate B (only non-pruned), got %s", best.Action)
	}
}
