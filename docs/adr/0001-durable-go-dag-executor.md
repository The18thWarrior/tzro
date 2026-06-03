# ADR-0001: Durable Go-Based DAG Compiler & Execution Runner

## Context & Problem Statement

Standard agent tool loops (which execute sequentially in a single Chat context) are highly brittle. If the runtime encounters network dropouts, API timeouts, or a local system crash, the entire execution context is lost. When restarted, the agent must re-run costly or non-idempotent operations (such as credit card charges or database mutations).

Additionally, zero-shot planners are prone to infinite tool-loop hallucinations when allowed to invoke tools without structure.

## Proposed Decision

We choose to implement a highly resilient, durable, and compiled **Directed Acyclic Graph (DAG) system** in Go.

1. **Strategic Planner Separation:** A cloud planner model generates an abstract representation of execution steps and dependency relations in JSON. It never invokes tools directly.
2. **Kahn Compilation Layer:** A Go Graph Compiler validates the JSON graph structure, detects invalid cyclic loops, and orders execution layers using **Kahn's Topological Sort Algorithm**.
3. **Goroutine Concurrency Gates:** Execution nodes occupying the same topological layer run concurrently in Go goroutines up to physical resource slot allocations. Steps between topological layers run with strict sequential synchronizations.
4. **State Checkpointing:** Node inputs and execution outputs are persisted to an SQLite-backed checkpoint database after each step, permitting resilient task resumes on unexpected crashes.

---

## Technical Specifications

### 1. Compiled Data Structures

```go
package domain

type GraphNode struct {
	ID              string                 `json:"id"`
	Type            string                 `json:"type"`                // "action" | "deterministic" | "branch" | "merge"
	Action          string                 `json:"action"`              // Name of the target tool (e.g. "salesforce_query")
	Instructions    string                 `json:"instructions"`        // Core objective instruction for this step
	AllowedTools    []string               `json:"allowedTools"`        // Strict whitelist subset of tools allowed
	Condition       string                 `json:"condition,omitempty"` // For logical branch nodes (evaluates parameters)
	DefaultTarget   string                 `json:"defaultTarget,omitempty"`
	SuggestedSkills []string               `json:"suggestedSkillIds,omitempty"` // Triggered procedural micro-skills
	Status          string                 `json:"status"`              // "pending" | "running" | "completed" | "failed" | "skipped"
	Output          string                 `json:"output,omitempty"`    // Execution results
}

type GraphEdge struct {
	SourceID string `json:"sourceId"`
	TargetID string `json:"targetId"`
}

type ExecutionGraph struct {
	TaskID    string      `json:"taskId"`
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
	MaxCycles int         `json:"maxCycles"` // Loop budget limit
	CreatedAt int64       `json:"createdAt"`
}
```

---

### 2. Kahn's Topological Sort Algorithm Implementation

The Go Compiler parses the abstract JSON graph, calculates in-degrees, and orders nodes into deterministic parallel levels:

```go
package compiler

import (
	"fmt"
	"tzro/domain"
)

// CompileAndSort sorts the execution graph into sequential parallel levels.
// It returns an error if cycles are detected (violates DAG properties).
func CompileAndSort(graph *domain.ExecutionGraph) ([][]string, error) {
	inDegree := make(map[string]int)
	adjList := make(map[string][]string)

	// Initialize maps
	for _, node := range graph.Nodes {
		inDegree[node.ID] = 0
		adjList[node.ID] = []string{}
	}

	// Build dependency relationships
	for _, edge := range graph.Edges {
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
```

---

### 3. SQLite Checkpointing Schema

Every state change of a running node is written to the SQLite database. If the engine restarts, it reads the `graph_node_states` table to fast-forward past completed execution branches.

```sql
CREATE TABLE graph_node_states (
    task_id        TEXT NOT NULL,
    node_id        TEXT NOT NULL,
    status         TEXT CHECK(status IN ('pending', 'running', 'completed', 'failed', 'skipped')) NOT NULL,
    output_payload TEXT,
    completed_at   INTEGER,
    PRIMARY KEY (task_id, node_id)
);
```

---

## Consequences

- **Pros:**
  - **Resilience:** Unaffected by runtime desktop crashes; resumes from last checkpointed Kahn topological level.
  - **Predictability:** Flow behavior is deterministic, removing LLM decision hallucinations during step traversal.
  - **Concurrence:** Multiple independent tool executions run in parallel, drastically reducing task runtimes.
- **Cons:**
  - **Rigidity:** Graph structures must be compiled beforehand. Mid-flight graph restructuring requires explicit compiler re-sorts and checkpoint migrations.
