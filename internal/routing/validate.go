package routing

import (
	"context"
	"fmt"
	"strings"

	"tzro/internal/compiler"
	"tzro/internal/telemetry"
)

// PlanFunc is a function type for plan backends (local or cloud).
type PlanFunc func(ctx context.Context) (*compiler.ExecutionGraph, error)

// maxRepairAttempts is the maximum number of plan repair iterations before
// escalating to cloud planning.
const maxRepairAttempts = 2

// ValidateGraph runs the local plan validation pipeline:
//  1. Structural check — graph is non-nil with at least one node
//  2. Cycle detection — Kahn topological sort succeeds
//  3. Tool schema conformance — every action node references a registered tool
//
// The toolExists function should return true if the tool name is registered.
func ValidateGraph(graph *compiler.ExecutionGraph, toolExists func(string) bool) error {
	// Step 1: Structural check
	if graph == nil || len(graph.Nodes) == 0 {
		return fmt.Errorf("validation failed: empty or nil graph")
	}

	// Step 2: Cycle detection via Kahn topological sort
	if _, err := compiler.CompileAndSort(graph); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Step 3: Tool schema conformance
	// Probe, synthesis, and deterministic nodes are exempt from tool checks
	invalidTools := findInvalidTools(graph, toolExists)
	if len(invalidTools) > 0 {
		return fmt.Errorf("validation failed: nodes reference unknown tools: %v", invalidTools)
	}

	return nil
}

// findInvalidTools returns a list of {nodeID, toolName} pairs for nodes that
// reference tools not in the registered tool set. Probe, analyze, synthesis, and
// deterministic nodes are exempt (they don't directly call external tools).
func findInvalidTools(graph *compiler.ExecutionGraph, toolExists func(string) bool) []InvalidTool {
	var invalid []InvalidTool
	for _, node := range graph.Nodes {
		switch node.Type {
		case "probe", "analyze", "synthesis", "deterministic":
			continue
		}
		if node.Action != "" && !toolExists(node.Action) {
			invalid = append(invalid, InvalidTool{NodeID: node.ID, ToolName: node.Action})
		}
	}
	return invalid
}

// InvalidTool records a node that references an unregistered tool.
type InvalidTool struct {
	NodeID   string
	ToolName string
}

// PlanWithEscalation attempts local planning, validates the result, and escalates
// to cloud planning if validation fails and the routing decision allows it.
//
// The repair flow (P1 benchmark fix):
//  1. Local plan → validate
//  2. If invalid tools found → surgically repair the graph (up to maxRepairAttempts)
//  3. If repair exhausted → escalate to cloud (if allowed)
//
// Repair works by replacing nodes with invalid tools with a single probe node,
// since the planner hallucinated tools typically indicate an exploration task
// that should have used a probe node in the first place.
func PlanWithEscalation(ctx context.Context, localPlan, cloudPlan PlanFunc, decision RoutingDecision, toolExists func(string) bool) (*compiler.ExecutionGraph, error) {
	// Attempt local planning
	graph, err := localPlan(ctx)
	if err != nil {
		if !decision.AllowCloudFallback {
			return nil, fmt.Errorf("local planning failed and cloud fallback blocked by privacy policy: %w", err)
		}
		telemetry.Default.PublishEvent("plan_local_error", "", "", fmt.Sprintf("Local planning error: %v. Escalating to cloud.", err))
		return cloudPlan(ctx)
	}

	// Validate the locally-produced graph
	for attempt := 0; attempt <= maxRepairAttempts; attempt++ {
		invalidTools := findInvalidTools(graph, toolExists)
		if len(invalidTools) == 0 {
			// Structural validation (cycles, empty graph)
			if _, err := compiler.CompileAndSort(graph); err != nil {
				break // structural issue, escalate
			}
			return graph, nil
		}

		if attempt == maxRepairAttempts {
			telemetry.Default.PublishEvent("plan_repair_exhausted", graph.TaskID, "",
				fmt.Sprintf("Plan repair exhausted after %d attempts. Invalid tools: %v", maxRepairAttempts, invalidTools))
			break
		}

		// Surgical repair: replace nodes with invalid tools with a probe node
		telemetry.Default.PublishEvent("plan_repair_attempt", graph.TaskID, "",
			fmt.Sprintf("Repair attempt %d: replacing %d nodes with invalid tools %v",
				attempt+1, len(invalidTools), invalidTools))

		graph = repairGraphWithProbe(graph, invalidTools)
	}

	// Repair exhausted or structural issue — escalate
	if !decision.AllowCloudFallback {
		return nil, fmt.Errorf("local plan invalid after repair and cloud fallback blocked by privacy policy")
	}

	telemetry.Default.PublishEvent("plan_validation_failed", graph.TaskID, "",
		"Local plan failed validation after repair. Escalating to cloud.")
	return cloudPlan(ctx)
}

// repairGraphWithProbe surgically patches a graph by replacing all nodes with
// invalid tools with a single probe node. This preserves the graph structure
// while fixing the hallucinated-tools failure mode.
//
// Strategy:
//  1. Remove all nodes with invalid tools and their associated edges
//  2. Insert a single probe node that covers the combined goal
//  3. Re-wire edges: nodes that depended on removed nodes now depend on the probe
//  4. Nodes that removed nodes depended on now feed into the probe
func repairGraphWithProbe(graph *compiler.ExecutionGraph, invalidTools []InvalidTool) *compiler.ExecutionGraph {
	// Build set of node IDs to remove
	removeSet := make(map[string]bool)
	var removedInstructions []string
	for _, it := range invalidTools {
		removeSet[it.NodeID] = true
		// Collect instructions from removed nodes for the probe's goal
		for _, node := range graph.Nodes {
			if node.ID == it.NodeID {
				removedInstructions = append(removedInstructions, node.Instructions)
			}
		}
	}

	// Build the replacement node. Detect data analysis tasks from the removed
	// tool names and instructions to choose between probe (exploration) and
	// analyze (data analysis) repair nodes.
	isDataAnalysis := isDataAnalysisRepair(invalidTools, removedInstructions)

	var repairGoal string
	if isDataAnalysis {
		repairGoal = "Analyze the data to answer the following:\n"
	} else {
		repairGoal = "Explore and complete the following objectives:\n"
	}
	// Include the overall task goal so the repair probe stays on-topic.
	// Without this, the probe only sees the removed node instructions which
	// are often too terse to guide exploration effectively.
	if graph.GoalPrompt != "" {
		repairGoal = fmt.Sprintf("Overall task goal: %s\n\n%s", graph.GoalPrompt, repairGoal)
	}
	for i, instr := range removedInstructions {
		repairGoal += fmt.Sprintf("%d. %s\n", i+1, instr)
	}

	repairID := "repair_probe"
	var repairNode compiler.GraphNode

	if isDataAnalysis {
		// Analyze node: cache tools are auto-provisioned by the SCT compiler.
		// We set the type and instructions; the compiler handles the rest.
		repairNode = compiler.GraphNode{
			ID:           repairID,
			Type:         "analyze",
			Action:       "",
			Instructions: repairGoal,
			Status:       "pending",
		}
	} else {
		repairNode = compiler.GraphNode{
			ID:           repairID,
			Type:         "probe",
			Action:       "",
			Instructions: repairGoal,
			AllowedTools: []string{"read_file", "list_dir", "search_files"},
			Status:       "pending",
			ProbeConfig: &compiler.ProbeConfig{
				Goal:            repairGoal,
				AllowedTools:    []string{"read_file", "list_dir", "search_files"},
				StepBudget:      20,
				CompactEvery:    3,
				CompactionLevel: compiler.CompactPreserve,
			},
		}
	}

	// Collect upstream/downstream dependencies of removed nodes
	upstreamOfRemoved := make(map[string]bool)   // nodes that fed into removed nodes
	downstreamOfRemoved := make(map[string]bool) // nodes that depended on removed nodes

	for _, edge := range graph.Edges {
		if removeSet[edge.TargetID] && !removeSet[edge.SourceID] {
			upstreamOfRemoved[edge.SourceID] = true
		}
		if removeSet[edge.SourceID] && !removeSet[edge.TargetID] {
			downstreamOfRemoved[edge.TargetID] = true
		}
	}

	// Build new node list: keep non-removed nodes, add probe
	var newNodes []compiler.GraphNode
	for _, node := range graph.Nodes {
		if !removeSet[node.ID] {
			newNodes = append(newNodes, node)
		}
	}
	newNodes = append(newNodes, repairNode)

	// Build new edges: remove edges involving removed nodes, add probe wiring
	var newEdges []compiler.GraphEdge
	for _, edge := range graph.Edges {
		if !removeSet[edge.SourceID] && !removeSet[edge.TargetID] {
			newEdges = append(newEdges, edge)
		}
	}

	// Wire upstream → repair node
	for nodeID := range upstreamOfRemoved {
		newEdges = append(newEdges, compiler.GraphEdge{
			SourceID: nodeID,
			TargetID: repairID,
		})
	}

	// Wire repair node → downstream
	for nodeID := range downstreamOfRemoved {
		newEdges = append(newEdges, compiler.GraphEdge{
			SourceID: repairID,
			TargetID: nodeID,
		})
	}

	return &compiler.ExecutionGraph{
		TaskID:         graph.TaskID,
		GoalPrompt:     graph.GoalPrompt,
		Nodes:          newNodes,
		Edges:          newEdges,
		MaxCycles:      graph.MaxCycles,
		CreatedAt:      graph.CreatedAt,
		MutationBudget: graph.MutationBudget,
	}
}

// isDataAnalysisRepair detects whether the removed invalid tools suggest a data
// analysis task. Checks both the hallucinated tool names (e.g., postgres_insert,
// sql_query, db_query) and the instructions for data analysis keywords.
func isDataAnalysisRepair(invalidTools []InvalidTool, removedInstructions []string) bool {
	// Data-oriented hallucinated tool names
	dataTools := map[string]bool{
		"postgres_insert": true, "postgres_query": true,
		"sql_query": true, "db_query": true, "local_db_query": true,
		"database_query": true, "csv_query": true, "data_query": true,
		"aggregate": true, "pivot_table": true,
	}
	for _, it := range invalidTools {
		if dataTools[it.ToolName] {
			return true
		}
	}

	// Check instructions for data analysis keywords
	for _, instr := range removedInstructions {
		lower := strings.ToLower(instr)
		for _, keyword := range []string{"count", "group", "aggregate", "average", "sum", "filter", "sort", "rank", "top ", "breakdown", "csv", "tabular"} {
			if strings.Contains(lower, keyword) {
				return true
			}
		}
	}

	return false
}
