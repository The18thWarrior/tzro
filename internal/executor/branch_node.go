package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"tzro/internal/compiler"
	"tzro/internal/memory"
	"tzro/internal/stream"
)

// executeBranchNode handles branch/conditional node evaluation.
// Extracted from executeSingleNode as part of strategy extraction (ADR-0069).
//
// Branch nodes evaluate a condition and either complete (satisfied) or
// propagate skip (not satisfied) to all downstream nodes.
func (e *ExecutionEngine) executeBranchNode(
	ctx context.Context,
	graph *compiler.ExecutionGraph,
	node *compiler.GraphNode,
) error {
	taskID := graph.TaskID

	satisfied, err := e.evaluateBranchCondition(ctx, graph, node)
	if err != nil {
		return fmt.Errorf("failed to evaluate branch condition for node %s: %w", node.ID, err)
	}

	if satisfied {
		fmt.Fprintf(os.Stderr, "[Executor] Branch node %s condition satisfied!\n", node.ID)
		_ = memory.DB.SetNodeState(taskID, node.ID, "completed", "Condition satisfied")
		e.getPublisher().PublishEvent("node_completed", taskID, node.ID, "Condition satisfied")
		if statePayload, err := json.Marshal(map[string]string{"status": "completed", "output": "Condition satisfied"}); err == nil {
			e.getPublisher().PublishStream(stream.StreamChunk{
				Source:  "executor",
				TaskID:  taskID,
				NodeID:  node.ID,
				Type:    "node_state",
				Content: string(statePayload),
			})
		}
		return nil
	}

	fmt.Fprintf(os.Stderr, "[Executor] Branch node %s condition NOT satisfied. Skipping branch and propagating skip...\n", node.ID)
	_ = memory.DB.SetNodeState(taskID, node.ID, "skipped", "Condition not satisfied")
	e.getPublisher().PublishEvent("node_skipped", taskID, node.ID, "Condition not satisfied")
	if statePayload, err := json.Marshal(map[string]string{"status": "skipped", "output": "Condition not satisfied"}); err == nil {
		e.getPublisher().PublishStream(stream.StreamChunk{
			Source:  "executor",
			TaskID:  taskID,
			NodeID:  node.ID,
			Type:    "node_state",
			Content: string(statePayload),
		})
	}
	e.propagateSkip(graph, node.ID)
	return nil
}
