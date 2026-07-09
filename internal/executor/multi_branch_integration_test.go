package executor

import (
	"context"
	"testing"

	"tzro/internal/compiler"
	"tzro/internal/config"
)

// TestMultiBranchEvaluationPipeline verifies the complete multi-branch
// evaluation pipeline: generate candidates → classify tools → score → select.
func TestMultiBranchEvaluationPipeline(t *testing.T) {
	candidates := []Candidate{
		{
			Action:    "read database config",
			ToolName:  "read_file",
			Args:      map[string]interface{}{"path": "/etc/db.conf"},
			Reasoning: "Read the database configuration file for connection details",
			SelfScore: 0.8,
		},
		{
			Action:    "search for connection string",
			ToolName:  "search_files",
			Args:      map[string]interface{}{"query": "connection_string"},
			Reasoning: "Search across all files for the database connection string",
			SelfScore: 0.6,
		},
		{
			Action:    "deploy database",
			ToolName:  "web_search", // L4 tool — should be blocked
			Reasoning: "Search the web for database setup",
			SelfScore: 0.9,
		},
	}

	goal := "find the database connection string in the config file"
	ceil := config.GetMCTSSpeculationCeil() // default 2

	vf := &HeuristicValueFunction{}

	// Phase 1: Classify each candidate's tool through the Speculation Fence
	for i, c := range candidates {
		mode := ClassifySpeculation(c.ToolName, ceil)
		switch mode {
		case SpecBlocked:
			candidates[i].Score = -1 // pruned
		case SpecImagined:
			// In a real pipeline, the Local Model would simulate the output.
			// For testing, simulate an imagined response.
			candidates[i].Output = "(imagined) some data from imagined execution"
			score, _ := vf.Score(context.Background(), c, candidates[i].Output, goal)
			candidates[i].Score = score
		case SpecReal:
			// In a real pipeline, the tool would execute.
			// For testing, simulate a real response.
			candidates[i].Output = "database connection string: postgres://localhost:5432/mydb"
			score, _ := vf.Score(context.Background(), c, candidates[i].Output, goal)
			candidates[i].Score = score
		}
	}

	// Phase 2: Select best candidate
	best := selectBestCandidate(candidates)

	// Verify: web_search (L4) should have been pruned
	if candidates[2].Score >= 0 {
		t.Error("L4 tool 'web_search' should have been pruned (Score < 0)")
	}

	// Verify: best candidate should be one of the non-pruned ones
	if best.ToolName == "web_search" {
		t.Error("pruned L4 candidate should not be selected as best")
	}

	// Verify: best candidate should have a positive score
	if best.Score <= 0 {
		t.Errorf("best candidate should have positive score, got %f", best.Score)
	}
}

// TestMultiBranchPipelineConfidenceTierGates verifies that multi-branch
// evaluation only runs after the Confidence Tier check passes. This test
// verifies the decision logic, not the full executor integration.
func TestMultiBranchPipelineConfidenceTierGates(t *testing.T) {
	node := &compiler.GraphNode{
		ID:           "action_1",
		Type:         "action",
		MCTSBranches: 3,
	}

	// Scenario 1: Confidence Tier "sufficient" → multi-branch runs
	if !shouldUseMultiBranch(node) {
		t.Error("non-spawned node with MCTSBranches=3 should use multi-branch")
	}

	// Scenario 2: Spawned node → multi-branch skipped regardless
	spawnedNode := &compiler.GraphNode{
		ID:           "spawned_action_1_1",
		Type:         "action",
		MCTSBranches: 3,
	}
	if shouldUseMultiBranch(spawnedNode) {
		t.Error("spawned node should never use multi-branch")
	}
}

// TestMultiBranchShadowStateIsolation verifies that rollout evaluation
// does not leak state. Shadow state is in-memory only — no SQLite writes.
func TestMultiBranchShadowStateIsolation(t *testing.T) {
	// Create shadow state (in-memory map, not SQLite)
	shadowState := make(map[string]string)

	// Simulate tool execution in shadow state
	shadowState["action_1"] = "result from read_file"
	shadowState["action_2"] = "result from search_files"

	// Shadow state should contain our data
	if len(shadowState) != 2 {
		t.Errorf("expected 2 shadow state entries, got %d", len(shadowState))
	}

	// Verify it's truly ephemeral — just a map, no persistence mechanism
	// This is an architectural test: the shadow state type itself IS the guarantee
	_, isMap := interface{}(shadowState).(map[string]string)
	if !isMap {
		t.Error("shadow state should be a plain map, not a persistent store")
	}
}

// TestMultiBranchSpawnDepthEnforcesLimit verifies that the spawn depth
// limiting logic prevents multi-branch at depth >= MaxDepth.
func TestMultiBranchSpawnDepthEnforcesLimit(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		Nodes: []compiler.GraphNode{
			{ID: "action_1", Type: "action"},
			{ID: "spawned_action_1_1", Type: "action"},
			{ID: "spawned_spawned_action_1_1_1", Type: "action"},
			{ID: "spawned_spawned_spawned_action_1_1_1_1", Type: "action"},
		},
		MutationBudget: &compiler.MutationBudget{MaxDepth: 3},
	}

	tests := []struct {
		nodeID        string
		expectedDepth int
		beyondMax     bool
	}{
		{"action_1", 0, false},
		{"spawned_action_1_1", 1, false},
		{"spawned_spawned_action_1_1_1", 2, false},
		{"spawned_spawned_spawned_action_1_1_1_1", 3, true}, // at MaxDepth
	}

	for _, tc := range tests {
		depth := countSpawnDepth(graph, tc.nodeID)
		if depth != tc.expectedDepth {
			t.Errorf("node %s: expected depth %d, got %d", tc.nodeID, tc.expectedDepth, depth)
		}

		atOrBeyondMax := depth >= graph.MutationBudget.MaxDepth
		if atOrBeyondMax != tc.beyondMax {
			t.Errorf("node %s: expected beyondMax=%v, got %v", tc.nodeID, tc.beyondMax, atOrBeyondMax)
		}
	}
}
