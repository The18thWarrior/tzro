package compiler

import (
	"encoding/json"
	"testing"
)

// TestGraphNodeMCTSBranchesSerializes verifies that MCTSBranches is properly
// serialized/deserialized in JSON, which is how the planner communicates
// node configuration to the executor.
func TestGraphNodeMCTSBranchesSerializes(t *testing.T) {
	node := GraphNode{
		ID:           "action_1",
		Type:         "action",
		MCTSBranches: 3,
	}

	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded GraphNode
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.MCTSBranches != 3 {
		t.Errorf("expected MCTSBranches=3 after round-trip, got %d", decoded.MCTSBranches)
	}
}

// TestGraphNodeMCTSBranchesOmittedWhenZero verifies that MCTSBranches=0
// (single-shot mode) is omitted from JSON output to keep payloads clean.
func TestGraphNodeMCTSBranchesOmittedWhenZero(t *testing.T) {
	node := GraphNode{
		ID:           "deterministic_1",
		Type:         "deterministic",
		MCTSBranches: 0,
	}

	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	if string(data) != "" && json.Valid(data) {
		var raw map[string]interface{}
		json.Unmarshal(data, &raw)
		if _, exists := raw["mctsBranches"]; exists {
			t.Error("MCTSBranches=0 should be omitted from JSON (omitempty)")
		}
	}
}

// TestGraphNodeStreamOutputSerializes verifies that StreamOutput is properly
// serialized/deserialized.
func TestGraphNodeStreamOutputSerializes(t *testing.T) {
	node := GraphNode{
		ID:           "synthesis_1",
		Type:         "synthesis",
		StreamOutput: true,
	}

	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded GraphNode
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if !decoded.StreamOutput {
		t.Error("expected StreamOutput=true after round-trip")
	}
}

// TestMutationBudgetMaxDepthSerializes verifies that MaxDepth is properly
// serialized/deserialized in the MutationBudget.
func TestMutationBudgetMaxDepthSerializes(t *testing.T) {
	budget := MutationBudget{
		MaxSpawns:       10,
		RemainingSpawns: 10,
		MaxDepth:        3,
	}

	data, err := json.Marshal(budget)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded MutationBudget
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.MaxDepth != 3 {
		t.Errorf("expected MaxDepth=3 after round-trip, got %d", decoded.MaxDepth)
	}
}
