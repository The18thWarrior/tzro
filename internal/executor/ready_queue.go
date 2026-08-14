package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"tzro/internal/compiler"
	"tzro/internal/config"
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
	if e.Registry == nil {
		e.InitRegistry()
	}

	// ADR-0063: Background tasks must wait for all foreground activity to clear
	// before acquiring compute resources. This prevents background work from
	// competing with foreground tasks for the sidecar inference slot.
	if !graph.IsForeground {
		if err := proactivity.WaitForForegroundClear(ctx); err != nil {
			return fmt.Errorf("background task %s cancelled while waiting for foreground: %w", graph.TaskID, err)
		}
	}

	e.mutex.Lock()
	defer e.mutex.Unlock()

	// Register as foreground activity so the Sentinel defers its LLM calls
	// and doesn't compete for the local model during task execution.
	if graph.IsForeground {
		proactivity.RegisterActiveUserTask(graph.TaskID)
		defer proactivity.DeregisterActiveUserTask(graph.TaskID)
	}

	// Pre-task GC: Clear KV cache slots from previous tasks to prevent
	// memory pressure degradation in sequential runs (e.g., benchmarks).
	_ = inference.GlobalLocalModel.TriggerGC(ctx)

	// Initialize default mutation budget if not set to prevent nil pointer panics on spawn logging/evaluation
	if graph != nil && graph.MutationBudget == nil {
		graph.MutationBudget = &compiler.MutationBudget{MaxSpawns: 15, RemainingSpawns: 15}
	}

	// ADR-0045: Initialize MaxDepth from config if not explicitly set by planner
	if graph.MutationBudget != nil && graph.MutationBudget.MaxDepth <= 0 {
		graph.MutationBudget.MaxDepth = config.GetMCTSMaxDepth()
	}

	// ADR-0045: Register PreFlect hook for corrective micro-skill injection (task-scoped)
	// Note: We append directly to e.hooks because we already hold e.mutex.Lock().
	preflectHook := &PreFlectHook{
		SkillFinder: func(toolName string) []memory.Skill {
			return memory.DB.GetRelevantSkills("corrective: "+toolName, 3)
		},
	}
	e.hooks = append(e.hooks, preflectHook)
	defer func() {
		// Remove PreFlect hook after execution (still under e.mutex)
		for i, h := range e.hooks {
			if h == preflectHook {
				e.hooks = append(e.hooks[:i], e.hooks[i+1:]...)
				break
			}
		}
	}()

	// ADR-0045: Apply compiler-inferred defaults (MCTSBranches, StreamOutput) based on node type
	compiler.ApplyDefaults(graph)

	fmt.Fprintf(os.Stderr, "[Executor/RQ] Starting reactive execution for Task %s with %d nodes...\n", graph.TaskID, len(graph.Nodes))

	// ADR-0040: Auto-sequential for benchmarks to prevent sidecar resource contention.
	if strings.HasPrefix(graph.TaskID, "comparison_") || strings.HasPrefix(graph.TaskID, "benchmark_") {
		fmt.Fprintf(os.Stderr, "[Executor/RQ] Benchmark task detected. Enabling node-level sequential execution.\n")
		e.Sequential = true
	}

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
			if e.Sequential {
				node := nodeIndex[nodeID]
				if node == nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("node %s not found in graph", nodeID)
					})
					continue
				}

				// Inject node ID into context for tool implementations
				nodeCtx := context.WithValue(ctx, contextKeyNodeID, nodeID)
				err := e.executeSingleNode(nodeCtx, graph, node, activeHooks)

				mu.Lock()
				if err != nil {
					if err == ErrTaskPaused {
						errOnce.Do(func() { firstErr = ErrTaskPaused })
						mu.Unlock()
						return firstErr
					}
					_ = memory.DB.SetNodeState(graph.TaskID, nodeID, "failed", err.Error())
					_, _ = notification.Send(ctx, "executor", "error",
						fmt.Sprintf("Action Node '%s' Failed", node.Action), err.Error(),
						notification.WithTaskID(graph.TaskID), notification.WithTargetID(nodeID))
					if statePayload, jerr := json.Marshal(map[string]string{"status": "failed", "output": err.Error()}); jerr == nil {
						e.getPublisher().PublishStream(stream.StreamChunk{
							Source: "executor", TaskID: graph.TaskID, NodeID: nodeID,
							Type: "node_state", Content: string(statePayload),
						})
					}
					errOnce.Do(func() {
						firstErr = fmt.Errorf("node %s execution error: %w", nodeID, err)
					})
					resolved[nodeID] = true
					if strings.HasPrefix(nodeID, "spawned_") && graph.MutationBudget != nil {
						graph.MutationBudget.ConsecutiveFailures++
					}
					mu.Unlock()
					continue
				}

				resolved[nodeID] = true
				// Handle neural edge traversal synchronously
				e.handleEdgeTraversal(ctx, graph, nodeIndex, node, resolved, &stepIndex, activeHooks, &firstErr, &errOnce)
				mu.Unlock()
			} else {
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
						if strings.HasPrefix(nID, "spawned_") && graph.MutationBudget != nil {
							graph.MutationBudget.ConsecutiveFailures++
						}
						return
					}

					// Mark resolved
					resolved[nID] = true

					// --- Cache Bridge Runtime Injection (spec §4.3) ---
					// If a node's output contains cacheId and dataProfile, and no
					// downstream node has cache tools, inject a cache bridge node.
					e.maybeInjectCacheBridge(graph, nodeIndex, node, nID)

					// --- Neural Edge Traversal (ADR-0024) ---
					e.handleEdgeTraversal(ctx, graph, nodeIndex, node, resolved, &stepIndex, activeHooks, &firstErr, &errOnce)
				}(nodeID)
			}
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

// buildSpawnChainContext collects outputs from completed spawned nodes in the chain
// and applies rolling compaction via TruncateSynthesisContext.
func buildSpawnChainContext(graph *compiler.ExecutionGraph, sourceID, targetID string) string {
	var steps []SynthesisStep
	for _, node := range graph.Nodes {
		if !strings.HasPrefix(node.ID, "spawned_") {
			continue
		}
		if state, ok := memory.DB.GetNodeState(graph.TaskID, node.ID); ok && state.Status == "completed" {
			output := state.RawOutput
			if output == "" {
				output = state.Output
			}
			steps = append(steps, SynthesisStep{Thought: node.Instructions, ToolOutput: output})
		}
	}
	if len(steps) == 0 {
		return ""
	}
	return TruncateSynthesisContext(steps)
}

// findSpawnedNodesInChain returns IDs of spawned nodes between source and target.
func findSpawnedNodesInChain(graph *compiler.ExecutionGraph, sourceID, targetID string) []string {
	var spawned []string
	for _, node := range graph.Nodes {
		if strings.HasPrefix(node.ID, "spawned_") && strings.Contains(node.ID, sourceID) {
			spawned = append(spawned, node.ID)
		}
	}
	return spawned
}

// buildSynthesisInstructions generates format-constrained synthesis instructions.
func buildSynthesisInstructions(graph *compiler.ExecutionGraph, targetNode *compiler.GraphNode) string {
	base := fmt.Sprintf("Synthesize all exploration findings for: %s", graph.GoalPrompt)
	switch targetNode.OutputFormat {
	case "source_code":
		return fmt.Sprintf("%s\n\nCRITICAL: Output ONLY compilable %s source code.\nNo markdown, no explanations, no summaries. Complete file content only.", base, targetNode.OutputLanguage)
	default:
		return base + "\nProduce a comprehensive, structured final answer."
	}
}

// injectSynthesisNode inserts a synthesis node between spawned nodes and the target,
// re-wiring edges so spawned nodes feed into synthesis, which feeds target.
func injectSynthesisNode(graph *compiler.ExecutionGraph, sourceID, targetID string, synthNode compiler.GraphNode) {
	graph.Nodes = append(graph.Nodes, synthNode)
	var newEdges []compiler.GraphEdge
	for _, edge := range graph.Edges {
		if strings.HasPrefix(edge.SourceID, "spawned_") && edge.TargetID == targetID {
			// Redirect spawned→target to spawned→synth
			newEdges = append(newEdges, compiler.GraphEdge{SourceID: edge.SourceID, TargetID: synthNode.ID})
		} else {
			newEdges = append(newEdges, edge)
		}
	}
	// Add synth → target edge
	newEdges = append(newEdges, compiler.GraphEdge{SourceID: synthNode.ID, TargetID: targetID})
	graph.Edges = newEdges
}

// handleEdgeTraversal handles the neural edge traversal logic after a node completes.
func (e *ExecutionEngine) handleEdgeTraversal(ctx context.Context, graph *compiler.ExecutionGraph, nodeIndex map[string]*compiler.GraphNode, node *compiler.GraphNode, resolved map[string]bool, stepIndex *int, activeHooks []ExecutionHook, firstErr *error, errOnce *sync.Once) {
	if e.EdgeThoughtGen == nil {
		return
	}

	nID := node.ID
	sourceOutput := ""
	if state, ok := memory.DB.GetNodeState(graph.TaskID, nID); ok {
		sourceOutput = state.Output
	}

	for _, edge := range graph.Edges {
		if edge.SourceID != nID {
			continue
		}
		targetNode := nodeIndex[edge.TargetID]
		if targetNode == nil {
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

		if !shouldGenerateEdgeThought(targetNode) {
			for _, h := range activeHooks {
				action, herr := h.OnEdgeTraversal(ctx, graph.TaskID, node, targetNode, nil)
				if herr != nil || action == ActionAbort {
					break
				}
			}
			continue
		}

		*stepIndex++
		et, genErr := e.EdgeThoughtGen.GenerateEdgeThought(
			ctx, graph.TaskID, node, targetNode, sourceOutput, *stepIndex,
		)
		if genErr != nil {
			fmt.Fprintf(os.Stderr, "[Executor/RQ] Edge thought generation failed for %s→%s: %v\n",
				nID, targetNode.ID, genErr)
			continue
		}

		_ = memory.DB.AddEdgeThought(*et)
		e.getPublisher().PublishEvent("edge_thought_generated", graph.TaskID, nID,
			fmt.Sprintf("Edge %s→%s: confidence=%.2f, goalAchieved=%v",
				nID, targetNode.ID, et.GoalConfidence, et.GoalAchieved))

		for _, h := range activeHooks {
			action, herr := h.OnEdgeTraversal(ctx, graph.TaskID, node, targetNode, et)
			if herr != nil {
				fmt.Fprintf(os.Stderr, "[Executor/RQ] OnEdgeTraversal hook error: %v\n", herr)
				break
			}
			if action == ActionAbort {
				errOnce.Do(func() {
					*firstErr = fmt.Errorf("OnEdgeTraversal hook aborted for edge %s→%s", nID, targetNode.ID)
				})
				return
			}
		}

		activationAction := evaluateActivationThreshold(et, targetNode)
		fmt.Fprintf(os.Stderr, "[Executor/RQ] Activation gate %s→%s: confidence=%.2f, threshold=%.2f → %s\n",
			nID, targetNode.ID, et.GoalConfidence, targetNode.ActivationThreshold, activationAction)

		if e.ProgressGuard != nil && (activationAction == ActivationHalt || activationAction == ActivationContinue) {
			if !e.ProgressGuard.VerifySufficientProgress(ctx, graph.GoalPrompt, sourceOutput, et) {
				fmt.Fprintf(os.Stderr, "[Executor/RQ] Guard OVERRIDE: Hallucinated sufficiency detected for %s→%s. Demoting to SPAWN.\n",
					nID, targetNode.ID)
				activationAction = ActivationSpawn
				et.Thought = "Guard detected insufficient content in output (hallucinated completeness). Continuing exploration."
			}
		}

		switch activationAction {
		case ActivationSpawn:
			if strings.HasPrefix(nID, "spawned_") && graph.MutationBudget != nil {
				graph.MutationBudget.ConsecutiveFailures++
			}

			// ADR-0045: Enforce spawn depth limit
			if !canSpawnAtDepth(graph, nID) {
				fmt.Fprintf(os.Stderr, "[Executor/RQ] Spawn BLOCKED for %s→%s: depth limit reached (maxDepth=%d)\n",
					nID, targetNode.ID, graph.MutationBudget.MaxDepth)
				continue
			}

			spawnedID := fmt.Sprintf("spawned_%s_%d", nID, *stepIndex)
			chainContext := buildSpawnChainContext(graph, nID, targetNode.ID)

			spawnedType := "action"
			spawnedOutputFormat := targetNode.OutputFormat
			spawnedOutputLanguage := targetNode.OutputLanguage
			if node.OutputFormat == "source_code" {
				spawnedType = "synthesis"
				spawnedOutputFormat = node.OutputFormat
				spawnedOutputLanguage = node.OutputLanguage
			}

			spawnedNode := compiler.GraphNode{
				ID:                  spawnedID,
				Type:                spawnedType,
				Action:              node.Action,
				AllowedTools:        node.AllowedTools,
				Instructions:        fmt.Sprintf("Goal: %s\n\nAccumulated Context:\n%s\n\nPrevious step result: %s\n\nContinue working toward the goal.", graph.GoalPrompt, chainContext, et.Thought),
				Status:              "pending",
				ActivationThreshold: 0.0,
				MCTSBranches:        0, // ADR-0045: Spawned nodes always single-shot
				OutputFormat:        spawnedOutputFormat,
				OutputLanguage:      spawnedOutputLanguage,
			}

			spawnErr := ApplySpawn(graph, nID, spawnedNode)
			if spawnErr != nil {
				fmt.Fprintf(os.Stderr, "[Executor/RQ] Spawn failed for %s: %v\n", nID, spawnErr)
				continue
			}

			nodeIndex[spawnedID] = &graph.Nodes[len(graph.Nodes)-1]
			_ = memory.DB.SetNodeState(graph.TaskID, spawnedID, "pending", "")

			remainingSpawns := 0
			if graph.MutationBudget != nil {
				remainingSpawns = graph.MutationBudget.RemainingSpawns
			}
			fmt.Fprintf(os.Stderr, "[Executor/RQ] Spawned node %s between %s and %s (budget: %d remaining)\n",
				spawnedID, nID, targetNode.ID, remainingSpawns)
			e.getPublisher().PublishEvent("node_spawned", graph.TaskID, spawnedID,
				fmt.Sprintf("Spawned between %s and %s due to low confidence (%.2f < %.2f)",
					nID, targetNode.ID, et.GoalConfidence, targetNode.ActivationThreshold))

		case ActivationHalt:
			if graph.MutationBudget != nil {
				graph.MutationBudget.ConsecutiveFailures = 0
			}

			fmt.Fprintf(os.Stderr, "[Executor/RQ] HALT: Goal achieved at edge %s→%s. Skipping downstream.\n",
				nID, targetNode.ID)
			_ = memory.DB.SetNodeState(graph.TaskID, targetNode.ID, "skipped", "Goal achieved (halt)")
			e.getPublisher().PublishEvent("node_skipped", graph.TaskID, targetNode.ID, "Goal achieved (halt)")
			resolved[targetNode.ID] = true
			e.propagateSkip(graph, targetNode.ID)
			for _, gn := range graph.Nodes {
				if s, ok := memory.DB.GetNodeState(graph.TaskID, gn.ID); ok && s.Status == "skipped" {
					resolved[gn.ID] = true
				}
			}

		case ActivationContinue:
			if graph.MutationBudget != nil {
				graph.MutationBudget.ConsecutiveFailures = 0
			}

			// ADR-0045: Multi-branch evaluation for nodes with MCTSBranches > 0.
			// Generates K candidates, classifies via Speculation Fence, scores via
			// Value Function, and injects the winning strategy into the target node.
			if shouldUseMultiBranch(targetNode) {
				ceil := config.GetMCTSSpeculationCeil()
				winner, mbErr := evaluateMultiBranch(ctx, targetNode, graph.GoalPrompt, sourceOutput, ceil)
				if mbErr != nil {
					fmt.Fprintf(os.Stderr, "[Executor/RQ] MultiBranch error for %s: %v — continuing single-shot\n",
						targetNode.ID, mbErr)
				} else if winner != nil {
					// Inject winning strategy into node instructions
					targetNode.Instructions = fmt.Sprintf("Strategy: %s\nReasoning: %s\n\n%s",
						winner.Action, winner.Reasoning, targetNode.Instructions)
					if winner.ToolName != "" {
						targetNode.Action = winner.ToolName
					}
					e.getPublisher().PublishEvent("multi_branch_selected", graph.TaskID, targetNode.ID,
						fmt.Sprintf("Selected candidate: %s (tool=%s, score=%.3f)",
							winner.Action, winner.ToolName, winner.Score))
				}
			}
			spawnedNodes := findSpawnedNodesInChain(graph, nID, targetNode.ID)
			sidecarStatus, _, _, _, _ := inference.GlobalLocalModel.GetStatusInfo()
			sidecarActive := sidecarStatus == "Active" || sidecarStatus == "Adopted"
			if len(spawnedNodes) > 0 && sidecarActive {
				synthID := fmt.Sprintf("synth_%s_%s", nID, targetNode.ID)
				synthNode := compiler.GraphNode{
					ID:             synthID,
					Type:           "synthesis",
					Instructions:   buildSynthesisInstructions(graph, targetNode),
					Status:         "pending",
					OutputFormat:   targetNode.OutputFormat,
					OutputLanguage: targetNode.OutputLanguage,
				}
				injectSynthesisNode(graph, nID, targetNode.ID, synthNode)
				nodeIndex[synthID] = &graph.Nodes[len(graph.Nodes)-1]
				_ = memory.DB.SetNodeState(graph.TaskID, synthID, "pending", "")
				fmt.Fprintf(os.Stderr, "[Executor/RQ] Injected synthesis node %s between spawns and %s\n", synthID, targetNode.ID)
				e.getPublisher().PublishEvent("node_injected", graph.TaskID, synthID,
					fmt.Sprintf("Synthesis node injected before %s", targetNode.ID))
			}
		}
	}
}
