package executor

import (
	"context"
	"fmt"
	"os"

	"tzro/internal/strategy"
	"tzro/internal/stream"
)

// ---------------------------------------------------------------------------
// SubDAGStrategy — strategy-owned Execute for sub_dag nodes (ADR-0069)
// ---------------------------------------------------------------------------

// SubDAGStrategy spawns an isolated child task via the SubTaskSpawner callback.
// It resolves dynamic bindings into inputs, delegates to SpawnSubTask,
// and returns the child task output. The dispatch envelope handles state
// persistence, AfterNode hooks, and event publishing.
type SubDAGStrategy struct {
	strategy.BaseStrategy
	publishState func(pub interface{ PublishStream(stream.StreamChunk) }, taskID, nodeID, status, output string)
}

// NewSubDAGStrategy creates a SubDAGStrategy.
func NewSubDAGStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *SubDAGStrategy {
	return &SubDAGStrategy{
		BaseStrategy: *base,
		publishState: publishNodeState,
	}
}

// Execute spawns the child task and returns its output.
// The dispatch envelope handles state management, hooks, and events.
func (s *SubDAGStrategy) Execute(ctx context.Context, nr *strategy.NodeRuntime) (*strategy.ExecutionResult, error) {
	node := nr.Node()
	graph := nr.Graph()
	taskID := nr.TaskID()

	// Set initial state — "waiting_on_child" is specific to sub_dag semantics
	_ = nr.State().SetNodeState("waiting_on_child", "")
	nr.Publisher().PublishEvent("node_started", taskID, node.ID,
		fmt.Sprintf("Spawning child task for Sub-DAG '%s'", node.Action))
	s.publishState(nr.Publisher(), taskID, node.ID, "waiting_on_child", "")

	if SpawnSubTask == nil {
		return &strategy.ExecutionResult{
			Output:    "SpawnSubTask is not initialized; cannot execute sub_dag node",
			Directive: strategy.DirectiveHalt,
		}, nil
	}

	// Inject dynamic bindings into inputs if present
	finalInputs := make(map[string]interface{})
	for k, v := range node.Inputs {
		finalInputs[k] = v
	}
	if len(node.DynamicBindings) > 0 {
		resolved := resolveDynamicBindings(ctx, node.DynamicBindings, taskID, graph)
		for paramName, rb := range resolved {
			if rb.Value != "" && rb.Value != "null" {
				finalInputs[paramName] = rb.Value
			}
		}
	}

	output, err := SpawnSubTask(ctx, node.Action, finalInputs, taskID, node.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[SubDAGStrategy] Child task '%s' failed: %v\n", node.Action, err)
		return &strategy.ExecutionResult{
			Output:    fmt.Sprintf("sub_dag node '%s' execution failed: %v", node.Action, err),
			Directive: strategy.DirectiveHalt,
		}, nil
	}

	fmt.Fprintf(os.Stderr, "[SubDAGStrategy] Child task '%s' completed (%d chars output)\n",
		node.Action, len(output))

	// Return output — envelope handles state, hooks, and events
	return &strategy.ExecutionResult{
		Output:    fmt.Sprintf("[SubDAG] %s", output),
		Directive: strategy.DirectiveContinue,
	}, nil
}

// Compile-time interface check.
var _ strategy.NodeStrategy = (*SubDAGStrategy)(nil)
