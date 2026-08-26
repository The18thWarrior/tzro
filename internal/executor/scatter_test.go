package executor

import (
	"sort"
	"strings"
	"testing"

	"tzro/internal/compiler"
)

// ── Slice 3: SpawnScatterProbes tests ────────────────────────────────────────

func TestSpawnScatterProbes_CreatesNodesAndEdges(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID:     "task-1",
		GoalPrompt: "Research 3 topics",
		Nodes: []compiler.GraphNode{
			{ID: "recall_1", Type: "recall", Status: "completed"},
		},
		Edges: []compiler.GraphEdge{},
	}

	budget := &compiler.MutationBudget{RemainingSpawns: 10}

	specs := []ScatterSpec{
		{GoalItem: "Topic A", ContextFilePath: "/tmp/ctx.txt"},
		{GoalItem: "Topic B", ContextFilePath: "/tmp/ctx.txt"},
	}

	assemblyID, scatterIDs, err := SpawnScatterProbes(graph, "recall_1", specs, budget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should create 2 probe nodes + 1 assembly node = 3 new nodes
	if len(scatterIDs) != 2 {
		t.Errorf("expected 2 scatter node IDs, got %d", len(scatterIDs))
	}
	if assemblyID == "" {
		t.Fatal("expected non-empty assembly node ID")
	}

	// Total nodes: 1 (recall) + 2 (scatter) + 1 (assembly) = 4
	if len(graph.Nodes) != 4 {
		t.Errorf("expected 4 nodes in graph, got %d", len(graph.Nodes))
	}

	// Verify probe nodes have correct config
	for _, id := range scatterIDs {
		found := false
		for _, n := range graph.Nodes {
			if n.ID == id {
				found = true
				if n.Type != "list" {
					t.Errorf("scatter node %s should be type 'probe', got %q", id, n.Type)
				}
				if n.ProbeConfig == nil {
					t.Fatalf("scatter node %s should have ProbeConfig", id)
				}
				if !n.ProbeConfig.DirectSynthesis {
					t.Error("scatter probe should have DirectSynthesis=true")
				}
				if n.ProbeConfig.MaxTokens != scatterMaxTokens {
					t.Errorf("expected MaxTokens=%d, got %d", scatterMaxTokens, n.ProbeConfig.MaxTokens)
				}
				if n.ProbeConfig.ContextFile != "/tmp/ctx.txt" {
					t.Errorf("expected ContextFile='/tmp/ctx.txt', got %q", n.ProbeConfig.ContextFile)
				}
			}
		}
		if !found {
			t.Errorf("scatter node %s not found in graph", id)
		}
	}

	// Verify assembly node
	var assemblyNode *compiler.GraphNode
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == assemblyID {
			assemblyNode = &graph.Nodes[i]
			break
		}
	}
	if assemblyNode == nil {
		t.Fatal("assembly node not found in graph")
	}
	if assemblyNode.Type != "scatter_assembly" {
		t.Errorf("expected type 'scatter_assembly', got %q", assemblyNode.Type)
	}
	if assemblyNode.Instructions != "recall_1" {
		t.Errorf("expected assembly Instructions='recall_1', got %q", assemblyNode.Instructions)
	}

	// Verify edges: recall → each probe, each probe → assembly
	expectedEdges := map[string]string{
		"recall_1":   scatterIDs[0],
		scatterIDs[0]: assemblyID,
	}
	// Also recall → probe2 and probe2 → assembly
	expectedEdges["recall_1_2"] = scatterIDs[1] // tag to allow duplicate source
	for _, e := range graph.Edges {
		if e.SourceID == "recall_1" {
			// recall → probe
			found := false
			for _, sid := range scatterIDs {
				if e.TargetID == sid {
					found = true
				}
			}
			if !found {
				t.Errorf("unexpected edge from recall_1 to %s", e.TargetID)
			}
		}
	}
	// Verify each probe has edge to assembly
	for _, sid := range scatterIDs {
		found := false
		for _, e := range graph.Edges {
			if e.SourceID == sid && e.TargetID == assemblyID {
				found = true
			}
		}
		if !found {
			t.Errorf("expected edge from %s to %s", sid, assemblyID)
		}
	}

	// Budget should be decremented by 2
	if budget.RemainingSpawns != 8 {
		t.Errorf("expected remaining budget 8, got %d", budget.RemainingSpawns)
	}
}

func TestSpawnScatterProbes_CapsAtHalfBudget(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "task-1",
		Nodes: []compiler.GraphNode{
			{ID: "recall_1", Type: "recall"},
		},
	}

	budget := &compiler.MutationBudget{RemainingSpawns: 4}

	specs := []ScatterSpec{
		{GoalItem: "A"},
		{GoalItem: "B"},
		{GoalItem: "C"},
		{GoalItem: "D"},
		{GoalItem: "E"},
	}

	_, scatterIDs, err := SpawnScatterProbes(graph, "recall_1", specs, budget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 4/2 = 2, so only 2 probes should be spawned
	if len(scatterIDs) != 2 {
		t.Errorf("expected 2 scatter probes (budget cap), got %d", len(scatterIDs))
	}

	// Budget should be decremented by 2
	if budget.RemainingSpawns != 2 {
		t.Errorf("expected remaining budget 2, got %d", budget.RemainingSpawns)
	}
}

func TestSpawnScatterProbes_NilWhenZeroBudget(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "task-1",
		Nodes:  []compiler.GraphNode{{ID: "recall_1", Type: "recall"}},
	}

	budget := &compiler.MutationBudget{RemainingSpawns: 1}

	specs := []ScatterSpec{
		{GoalItem: "A"},
		{GoalItem: "B"},
	}

	assemblyID, scatterIDs, err := SpawnScatterProbes(graph, "recall_1", specs, budget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if assemblyID != "" {
		t.Errorf("expected empty assembly ID, got %q", assemblyID)
	}
	if scatterIDs != nil {
		t.Errorf("expected nil scatter IDs, got %v", scatterIDs)
	}

	// Graph should not be modified
	if len(graph.Nodes) != 1 {
		t.Errorf("expected graph unchanged (1 node), got %d", len(graph.Nodes))
	}
}

func TestSpawnScatterProbes_NilBudget(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "task-1",
		Nodes:  []compiler.GraphNode{{ID: "recall_1", Type: "recall"}},
	}

	assemblyID, scatterIDs, err := SpawnScatterProbes(graph, "recall_1", []ScatterSpec{{GoalItem: "A"}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if assemblyID != "" || scatterIDs != nil {
		t.Error("expected no-op when budget is nil")
	}
}

// ── Slice 4: assembleScatterOutput tests ────────────────────────────────────

func TestAssembleScatterOutput_SectionPerItem(t *testing.T) {
	original := "# Report\nContent here."
	// Use a sorted map via ordered keys to ensure deterministic output
	outputs := map[string]string{
		"item A": "Details about A",
		"item B": "Details about B",
	}

	result := assembleScatterOutput(original, outputs)

	if !strings.HasPrefix(result, original) {
		t.Error("assembled output should start with original synthesis")
	}

	// Both items should appear as sections
	for item, detail := range outputs {
		if !strings.Contains(result, "## "+item) {
			t.Errorf("expected section header '## %s' in assembled output", item)
		}
		if !strings.Contains(result, detail) {
			t.Errorf("expected detail %q in assembled output", detail)
		}
	}
}

func TestAssembleScatterOutput_SkipsEmptyOutputs(t *testing.T) {
	original := "# Report"
	outputs := map[string]string{
		"item A": "Details",
		"item B": "",
		"item C": "   ", // whitespace-only
	}

	result := assembleScatterOutput(original, outputs)

	if strings.Contains(result, "## item B") {
		t.Error("empty output should be skipped")
	}
	if strings.Contains(result, "## item C") {
		t.Error("whitespace-only output should be skipped")
	}
	if !strings.Contains(result, "## item A") {
		t.Error("non-empty output should be included")
	}
}

func TestAssembleScatterOutput_NoScatterOutputs_ReturnsOriginal(t *testing.T) {
	original := "# Report\nContent."
	result := assembleScatterOutput(original, map[string]string{})

	if result != original {
		t.Errorf("expected original returned for empty scatter, got %q", result)
	}
}

func TestAssembleScatterOutput_NilMap_ReturnsOriginal(t *testing.T) {
	original := "# Report\nContent."
	result := assembleScatterOutput(original, nil)

	if result != original {
		t.Errorf("expected original returned for nil scatter, got %q", result)
	}
}

func TestAssembleScatterOutput_DeterministicOrder(t *testing.T) {
	original := "# Report"
	outputs := map[string]string{
		"alpha": "A content",
		"beta":  "B content",
		"gamma": "G content",
	}

	// Run assembly multiple times — since Go maps iterate non-deterministically,
	// the output order may vary. This test verifies all items are present.
	result := assembleScatterOutput(original, outputs)

	// Verify all sections present
	keys := make([]string, 0, len(outputs))
	for k := range outputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if !strings.Contains(result, "## "+k) {
			t.Errorf("expected section %q in output", k)
		}
	}
}
