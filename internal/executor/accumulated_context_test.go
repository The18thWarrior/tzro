package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tzro/internal/compiler"
	"tzro/internal/memory"
)

func setupContextTestDB(t *testing.T) func() {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "context_test.db")

	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting(dbPath)

	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init test DB: %v", err)
	}

	return func() {
		memory.DB.Close()
		os.Remove(dbPath)
		memory.DB.SetDBPathForTesting(oldDBPath)
	}
}

func TestAccumulatedContext_TruncatesPerNodeOutput(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	taskID := "task_trunc_test"

	// Insert 3 completed nodes with large outputs (10K chars each)
	largeOutput := strings.Repeat("x", 10000)
	for _, nodeID := range []string{"node_1", "node_2", "node_3"} {
		if err := memory.DB.SetNodeState(taskID, nodeID, "completed", "output "+nodeID); err != nil {
			t.Fatalf("failed to set node state: %v", err)
		}
		if err := memory.DB.SetNodeRawOutput(taskID, nodeID, largeOutput); err != nil {
			t.Fatalf("failed to set raw output: %v", err)
		}
	}

	graph := &compiler.ExecutionGraph{
		Nodes: []compiler.GraphNode{
			{ID: "node_1", Action: "tool_a"},
			{ID: "node_2", Action: "tool_b"},
			{ID: "node_3", Action: "tool_c"},
		},
	}

	result := buildAccumulatedContext(taskID, graph, "action")

	// With dynamic ceiling min(3*4096, 32000) = 12288 + header overhead,
	// total output should be bounded.
	// 3 headers (~50 chars each) + potential goal prompt = ~300 chars overhead.
	maxExpected := 12288 + 500 // dynamic ceiling + generous header allowance
	if len(result) > maxExpected {
		t.Errorf("accumulated context should be ≤ %d chars, got %d", maxExpected, len(result))
	}

	// Each node should appear in the output (not dropped entirely)
	for _, nodeID := range []string{"node_1", "node_2", "node_3"} {
		if !strings.Contains(result, nodeID) {
			t.Errorf("expected node %s to appear in accumulated context", nodeID)
		}
	}

	// The original 10K per-node output should have been truncated
	// (each node gets a portion of the budget, so no single node should have 10K)
	if strings.Contains(result, largeOutput) {
		t.Error("expected per-node output to be truncated, but found full 10K content")
	}
}

func TestAccumulatedContext_SmallContentNotTruncated(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	taskID := "task_small_test"

	// Insert 3 nodes with small outputs (1K chars each, well under any budget)
	smallOutput := strings.Repeat("y", 1000)
	for _, nodeID := range []string{"node_a", "node_b", "node_c"} {
		if err := memory.DB.SetNodeState(taskID, nodeID, "completed", "output "+nodeID); err != nil {
			t.Fatalf("failed to set node state: %v", err)
		}
		if err := memory.DB.SetNodeRawOutput(taskID, nodeID, smallOutput); err != nil {
			t.Fatalf("failed to set raw output: %v", err)
		}
	}

	graph := &compiler.ExecutionGraph{
		Nodes: []compiler.GraphNode{
			{ID: "node_a", Action: "tool_x"},
			{ID: "node_b", Action: "tool_y"},
			{ID: "node_c", Action: "tool_z"},
		},
	}

	result := buildAccumulatedContext(taskID, graph, "action")

	// With 3 nodes at 1K each (3K total), no truncation should occur
	// Each node's full 1K output should be present
	nodeOutputCount := strings.Count(result, smallOutput)
	if nodeOutputCount != 3 {
		t.Errorf("expected all 3 node outputs untruncated, found %d occurrences", nodeOutputCount)
	}
}

// --- ADR-0044 Tests ---

// T1: Non-synthesis callers produce output consistent with pre-ADR-0044 behavior.
// When calling with a non-synthesis type (e.g., "action"), all completed node outputs
// should be included (within budget) and no synthesis-specific filtering should apply.
func TestAccumulatedContext_NonSynthesisPreservesAllNodes(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	taskID := "task_nonsynthesis_test"

	// Set up a realistic DAG with mixed node types
	nodes := []struct {
		id     string
		ntype  string
		action string
		output string
	}{
		{"recall_1", "recall", "recall_agent", "Recalled findings: the module uses event-driven architecture with pub/sub messaging."},
		{"validator_1", "action", "write_file", `{"content": "# Architecture\nEvent-driven pub/sub.", "path": "docs/arch.md"}`},
		{"write_1", "deterministic", "write_file", "File written successfully to docs/arch.md"},
	}

	graphNodes := make([]compiler.GraphNode, len(nodes))
	for i, n := range nodes {
		if err := memory.DB.SetNodeState(taskID, n.id, "completed", "output "+n.id); err != nil {
			t.Fatalf("failed to set node state: %v", err)
		}
		if err := memory.DB.SetNodeRawOutput(taskID, n.id, n.output); err != nil {
			t.Fatalf("failed to set raw output: %v", err)
		}
		graphNodes[i] = compiler.GraphNode{ID: n.id, Type: n.ntype, Action: n.action}
	}

	graph := &compiler.ExecutionGraph{Nodes: graphNodes}

	result := buildAccumulatedContext(taskID, graph, "action")

	// All 3 nodes should be present in the output
	for _, n := range nodes {
		if !strings.Contains(result, n.id) {
			t.Errorf("expected node %s to appear in non-synthesis context", n.id)
		}
	}

	// The deterministic node's full output should be present (not capped at 256)
	// because this is NOT a synthesis caller
	if !strings.Contains(result, "File written successfully to docs/arch.md") {
		t.Error("non-synthesis caller should include full deterministic node output")
	}

	// The recall node's output should be present
	if !strings.Contains(result, "event-driven architecture") {
		t.Error("non-synthesis caller should include recall node output")
	}
}

// T2: Superseded probe skipping still works with the new callingNodeType parameter.
func TestAccumulatedContext_SupersededProbeSkipping(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	taskID := "task_superseded_test"

	// probe → recall edge. When recall is completed, probe should be skipped.
	probeOutput := "Raw exploration: found 15 files in internal/executor/"
	recallOutput := "Synthesized findings: the executor package contains DAG execution logic."

	if err := memory.DB.SetNodeState(taskID, "probe_explore", "completed", "output probe"); err != nil {
		t.Fatalf("failed to set node state: %v", err)
	}
	if err := memory.DB.SetNodeRawOutput(taskID, "probe_explore", probeOutput); err != nil {
		t.Fatalf("failed to set raw output: %v", err)
	}
	if err := memory.DB.SetNodeState(taskID, "probe_explore_recall", "completed", "output recall"); err != nil {
		t.Fatalf("failed to set node state: %v", err)
	}
	if err := memory.DB.SetNodeRawOutput(taskID, "probe_explore_recall", recallOutput); err != nil {
		t.Fatalf("failed to set raw output: %v", err)
	}

	graph := &compiler.ExecutionGraph{
		Nodes: []compiler.GraphNode{
			{ID: "probe_explore", Type: "probe", Action: "probe_agent"},
			{ID: "probe_explore_recall", Type: "recall", Action: "recall_agent"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "probe_explore", TargetID: "probe_explore_recall"},
		},
	}

	// Test with both synthesis and non-synthesis callers
	for _, callerType := range []string{"action", "synthesis"} {
		result := buildAccumulatedContext(taskID, graph, callerType)

		// The superseded probe should NOT appear
		if strings.Contains(result, probeOutput) {
			t.Errorf("[%s caller] superseded probe output should be skipped", callerType)
		}

		// The recall node SHOULD appear
		if !strings.Contains(result, recallOutput) {
			t.Errorf("[%s caller] recall node output should be present", callerType)
		}
	}
}

// T3: Tiered budget allocation gives recall nodes more budget than deterministic nodes.
func TestAccumulatedContext_TieredBudgetAllocation(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	taskID := "task_tiered_test"

	// Create nodes with outputs larger than any single tier's budget.
	// If tiered allocation works, recall gets more chars than deterministic.
	largeOutput := strings.Repeat("z", 8000)

	nodes := []struct {
		id    string
		ntype string
	}{
		{"recall_node", "recall"},
		{"action_node", "action"},
		{"det_node", "deterministic"},
	}

	graphNodes := make([]compiler.GraphNode, len(nodes))
	for i, n := range nodes {
		if err := memory.DB.SetNodeState(taskID, n.id, "completed", "output "+n.id); err != nil {
			t.Fatalf("failed to set node state: %v", err)
		}
		if err := memory.DB.SetNodeRawOutput(taskID, n.id, largeOutput); err != nil {
			t.Fatalf("failed to set raw output: %v", err)
		}
		graphNodes[i] = compiler.GraphNode{ID: n.id, Type: n.ntype, Action: "tool_" + n.id}
	}

	graph := &compiler.ExecutionGraph{Nodes: graphNodes}
	result := buildAccumulatedContext(taskID, graph, "action")

	// Extract the per-node content lengths from the formatted output
	sections := strings.Split(result, "--- ")
	recallLen := 0
	detLen := 0
	for _, section := range sections {
		if strings.HasPrefix(section, "recall_node") {
			recallLen = len(section)
		}
		if strings.HasPrefix(section, "det_node") {
			detLen = len(section)
		}
	}

	if recallLen == 0 || detLen == 0 {
		t.Fatalf("could not find recall or deterministic sections in output")
	}

	// Recall (weight 8) should get significantly more budget than deterministic (weight 1)
	if recallLen <= detLen {
		t.Errorf("tiered allocation failed: recall section (%d chars) should be larger than deterministic section (%d chars)", recallLen, detLen)
	}
}

// T4: Dynamic ceiling bounds total context to min(nodeCount * 4096, 32000).
func TestAccumulatedContext_DynamicCeiling(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	taskID := "task_ceiling_test"

	// Create 4 nodes with 10K output each (40K total).
	// Dynamic ceiling: min(4*4096, 32000) = 16384.
	largeOutput := strings.Repeat("c", 10000)
	var graphNodes []compiler.GraphNode
	for i := 0; i < 4; i++ {
		nodeID := "node_" + string(rune('a'+i))
		if err := memory.DB.SetNodeState(taskID, nodeID, "completed", "output "+nodeID); err != nil {
			t.Fatalf("failed to set node state: %v", err)
		}
		if err := memory.DB.SetNodeRawOutput(taskID, nodeID, largeOutput); err != nil {
			t.Fatalf("failed to set raw output: %v", err)
		}
		graphNodes = append(graphNodes, compiler.GraphNode{ID: nodeID, Type: "action", Action: "tool_" + nodeID})
	}

	graph := &compiler.ExecutionGraph{Nodes: graphNodes}
	result := buildAccumulatedContext(taskID, graph, "action")

	// Total should be bounded by dynamic ceiling + headers
	maxExpected := 16384 + 600 // ceiling + header overhead
	if len(result) > maxExpected {
		t.Errorf("dynamic ceiling violated: expected ≤ %d chars, got %d", maxExpected, len(result))
	}

	// With 8 nodes, ceiling should be 32000 (the cap)
	taskID2 := "task_ceiling_test_8"
	var graphNodes2 []compiler.GraphNode
	for i := 0; i < 8; i++ {
		nodeID := "n8_" + string(rune('a'+i))
		if err := memory.DB.SetNodeState(taskID2, nodeID, "completed", "output "+nodeID); err != nil {
			t.Fatalf("failed to set node state: %v", err)
		}
		if err := memory.DB.SetNodeRawOutput(taskID2, nodeID, largeOutput); err != nil {
			t.Fatalf("failed to set raw output: %v", err)
		}
		graphNodes2 = append(graphNodes2, compiler.GraphNode{ID: nodeID, Type: "action", Action: "tool_" + nodeID})
	}

	graph2 := &compiler.ExecutionGraph{Nodes: graphNodes2}
	// Only 6 nodes fit in maxAccumulatedContextNodes window
	result2 := buildAccumulatedContext(taskID2, graph2, "action")

	maxExpected2 := 32000 + 800
	if len(result2) > maxExpected2 {
		t.Errorf("dynamic ceiling cap violated: expected ≤ %d chars, got %d", maxExpected2, len(result2))
	}
}

// T5: Synthesis nodes cap deterministic output at 256 chars.
func TestAccumulatedContext_SynthesisCapsDeterministicOutput(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	taskID := "task_synth_det_test"

	// Create a deterministic node with a long success message (5000 chars)
	// This exceeds the 4096-char budget for deterministic nodes under standard synthesis,
	// so it must be compacted.
	longSuccess := "File written successfully to /very/long/path/that/keeps/going/" + strings.Repeat("x", 5000)

	if err := memory.DB.SetNodeState(taskID, "write_node", "completed", "output write_node"); err != nil {
		t.Fatalf("failed to set node state: %v", err)
	}
	if err := memory.DB.SetNodeRawOutput(taskID, "write_node", longSuccess); err != nil {
		t.Fatalf("failed to set raw output: %v", err)
	}

	// Also add a recall node for contrast
	recallOutput := "The module architecture uses a clean pub/sub pattern."
	if err := memory.DB.SetNodeState(taskID, "recall_node", "completed", "output recall_node"); err != nil {
		t.Fatalf("failed to set node state: %v", err)
	}
	if err := memory.DB.SetNodeRawOutput(taskID, "recall_node", recallOutput); err != nil {
		t.Fatalf("failed to set raw output: %v", err)
	}

	graph := &compiler.ExecutionGraph{
		Nodes: []compiler.GraphNode{
			{ID: "recall_node", Type: "recall", Action: "recall_agent"},
			{ID: "write_node", Type: "deterministic", Action: "write_file"},
		},
	}

	result := buildAccumulatedContext(taskID, graph, "synthesis")

	// The deterministic output should be capped — the full 5000+ char output should NOT be present
	if strings.Contains(result, longSuccess) {
		t.Error("synthesis caller should cap deterministic output, but found full content")
	}

	// But the deterministic node should still appear (just truncated)
	if !strings.Contains(result, "write_node") {
		t.Error("deterministic node should still appear in synthesis context (just capped)")
	}

	// The recall output should be fully present (not capped — it's small and under ceiling)
	if !strings.Contains(result, recallOutput) {
		t.Error("recall output should be fully present in synthesis context")
	}
}

// T6: Synthesis nodes apply proportional budgets to recall/validator RawOutput
// under the 16K synthesis ceiling (ADR-0044). When total untruncated content
// exceeds the ceiling, outputs are proportionally capped. This test verifies
// that the non-synthesis path truncates large action-node outputs that exceed
// their tiered per-node budget.
func TestAccumulatedContext_SynthesisUntruncatedValidatorRecall(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	taskID := "task_synth_untrunc_test"

	// Create outputs large enough to exceed non-synthesis per-node budgets.
	// Non-synthesis path: dynamicCeiling = 3 * 4096 = 12288 chars.
	// Action (validator) weight = 6, deterministic weight = 1, recall = exempt.
	// totalWeight = 7, so action budget = (6 * 12288) / 7 ≈ 10,532 chars.
	// Validator: ~15K chars → exceeds ~10K budget under non-synthesis path.
	// Under synthesis path: total ~20K > 16K ceiling → proportionally capped too.
	validatorContent := `{"content": "` + strings.Repeat("Generated code content. ", 600) + `", "path": "output.go"}`
	recallContent := "Comprehensive synthesis: " + strings.Repeat("detailed finding. ", 250)

	nodes := []struct {
		id     string
		ntype  string
		action string
		output string
	}{
		{"validator_1", "action", "write_file_validator", validatorContent},
		{"recall_1", "recall", "recall_agent", recallContent},
		{"det_1", "deterministic", "write_file", "File written successfully"},
	}

	graphNodes := make([]compiler.GraphNode, len(nodes))
	for i, n := range nodes {
		if err := memory.DB.SetNodeState(taskID, n.id, "completed", "output "+n.id); err != nil {
			t.Fatalf("failed to set node state: %v", err)
		}
		if err := memory.DB.SetNodeRawOutput(taskID, n.id, n.output); err != nil {
			t.Fatalf("failed to set raw output: %v", err)
		}
		graphNodes[i] = compiler.GraphNode{ID: n.id, Type: n.ntype, Action: n.action}
	}

	graph := &compiler.ExecutionGraph{Nodes: graphNodes}

	// Non-synthesis caller should truncate the validator output (exceeds ~10K per-node budget)
	resultAction := buildAccumulatedContext(taskID, graph, "action")
	if strings.Contains(resultAction, validatorContent) {
		t.Error("non-synthesis caller should truncate large validator output")
	}
	// But the validator node should still appear
	if !strings.Contains(resultAction, "validator_1") {
		t.Error("validator node should still appear in non-synthesis context (just truncated)")
	}
}

// T7: Data-profile exec nodes (containing cacheId + dataProfile) are exempted from compaction.
// This is the root cause fix for datanal benchmark failures where compaction destroyed
// the cacheId envelope before analyze Probe nodes could use sql_cached_data.
func TestAccumulatedContext_DataProfileExemptFromCompaction(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	taskID := "task_dataprofile_exempt_test"

	// Simulate a data-profile exec node output (~4300 chars, typical for CSV profiler)
	dataProfileOutput := `{"dataProfile":{"columns":["name","email","account_name","Country","Sector","Lead_Source","Accout_Owner","Target_Account?","Primary_Incumbent_CDN"],"rowCount":252,"sampleRows":[{"name":"John Doe","email":"jdoe@example.com","account_name":"Acme Corp","Country":"USA"}]},"path":"/Users/test/helpers/LeadSuccess.csv","cacheId":"cache_1784603777374136000"}` + strings.Repeat(" ", 4000)
	regularOutput := strings.Repeat("z", 10000)

	// Two nodes: one data-profile (should be untruncated), one regular (should be compacted)
	if err := memory.DB.SetNodeState(taskID, "csv_exec", "completed", "output csv_exec"); err != nil {
		t.Fatalf("failed to set node state: %v", err)
	}
	if err := memory.DB.SetNodeRawOutput(taskID, "csv_exec", dataProfileOutput); err != nil {
		t.Fatalf("failed to set raw output: %v", err)
	}
	if err := memory.DB.SetNodeState(taskID, "regular_node", "completed", "output regular_node"); err != nil {
		t.Fatalf("failed to set node state: %v", err)
	}
	if err := memory.DB.SetNodeRawOutput(taskID, "regular_node", regularOutput); err != nil {
		t.Fatalf("failed to set raw output: %v", err)
	}

	graph := &compiler.ExecutionGraph{
		Nodes: []compiler.GraphNode{
			{ID: "csv_exec", Type: "action", Action: "read_file"},
			{ID: "regular_node", Type: "action", Action: "tool_other"},
		},
	}

	result := buildAccumulatedContext(taskID, graph, "action")

	// The full data-profile output should be preserved (including cacheId)
	if !strings.Contains(result, "cache_1784603777374136000") {
		t.Error("data-profile exec node should have cacheId preserved (not compacted)")
	}
	if !strings.Contains(result, "dataProfile") {
		t.Error("data-profile exec node should have dataProfile marker preserved")
	}

	// The regular node should have been compacted (10K > budget)
	if strings.Contains(result, regularOutput) {
		t.Error("regular node should have been compacted, but found full 10K content")
	}
}

// T8: Pre-extracted cacheIds appear as a trailing metadata block.
// This ensures enrichCacheBridgeContext can always find the cacheId even if
// compaction were to strip it from a node body (belt-and-suspenders with T7).
func TestAccumulatedContext_PreExtractedCacheIds(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	taskID := "task_cache_id_extract_test"

	outputWithCache := `Result from tool: {"cacheId": "cache_1784603777374136000", "dataProfile": {"columns": ["a", "b"]}}`
	if err := memory.DB.SetNodeState(taskID, "node_with_cache", "completed", "output node_with_cache"); err != nil {
		t.Fatalf("failed to set node state: %v", err)
	}
	if err := memory.DB.SetNodeRawOutput(taskID, "node_with_cache", outputWithCache); err != nil {
		t.Fatalf("failed to set raw output: %v", err)
	}

	graph := &compiler.ExecutionGraph{
		Nodes: []compiler.GraphNode{
			{ID: "node_with_cache", Type: "action", Action: "read_file"},
		},
	}

	result := buildAccumulatedContext(taskID, graph, "action")

	// Should contain the pre-extracted cacheId block
	if !strings.Contains(result, "Pre-extracted Cache IDs") {
		t.Error("expected pre-extracted cache ID metadata block in output")
	}
	if !strings.Contains(result, "cacheId: cache_1784603777374136000") {
		t.Error("expected extracted cacheId 'cache_1784603777374136000' in metadata block")
	}
}
