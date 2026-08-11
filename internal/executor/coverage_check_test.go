package executor

import (
	"testing"
)

func TestCheckCoverage_AllPresent(t *testing.T) {
	goal := `Analyze the following components:
1. The Kahn Compiler and topological sorting
2. The Executor and DAG processing
3. The Inference Engine and model routing`

	synthesis := `## Architecture Analysis

### Kahn Compiler
The Kahn Compiler performs topological sorting of the abstract graph into execution layers.

### Executor  
The Executor processes the DAG, dispatching tool calls and collecting results.

### Inference Engine
The Inference Engine provides model routing for both local and cloud models.`

	result := CheckCoverage(goal, synthesis)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.TotalRequired != 3 {
		t.Errorf("expected TotalRequired=3, got %d", result.TotalRequired)
	}
	if result.Covered != 3 {
		t.Errorf("expected Covered=3, got %d", result.Covered)
	}
	if len(result.Missing) != 0 {
		t.Errorf("expected no missing items, got %v", result.Missing)
	}
}

func TestCheckCoverage_PartialMissing(t *testing.T) {
	goal := `Research the following topics:
1. Compiler design and optimization
2. Executor parallelism patterns
3. Memory management strategies
4. Error handling in distributed systems
5. Thermal throttling and resource constraints`

	synthesis := `## Research Results

### Compiler Design
The compiler uses topological sorting for optimization of execution layers.

### Executor Parallelism
The executor uses goroutines for parallel DAG layer execution.

### Memory Management
The system uses SQLite-backed memory with garbage collection strategies.`

	result := CheckCoverage(goal, synthesis)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.TotalRequired != 5 {
		t.Errorf("expected TotalRequired=5, got %d", result.TotalRequired)
	}
	if result.Covered != 3 {
		t.Errorf("expected Covered=3, got %d", result.Covered)
	}
	if len(result.Missing) != 2 {
		t.Errorf("expected 2 missing items, got %d: %v", len(result.Missing), result.Missing)
	}
}

func TestCheckCoverage_NoItemList(t *testing.T) {
	// Goal has no extractable items — should return nil (skip check)
	goal := "Explore the codebase and explain the architecture"
	synthesis := "The system uses a DAG-based model."

	result := CheckCoverage(goal, synthesis)
	if result != nil {
		t.Errorf("expected nil for goal without item list, got %+v", result)
	}
}

func TestCheckCoverage_BulletList(t *testing.T) {
	goal := `Investigate these modules:
- The compiler package
- The executor package
- The inference package`

	synthesis := `The compiler package handles DAG compilation.
The executor package processes nodes.
The inference package routes model calls.`

	result := CheckCoverage(goal, synthesis)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Covered != 3 {
		t.Errorf("expected Covered=3, got %d", result.Covered)
	}
}

func TestExtractGoalItems_MixedLists(t *testing.T) {
	goal := `Research:
1. First numbered item
2. Second numbered item
- First bullet item
- Second bullet item`

	items := extractGoalItems(goal)
	if len(items) != 4 {
		t.Errorf("expected 4 items from mixed list, got %d: %v", len(items), items)
	}
}
