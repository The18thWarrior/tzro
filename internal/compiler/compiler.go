package compiler

import (
	"fmt"
)

type GraphNode struct {
	ID              string       `json:"id"`
	Type            string       `json:"type"`                // "action" | "deterministic" | "branch" | "merge" | "gbnf_bridge" | "synthesis" | "hypothesis" | "probe"
	Action          string       `json:"action"`              // Target tool name
	Instructions    string       `json:"instructions"`        // Core step instruction
	AllowedTools    []string     `json:"allowedTools"`        // Whitelist of permitted tools
	Condition       string       `json:"condition,omitempty"` // For logical branch nodes
	DefaultTarget   string       `json:"defaultTarget,omitempty"`
	SuggestedSkills []string     `json:"suggestedSkillIds,omitempty"` // Injected micro-skills
	Status          string       `json:"status"`                      // "pending" | "running" | "completed" | "failed" | "skipped"
	Output          string       `json:"output,omitempty"`
	OutputSchema    string       `json:"outputSchema,omitempty"` // Added for bridge nodes (GBNF grammar)
	StaticArgs      string       `json:"staticArgs,omitempty"`   // Added for pre-known arguments
	Error           string       `json:"error,omitempty"`
	RequireApproval bool         `json:"requireApproval,omitempty"` // Pause and wait for approval
	ProbeConfig     *ProbeConfig `json:"probeConfig,omitempty"`     // Configuration for probe nodes (ADR-0019)
}

// ProbeConfig configures a Probe Node's Thought Chain execution loop.
// The probe autonomously explores a codebase or data source using filesystem tools,
// persisting each reasoning step to SQLite for durability and compaction.
type ProbeConfig struct {
	Goal         string   `json:"goal"`         // The exploration objective
	AllowedTools []string `json:"allowedTools"` // Tools the probe may use (e.g., ["read_file", "list_dir", "search_files"])
	StepBudget   int      `json:"stepBudget"`   // Maximum number of Thought Chain steps before forced synthesis
	CompactEvery int      `json:"compactEvery"` // Rolling compaction frequency (every N steps)
}

type GraphEdge struct {
	SourceID string `json:"sourceId"`
	TargetID string `json:"targetId"`
}

type ExecutionGraph struct {
	TaskID    string      `json:"taskId"`
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
	MaxCycles int         `json:"maxCycles"`
	CreatedAt int64       `json:"createdAt"`
}

// CompileAndSort sorts the execution graph into sequential parallel levels.
// It returns an error if cycles are detected (violates DAG properties).
func CompileAndSort(graph *ExecutionGraph) ([][]string, error) {
	inDegree := make(map[string]int)
	adjList := make(map[string][]string)

	// Initialize maps
	for _, node := range graph.Nodes {
		inDegree[node.ID] = 0
		adjList[node.ID] = []string{}
	}

	// Build dependency relationships
	for _, edge := range graph.Edges {
		// Ensure nodes exist in the graph to avoid panic
		if _, exists := inDegree[edge.SourceID]; !exists {
			return nil, fmt.Errorf("compile error: source node %s does not exist", edge.SourceID)
		}
		if _, exists := inDegree[edge.TargetID]; !exists {
			return nil, fmt.Errorf("compile error: target node %s does not exist", edge.TargetID)
		}
		adjList[edge.SourceID] = append(adjList[edge.SourceID], edge.TargetID)
		inDegree[edge.TargetID]++
	}

	// Gather nodes with 0 incoming dependencies
	var queue []string
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	var topoOrder []string
	var executionLevels [][]string

	for len(queue) > 0 {
		levelSize := len(queue)
		var currentLevel []string

		for i := 0; i < levelSize; i++ {
			u := queue[0]
			queue = queue[1:]

			currentLevel = append(currentLevel, u)
			topoOrder = append(topoOrder, u)

			for _, v := range adjList[u] {
				inDegree[v]--
				if inDegree[v] == 0 {
					queue = append(queue, v)
				}
			}
		}
		executionLevels = append(executionLevels, currentLevel)
	}

	// Cycle detection
	if len(topoOrder) != len(graph.Nodes) {
		return nil, fmt.Errorf("compile error: graph contains cyclic dependencies")
	}

	return executionLevels, nil
}
