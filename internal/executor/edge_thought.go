package executor

import (
	"context"

	"tzro/internal/compiler"
	"tzro/internal/memory"
)

// ActivationAction represents the result of evaluating an Edge Thought
// against a target node's Activation Threshold (ADR-0024).
type ActivationAction int

const (
	// ActivationContinue means the downstream node should execute normally.
	ActivationContinue ActivationAction = iota
	// ActivationSpawn means confidence is below threshold — spawn a new node.
	ActivationSpawn
	// ActivationHalt means the goal has been achieved — skip remaining nodes.
	ActivationHalt
)

func (a ActivationAction) String() string {
	switch a {
	case ActivationContinue:
		return "continue"
	case ActivationSpawn:
		return "spawn"
	case ActivationHalt:
		return "halt"
	default:
		return "unknown"
	}
}

// EdgeThoughtInference abstracts the Local Model call that generates an EdgeThought.
// This allows mocking in tests without requiring a running model.
type EdgeThoughtInference interface {
	GenerateEdgeThought(
		ctx context.Context,
		taskID string,
		sourceNode *compiler.GraphNode,
		targetNode *compiler.GraphNode,
		sourceOutput string,
		stepIndex int,
	) (*memory.EdgeThought, error)
}

// shouldGenerateEdgeThought returns true if the target node has a non-zero
// activation threshold AND is not a structurally-deterministic node type.
//
// Recall, synthesis, and semantic_validator nodes are protected by the
// Deterministic Shield (evaluateActivationThreshold) which always returns
// ActivationContinue for them — so generating an edge thought is pure waste.
// Skipping the inference call saves ~10-15s per structurally-deterministic edge.
func shouldGenerateEdgeThought(targetNode *compiler.GraphNode) bool {
	if targetNode.ActivationThreshold <= 0.0 {
		return false
	}
	// These node types must always execute regardless of edge thought result.
	// The Deterministic Shield prevents halting them, so skip the inference call.
	switch targetNode.Type {
	case "recall", "synthesis", "semantic_validator", "analyze", "probe":
		return false
	}
	return true
}

// evaluateActivationThreshold compares the edge thought's confidence score
// against the target node's activation threshold to determine the next action.
//
// Decision matrix:
//   - GoalAchieved == true         → ActivationHalt (skip remaining nodes)
//   - Threshold == 0.0             → ActivationContinue (disabled, no evaluation)
//   - Confidence < Threshold       → ActivationSpawn (need more work)
//   - Confidence >= Threshold      → ActivationContinue (sufficient, proceed)
func evaluateActivationThreshold(et *memory.EdgeThought, targetNode *compiler.GraphNode) ActivationAction {
	if et.GoalAchieved {
		// Deterministic Shield: Never halt on recall, synthesis, or semantic_validator nodes.
		// These nodes are responsible for consolidation, presentation, and parameter alignment.
		// They must execute to provide a return result or fulfill a planned side-effect,
		// even if the model upstream believes the goal was "achieved".
		if targetNode.Type == "recall" || targetNode.Type == "synthesis" || targetNode.Type == "semantic_validator" || targetNode.Type == "analyze" || targetNode.Type == "probe" {
			return ActivationContinue
		}
		return ActivationHalt
	}

	if targetNode.ActivationThreshold <= 0.0 {
		return ActivationContinue
	}

	if et.GoalConfidence < targetNode.ActivationThreshold {
		return ActivationSpawn
	}

	return ActivationContinue
}
