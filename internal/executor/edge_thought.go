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
// activation threshold, indicating that an edge thought should be generated
// for incoming edges. Threshold 0.0 = disabled (zero overhead for deterministic nodes).
func shouldGenerateEdgeThought(targetNode *compiler.GraphNode) bool {
	return targetNode.ActivationThreshold > 0.0
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
