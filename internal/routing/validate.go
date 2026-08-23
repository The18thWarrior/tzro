package routing

import (
	"context"
	"fmt"
	"strings"

	"tzro/internal/compiler"
	"tzro/internal/telemetry"
	"tzro/internal/templates"
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
	if graph == nil || len(graph.Nodes) == 0 {
		return fmt.Errorf("plan graph is empty")
	}

	// Structural validation: cycle detection
	if _, err := compiler.CompileAndSort(graph); err != nil {
		return fmt.Errorf("graph has cycles or invalid structure: %w", err)
	}

	// Tool conformance check
	invalidTools := findInvalidTools(graph, toolExists)
	if len(invalidTools) > 0 {
		var toolNames []string
		for _, it := range invalidTools {
			toolNames = append(toolNames, fmt.Sprintf("%s (in node %s)", it.ToolName, it.NodeID))
		}
		return fmt.Errorf("plan references unregistered tools: %s", strings.Join(toolNames, ", "))
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
// When cloud fallback is prohibited (local-only mode), it deterministically falls
// back to the base hydrated template from the Plan Template Registry (ADR-0087).
//
// The repair flow:
//  1. Local plan → validate
//  2. If invalid tools found → surgically repair the graph (up to maxRepairAttempts)
//  3. If repair exhausted → baseline fallback (local-only) or cloud escalation
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
				break // structural issue, escalate or fallback
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

	// Repair exhausted or structural issue — escalate or baseline fallback (ADR-0087)
	if !decision.AllowCloudFallback {
		if graph != nil {
			telemetry.Default.PublishEvent("plan_baseline_fallback", graph.TaskID, "",
				"Local plan invalid after repair and cloud fallback prohibited. Falling back to base template.")
			baseModality := templates.SourceModality(graph.SourceModality)
			if baseModality == "" {
				baseModality = templates.SourceLocal
			}
			fallbackGraph := templates.GetWithModality(templates.ProbeSynthesis, baseModality)
			fallbackGraph.TaskID = graph.TaskID
			fallbackGraph.GoalPrompt = graph.GoalPrompt
			fallbackGraph.CreatedAt = graph.CreatedAt
			fallbackGraph.SourceModality = string(baseModality)
			for i := range fallbackGraph.Nodes {
				if fallbackGraph.Nodes[i].Type == "probe" {
					fallbackGraph.Nodes[i].Instructions = graph.GoalPrompt
					if fallbackGraph.Nodes[i].ProbeConfig != nil {
						fallbackGraph.Nodes[i].ProbeConfig.Goal = graph.GoalPrompt
					}
				}
			}
			return fallbackGraph, nil
		}
		return nil, fmt.Errorf("local plan invalid after repair and cloud fallback blocked by privacy policy")
	}

	telemetry.Default.PublishEvent("plan_validation_failed", graph.TaskID, "",
		"Local plan failed validation after repair. Escalating to cloud.")
	return cloudPlan(ctx)
}

// repairGraphWithProbe surgically patches a graph by replacing all nodes with
// invalid tools with a single probe node. This preserves the graph structure
// while fixing the hallucinated-tools failure mode.
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

	// Check if this task is web-oriented (ADR-0087)
	isWeb := graph.SourceModality == "web"
	if !isWeb {
		for _, node := range graph.Nodes {
			if node.ProbeConfig != nil && node.ProbeConfig.SourceHint == "web" {
				isWeb = true
				break
			}
			for _, tool := range node.AllowedTools {
				if tool == "web_search" || tool == "web_browse" {
					isWeb = true
					break
				}
			}
		}
	}

	var repairGoal string
	if isDataAnalysis {
		repairGoal = "Analyze the data to answer the following:\n"
	} else if isWeb {
		repairGoal = "Research the following on the web:\n"
	} else {
		repairGoal = "Explore and complete the following objectives:\n"
	}
	// Include the overall task goal so the repair probe stays on-topic.
	if graph.GoalPrompt != "" {
		repairGoal = fmt.Sprintf("Overall task goal: %s\n\n%s", graph.GoalPrompt, repairGoal)
	}
	for i, instr := range removedInstructions {
		repairGoal += fmt.Sprintf("%d. %s\n", i+1, instr)
	}

	repairID := "repair_probe"
	var repairNode compiler.GraphNode

	if isDataAnalysis {
		repairNode = compiler.GraphNode{
			ID:           repairID,
			Type:         "analyze",
			Action:       "",
			Instructions: repairGoal,
			Status:       "pending",
		}
	} else if isWeb {
		probeTools := []string{"web_search", "web_browse"}
		repairNode = compiler.GraphNode{
			ID:           repairID,
			Type:         "probe",
			Action:       "",
			Instructions: repairGoal,
			AllowedTools: probeTools,
			Status:       "pending",
			ProbeConfig: &compiler.ProbeConfig{
				Goal:            repairGoal,
				AllowedTools:    probeTools,
				StepBudget:      20,
				CompactEvery:    3,
				CompactionLevel: compiler.CompactPreserve,
				SourceHint:      "web",
			},
		}
	} else {
		probeTools := []string{"read_file", "list_dir", "search_files"}
		repairNode = compiler.GraphNode{
			ID:           repairID,
			Type:         "probe",
			Action:       "",
			Instructions: repairGoal,
			AllowedTools: probeTools,
			Status:       "pending",
			ProbeConfig: &compiler.ProbeConfig{
				Goal:            repairGoal,
				AllowedTools:    probeTools,
				StepBudget:      20,
				CompactEvery:    3,
				CompactionLevel: compiler.CompactPreserve,
				SourceHint:      "filesystem",
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
