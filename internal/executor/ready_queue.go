package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"tzro/internal/compiler"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/notification"
	"tzro/internal/proactivity"
	"tzro/internal/stream"
)

// contextKeyNodeID is used to pass the current node ID through the context
// to tool implementations during ready-queue execution.
type contextKeyType string

const contextKeyNodeID contextKeyType = "nodeID"

// ExecuteGraphReactive runs the graph using an event-driven ready queue
// instead of pre-computed topological levels (ADR-0024).
//
// Nodes fire as soon as all their dependencies are satisfied, enabling:
//   - Crash-resume at any step (completed nodes are skipped)
//   - Dynamic graph mutation mid-execution (spawned nodes enter the queue)
//   - True parallelism for independent nodes
//   - Neural edge traversal: Edge Thoughts + Activation Threshold gating
//
// The method locks the engine mutex to prevent concurrent graph execution.
func (e *ExecutionEngine) ExecuteGraphReactive(ctx context.Context, graph *compiler.ExecutionGraph) error {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	// Register as foreground activity so the Sentinel defers its LLM calls
	// and doesn't compete for the local model during task execution.
	proactivity.RegisterActiveUserTask(graph.TaskID)
	defer proactivity.DeregisterActiveUserTask(graph.TaskID)

	fmt.Fprintf(os.Stderr, "[Executor/RQ] Starting reactive execution for Task %s with %d nodes...\n", graph.TaskID, len(graph.Nodes))
	e.getPublisher().PublishEvent("task_started", graph.TaskID, "", "Task reactive execution initiated")

	// Cache graph for resume
	db := memory.DB.RawDB()
	if db != nil && graph != nil {
		graphBytes, err := json.Marshal(graph)
		if err == nil {
			createdAt := time.Now().Unix()
			_, _ = db.Exec("INSERT OR REPLACE INTO disk_cache (cache_id, raw_payload, envelope_json, created_at) VALUES (?, ?, ?, ?)",
				"graph_"+graph.TaskID, string(graphBytes), "", createdAt)
		}
	}

	// Build node index for fast lookup
	nodeIndex := make(map[string]*compiler.GraphNode)
	for i := range graph.Nodes {
		nodeIndex[graph.Nodes[i].ID] = &graph.Nodes[i]
	}

	// Track completed/skipped/failed nodes (for dependency satisfaction)
	resolved := make(map[string]bool) // nodeID → true if done (completed/skipped/failed)
	enqueued := make(map[string]bool) // nodeID → true if already sent to readyQueue
	var mu sync.Mutex

	// Pre-populate states: completed/skipped from checkpoint, pending for rest
	for _, node := range graph.Nodes {
		if state, ok := memory.DB.GetNodeState(graph.TaskID, node.ID); ok {
			if state.Status == "completed" || state.Status == "skipped" {
				resolved[node.ID] = true
				enqueued[node.ID] = true
				continue
			}
		}
		_ = memory.DB.SetNodeState(graph.TaskID, node.ID, "pending", "")
	}

	activeHooks := e.getHooksUnlocked()

	// Track errors from goroutines
	var firstErr error
	var errOnce sync.Once

	// Use WaitGroup to track in-flight node executions
	var wg sync.WaitGroup

	// Step index counter for edge thoughts
	stepIndex := 0

	// enqueueReady checks all unresolved nodes and enqueues those whose deps are satisfied.
	// Must be called while holding mu.
	enqueueReady := func() []string {
		var toEnqueue []string
		for _, node := range graph.Nodes {
			if resolved[node.ID] || enqueued[node.ID] {
				continue
			}
			if allDepsResolved(node.ID, graph, resolved) {
				enqueued[node.ID] = true
				toEnqueue = append(toEnqueue, node.ID)
			}
		}
		return toEnqueue
	}

	// Seed initial ready nodes
	mu.Lock()
	initialReady := enqueueReady()
	mu.Unlock()

	for len(initialReady) > 0 || !allNodesResolved(graph, resolved) {
		// Process all currently ready nodes
		for _, nodeID := range initialReady {
			wg.Add(1)
			go func(nID string) {
				defer wg.Done()

				node := nodeIndex[nID]
				if node == nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("node %s not found in graph", nID)
					})
					return
				}

				// Inject node ID into context for tool implementations
				nodeCtx := context.WithValue(ctx, contextKeyNodeID, nID)
				err := e.executeSingleNode(nodeCtx, graph, node, activeHooks)

				mu.Lock()
				defer mu.Unlock()

				if err != nil {
					if err == ErrTaskPaused {
						errOnce.Do(func() { firstErr = ErrTaskPaused })
						return
					}
					_ = memory.DB.SetNodeState(graph.TaskID, nID, "failed", err.Error())
					_, _ = notification.Send(ctx, "executor", "error",
						fmt.Sprintf("Action Node '%s' Failed", node.Action), err.Error(),
						notification.WithTaskID(graph.TaskID), notification.WithTargetID(nID))
					if statePayload, jerr := json.Marshal(map[string]string{"status": "failed", "output": err.Error()}); jerr == nil {
						e.getPublisher().PublishStream(stream.StreamChunk{
							Source: "executor", TaskID: graph.TaskID, NodeID: nID,
							Type: "node_state", Content: string(statePayload),
						})
					}
					errOnce.Do(func() {
						firstErr = fmt.Errorf("node %s execution error: %w", nID, err)
					})
					// Mark as resolved even on failure so we don't loop
					resolved[nID] = true
					return
				}

				// Mark resolved
				resolved[nID] = true

				// --- Neural Edge Traversal (ADR-0024) ---
				// After a node completes, evaluate each outgoing edge.
				// If the target has an ActivationThreshold > 0 and an EdgeThoughtGen is configured,
				// generate an Edge Thought and evaluate it against the threshold.
				if e.EdgeThoughtGen != nil {
					// Get source output for edge thought generation
					sourceOutput := ""
					if state, ok := memory.DB.GetNodeState(graph.TaskID, nID); ok {
						sourceOutput = state.Output
					}

					// Check each outgoing edge from the just-completed node
					for _, edge := range graph.Edges {
						if edge.SourceID != nID {
							continue
						}
						targetNode := nodeIndex[edge.TargetID]
						if targetNode == nil {
							// Target might be a newly-spawned node not yet in index
							for i := range graph.Nodes {
								if graph.Nodes[i].ID == edge.TargetID {
									targetNode = &graph.Nodes[i]
									nodeIndex[edge.TargetID] = targetNode
									break
								}
							}
						}
						if targetNode == nil || resolved[targetNode.ID] {
							continue
						}

						// Only generate edge thoughts for nodes with activation thresholds
						if !shouldGenerateEdgeThought(targetNode) {
							// Fire OnEdgeTraversal hook even without edge thought
							for _, h := range activeHooks {
								action, herr := h.OnEdgeTraversal(ctx, graph.TaskID, node, targetNode, nil)
								if herr != nil || action == ActionAbort {
									break
								}
							}
							continue
						}

						stepIndex++
						et, genErr := e.EdgeThoughtGen.GenerateEdgeThought(
							ctx, graph.TaskID, node, targetNode, sourceOutput, stepIndex,
						)
						if genErr != nil {
							fmt.Fprintf(os.Stderr, "[Executor/RQ] Edge thought generation failed for %s→%s: %v\n",
								nID, targetNode.ID, genErr)
							continue
						}

						// Persist edge thought
						_ = memory.DB.AddEdgeThought(*et)
						e.getPublisher().PublishEvent("edge_thought_generated", graph.TaskID, nID,
							fmt.Sprintf("Edge %s→%s: confidence=%.2f, goalAchieved=%v",
								nID, targetNode.ID, et.GoalConfidence, et.GoalAchieved))

						// Fire OnEdgeTraversal hooks
						for _, h := range activeHooks {
							action, herr := h.OnEdgeTraversal(ctx, graph.TaskID, node, targetNode, et)
							if herr != nil {
								fmt.Fprintf(os.Stderr, "[Executor/RQ] OnEdgeTraversal hook error: %v\n", herr)
								break
							}
							if action == ActionAbort {
								errOnce.Do(func() {
									firstErr = fmt.Errorf("OnEdgeTraversal hook aborted for edge %s→%s", nID, targetNode.ID)
								})
								return
							}
						}

						// Evaluate activation threshold
						activationAction := evaluateActivationThreshold(et, targetNode)
						fmt.Fprintf(os.Stderr, "[Executor/RQ] Activation gate %s→%s: confidence=%.2f, threshold=%.2f → %s\n",
							nID, targetNode.ID, et.GoalConfidence, targetNode.ActivationThreshold, activationAction)

						switch activationAction {
						case ActivationSpawn:
							// Spawn a new node between source and target
							spawnedID := fmt.Sprintf("spawned_%s_%d", nID, stepIndex)
							spawnedNode := compiler.GraphNode{
								ID:                  spawnedID,
								Type:                node.Type,
								Action:              node.Action,
								Instructions:        fmt.Sprintf("Continue work toward goal. Previous thought: %s", et.Thought),
								Status:              "pending",
								ActivationThreshold: 0.0, // Spawned nodes don't gate further
							}

							spawnErr := ApplySpawn(graph, nID, spawnedNode)
							if spawnErr != nil {
								fmt.Fprintf(os.Stderr, "[Executor/RQ] Spawn failed for %s: %v\n", nID, spawnErr)
								// Budget exhausted or dampened — continue without spawning
								continue
							}

							// Update node index with the spawned node
							nodeIndex[spawnedID] = &graph.Nodes[len(graph.Nodes)-1]
							_ = memory.DB.SetNodeState(graph.TaskID, spawnedID, "pending", "")

							fmt.Fprintf(os.Stderr, "[Executor/RQ] Spawned node %s between %s and %s (budget: %d remaining)\n",
								spawnedID, nID, targetNode.ID, graph.MutationBudget.RemainingSpawns)
							e.getPublisher().PublishEvent("node_spawned", graph.TaskID, spawnedID,
								fmt.Sprintf("Spawned between %s and %s due to low confidence (%.2f < %.2f)",
									nID, targetNode.ID, et.GoalConfidence, targetNode.ActivationThreshold))

						case ActivationHalt:
							// Goal achieved — skip the target and propagate downstream
							fmt.Fprintf(os.Stderr, "[Executor/RQ] HALT: Goal achieved at edge %s→%s. Skipping downstream.\n",
								nID, targetNode.ID)
							_ = memory.DB.SetNodeState(graph.TaskID, targetNode.ID, "skipped", "Goal achieved (halt)")
							e.getPublisher().PublishEvent("node_skipped", graph.TaskID, targetNode.ID, "Goal achieved (halt)")
							resolved[targetNode.ID] = true
							enqueued[targetNode.ID] = true
							e.propagateSkip(graph, targetNode.ID)
							// Mark all propagated nodes as resolved
							for _, gn := range graph.Nodes {
								if s, ok := memory.DB.GetNodeState(graph.TaskID, gn.ID); ok && s.Status == "skipped" {
									resolved[gn.ID] = true
									enqueued[gn.ID] = true
								}
							}

						case ActivationContinue:
							// Confidence sufficient — target will execute normally via enqueueReady
						}
					}
				}
			}(nodeID)
		}

		// Wait for all in-flight nodes to finish
		wg.Wait()

		// Check for errors
		if firstErr != nil {
			break
		}

		// After a batch completes, find newly-ready nodes
		mu.Lock()
		initialReady = enqueueReady()
		mu.Unlock()
	}

	if firstErr != nil {
		if firstErr == ErrTaskPaused {
			e.getPublisher().PublishEvent("task_paused", graph.TaskID, "", "Task execution paused by hook")
			return ErrTaskPaused
		}
		e.getPublisher().PublishEvent("task_failed", graph.TaskID, "", firstErr.Error())
		return firstErr
	}

	fmt.Fprintf(os.Stderr, "[Executor/RQ] Task %s completed successfully.\n", graph.TaskID)
	e.getPublisher().PublishEvent("task_completed", graph.TaskID, "", "Task reactive execution completed")

	// Trigger Two-Tier KV Cache Garbage Collection post-task boundary
	go func() {
		_ = inference.GlobalLocalModel.TriggerGC(context.Background())
		inference.GlobalLocalModel.CheckAndTriggerTier2GC(context.Background())
	}()

	return nil
}

// allDepsResolved checks if all dependencies of targetID are resolved.
func allDepsResolved(targetID string, graph *compiler.ExecutionGraph, resolved map[string]bool) bool {
	for _, edge := range graph.Edges {
		if edge.TargetID == targetID && !resolved[edge.SourceID] {
			return false
		}
	}
	return true
}

// allNodesResolved checks if all nodes in the graph are resolved.
func allNodesResolved(graph *compiler.ExecutionGraph, resolved map[string]bool) bool {
	for _, node := range graph.Nodes {
		if !resolved[node.ID] {
			return false
		}
	}
	return true
}
