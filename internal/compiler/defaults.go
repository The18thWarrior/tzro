package compiler

import (
	"tzro/internal/config"
)

// DefaultNodeFields applies default field values based on node type.
// Called by the Kahn Compiler after planning but before execution to infer
// operational parameters the Strategic Planner doesn't set.
//
// Per ADR-0045:
//   - Action nodes: MCTSBranches = config.GetMCTSMaxSimulations() (default 3)
//   - Synthesis nodes: StreamOutput = true (node-to-user token streaming)
//   - Deterministic/probe/validator/recall: MCTSBranches = 0 (single-shot)
//
// Explicitly set values (non-zero) are preserved — the planner can override.
func DefaultNodeFields(node *GraphNode) {
	switch node.Type {
	case "action":
		// Action nodes get multi-branch evaluation by default
		if node.MCTSBranches == 0 {
			node.MCTSBranches = config.GetMCTSMaxSimulations()
		}

	case "synthesis":
		// Synthesis nodes stream tokens to the TUI by default
		if !node.StreamOutput {
			node.StreamOutput = true
		}
		// No multi-branch for synthesis — it's a deterministic compilation step
		// MCTSBranches stays 0

	case "deterministic", "semantic_validator", "recall", "list":
		// These node types are single-shot — no multi-branch evaluation
		// MCTSBranches stays 0
	}
}

// ApplyDefaults runs DefaultNodeFields on all nodes in a graph.
// Should be called after CompileAndSort and before execution begins.
func ApplyDefaults(graph *ExecutionGraph) {
	for i := range graph.Nodes {
		DefaultNodeFields(&graph.Nodes[i])
	}
}
