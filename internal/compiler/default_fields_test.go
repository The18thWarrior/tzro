package compiler

import (
	"testing"

	"tzro/internal/config"
)

// TestDefaultNodeFieldsActionNodeGetsMCTSBranches verifies that action nodes
// get MCTSBranches set to config.GetMCTSMaxSimulations() by default.
func TestDefaultNodeFieldsActionNodeGetsMCTSBranches(t *testing.T) {
	node := &GraphNode{
		ID:   "action_1",
		Type: "action",
	}
	DefaultNodeFields(node)

	expected := config.GetMCTSMaxSimulations() // default 3
	if node.MCTSBranches != expected {
		t.Errorf("expected MCTSBranches=%d for action node, got %d", expected, node.MCTSBranches)
	}
}

// TestDefaultNodeFieldsDeterministicNodeNoMCTS verifies that deterministic
// nodes get MCTSBranches=0 (no multi-branch evaluation).
func TestDefaultNodeFieldsDeterministicNodeNoMCTS(t *testing.T) {
	for _, nodeType := range []string{"deterministic", "synthesis", "semantic_validator", "recall"} {
		node := &GraphNode{ID: nodeType + "_1", Type: nodeType}
		DefaultNodeFields(node)
		if node.MCTSBranches != 0 {
			t.Errorf("expected MCTSBranches=0 for %s node, got %d", nodeType, node.MCTSBranches)
		}
	}
}

// TestDefaultNodeFieldsSynthesisGetsStreamOutput verifies that synthesis
// nodes get StreamOutput=true by default.
func TestDefaultNodeFieldsSynthesisGetsStreamOutput(t *testing.T) {
	node := &GraphNode{ID: "synthesis_1", Type: "synthesis"}
	DefaultNodeFields(node)
	if !node.StreamOutput {
		t.Error("expected StreamOutput=true for synthesis node")
	}
}

// TestDefaultNodeFieldsPreserveExplicitMCTSBranches verifies that explicitly
// set MCTSBranches values are not overwritten by defaults.
func TestDefaultNodeFieldsPreserveExplicitMCTSBranches(t *testing.T) {
	node := &GraphNode{
		ID:           "action_1",
		Type:         "action",
		MCTSBranches: 5, // explicitly set by planner
	}
	DefaultNodeFields(node)
	if node.MCTSBranches != 5 {
		t.Errorf("expected MCTSBranches=5 (explicit), got %d", node.MCTSBranches)
	}
}

// TestDefaultNodeFieldsProbeNodeNoMCTS verifies that probe nodes
// get MCTSBranches=0 — they use Thought Chain, not multi-branch.
func TestDefaultNodeFieldsProbeNodeNoMCTS(t *testing.T) {
	node := &GraphNode{ID: "probe_1", Type: "probe"}
	DefaultNodeFields(node)
	if node.MCTSBranches != 0 {
		t.Errorf("expected MCTSBranches=0 for probe node, got %d", node.MCTSBranches)
	}
}
