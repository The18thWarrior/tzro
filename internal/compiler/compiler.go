package compiler

import (
	"fmt"
	"time"
)

type GraphNode struct {
	ID              string                 `json:"id"`
	Type            string                 `json:"type"`                // "action" | "deterministic" | "branch" | "merge" | "semantic_validator" | "synthesis" | "hypothesis" | "probe" | "sub_dag"
	Action          string                 `json:"action"`              // Target tool name or Sub-DAG template name
	Instructions    string                 `json:"instructions"`        // Core step instruction
	AllowedTools    []string               `json:"allowedTools"`        // Whitelist of permitted tools
	Inputs          map[string]interface{} `json:"inputs,omitempty"`    // Inputs for sub_dag macro nodes
	Condition       string                 `json:"condition,omitempty"` // For logical branch nodes
	DefaultTarget   string                 `json:"defaultTarget,omitempty"`
	SuggestedSkills []string               `json:"suggestedSkillIds,omitempty"` // Injected micro-skills
	Status          string                 `json:"status"`                      // "pending" | "running" | "waiting_on_child" | "completed" | "failed" | "skipped"
	Output          string                 `json:"output,omitempty"`
	OutputSchema    string                 `json:"outputSchema,omitempty"`    // Added for bridge nodes (GBNF grammar)
	StaticArgs      string                 `json:"staticArgs,omitempty"`      // Added for pre-known arguments
	DynamicBindings map[string]interface{} `json:"dynamicBindings,omitempty"` // Upstream data dependencies: paramName → "nodeId.output.propertyName"
	Error           string                 `json:"error,omitempty"`
	RequireApproval bool                   `json:"requireApproval,omitempty"` // Pause and wait for approval
	ProbeConfig     *ProbeConfig           `json:"probeConfig,omitempty"`     // Configuration for probe nodes (ADR-0019)

	// Neural traversal fields (ADR-0024)
	ActivationThreshold float64 `json:"activationThreshold,omitempty"` // Sufficiency gate (0.0-1.0). 0.0 = disabled.

	// Codegen output constraint fields (ADR-0035)
	OutputFormat   string `json:"outputFormat,omitempty"`   // "source_code" | "" — constrains synthesis output format
	OutputLanguage string `json:"outputLanguage,omitempty"` // e.g., "go", "typescript" — target language for source_code format

	// Multi-branch Edge Thought evaluation (ADR-0045)
	MCTSBranches int  `json:"mctsBranches,omitempty"` // K candidates for multi-branch mode. 0 = single-shot.
	StreamOutput bool `json:"streamOutput,omitempty"` // Stream token generation to StreamBus for TUI consumption.
}

// CompactionLevel controls how aggressively a node's output is compacted
// during the Thought Chain compaction pass. Probe nodes default to "preserve"
// to prevent destructive summarization of raw tool output (the primary cause
// of quality loss between cloud_dag_raw and cloud_dag in benchmark-results4).
type CompactionLevel string

const (
	CompactAggressive CompactionLevel = "aggressive" // Heavy summarization, 200-char tool output truncation
	CompactModerate   CompactionLevel = "moderate"   // Summarize prose, preserve code/tables/signatures
	CompactPreserve   CompactionLevel = "preserve"   // Pass through raw output, no compaction
)

// ProbeConfig configures a Probe Node's Thought Chain execution loop.
// The probe autonomously explores a codebase or data source using filesystem tools,
// persisting each reasoning step to SQLite for durability and compaction.
type ProbeConfig struct {
	Goal            string          `json:"goal"`                      // The exploration objective
	AllowedTools    []string        `json:"allowedTools"`              // Tools the probe may use (e.g., ["read_file", "list_dir", "search_files"])
	StepBudget      int             `json:"stepBudget"`                // Maximum number of Thought Chain steps before forced synthesis
	CompactEvery    int             `json:"compactEvery"`              // Rolling compaction frequency (every N steps)
	CompactionLevel CompactionLevel `json:"compactionLevel,omitempty"` // Controls tool output truncation during compaction. Default: "preserve"
	TaskContext     string          `json:"taskContext,omitempty"`     // Original task spec/goal — pinned above exploration results so task requirements override workspace conventions

	// Direct Synthesis mode (Grilling Decision #3): bypass Thought Chain exploration
	// and run a single-shot inference against a pre-compiled context file.
	// Skips Symbol Extraction and Compaction — the pre-compiled input is already structured.
	DirectSynthesis bool   `json:"directSynthesis,omitempty"` // When true, skip Thought Chain and synthesize directly
	ContextFile     string `json:"contextFile,omitempty"`     // Absolute path to pre-compiled context file (required when DirectSynthesis=true)
}

type GraphEdge struct {
	SourceID string `json:"sourceId"`
	TargetID string `json:"targetId"`
}

type ExecutionGraph struct {
	TaskID         string          `json:"taskId"`
	ParentTaskID   string          `json:"parentTaskId,omitempty"` // ID of the parent task if this is a sub-DAG
	ParentNodeID   string          `json:"parentNodeId,omitempty"` // ID of the sub_dag node in the parent task
	GoalPrompt     string          `json:"goalPrompt,omitempty"`   // Original user prompt for downstream context
	Nodes          []GraphNode     `json:"nodes"`
	Edges          []GraphEdge     `json:"edges"`
	MaxCycles      int             `json:"maxCycles"`
	CreatedAt      int64           `json:"createdAt"`
	MutationBudget *MutationBudget `json:"mutationBudget,omitempty"` // ADR-0024: per-task spawn budget
}

// MutationBudget bounds dynamic graph expansion at runtime (ADR-0024).
// Enforced per-task to prevent runaway node spawning.
type MutationBudget struct {
	MaxSpawns           int `json:"maxSpawns"`
	RemainingSpawns     int `json:"remainingSpawns"`
	ConsecutiveFailures int `json:"consecutiveFailures"` // Dampening counter
	MaxDepth            int `json:"maxDepth,omitempty"`  // ADR-0045: recursive AGoT spawn depth limit
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

// IncrementalSort runs Kahn's topological sort on the subgraph of non-completed
// nodes. Completed nodes are excluded entirely — their outgoing edges are treated
// as satisfied dependencies (in-degree contribution removed). This enables
// efficient re-sorting after runtime graph mutations (ADR-0024) without
// reprocessing already-executed nodes.
func IncrementalSort(graph *ExecutionGraph, completedNodes map[string]bool) ([][]string, error) {
	if completedNodes == nil {
		completedNodes = map[string]bool{}
	}

	inDegree := make(map[string]int)
	adjList := make(map[string][]string)

	// Initialize only non-completed nodes
	for _, node := range graph.Nodes {
		if completedNodes[node.ID] {
			continue
		}
		inDegree[node.ID] = 0
		adjList[node.ID] = []string{}
	}

	// Build dependency relationships, skipping edges from/to completed nodes
	for _, edge := range graph.Edges {
		sourceCompleted := completedNodes[edge.SourceID]
		targetCompleted := completedNodes[edge.TargetID]

		// Skip edges where the target is completed (nothing to schedule)
		if targetCompleted {
			continue
		}

		// If source is completed, the dependency is already satisfied — don't count it
		if sourceCompleted {
			continue
		}

		// Both source and target are pending — normal edge
		if _, exists := inDegree[edge.SourceID]; !exists {
			return nil, fmt.Errorf("compile error: source node %s does not exist", edge.SourceID)
		}
		if _, exists := inDegree[edge.TargetID]; !exists {
			return nil, fmt.Errorf("compile error: target node %s does not exist", edge.TargetID)
		}
		adjList[edge.SourceID] = append(adjList[edge.SourceID], edge.TargetID)
		inDegree[edge.TargetID]++
	}

	// Kahn's algorithm on the pending subgraph
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

	// Cycle detection: count pending nodes
	pendingCount := 0
	for _, node := range graph.Nodes {
		if !completedNodes[node.ID] {
			pendingCount++
		}
	}
	if len(topoOrder) != pendingCount {
		return nil, fmt.Errorf("compile error: graph contains cyclic dependencies among pending nodes")
	}

	return executionLevels, nil
}

// NodeTimeBudgets defines the time budget allocation per node type for the
// weighted circuit breaker (P2). Values are based on empirical benchmark
// observations: probe nodes need ~10min for 20-step exploration, action nodes
// need ~5min for cloud escalation + retries, deterministic/validator nodes
// are fast local inference.
var NodeTimeBudgets = map[string]time.Duration{
	"probe":              10 * time.Minute,
	"action":             5 * time.Minute,
	"deterministic":      90 * time.Second,
	"semantic_validator": 90 * time.Second,
	"synthesis":          90 * time.Second,
}

// ComputeTimeBudget calculates the total weighted time budget for a graph
// based on its node composition. Used by the executor's circuit breaker
// to prevent runaway task execution.
func ComputeTimeBudget(graph *ExecutionGraph) time.Duration {
	var total time.Duration
	for _, node := range graph.Nodes {
		if budget, ok := NodeTimeBudgets[node.Type]; ok {
			total += budget
		} else {
			total += 90 * time.Second // default for unknown types
		}
	}
	return total
}
