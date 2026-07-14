package executor

import (
	"testing"

	"tzro/internal/compiler"
)

func TestMaybeInjectCacheBridge_InjectsWhenOutputHasProfile(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "test_runtime_bridge",
		Nodes: []compiler.GraphNode{
			{
				ID:     "step_1",
				Type:   "action",
				Status: "completed",
				Output: `{"dataProfile": {"format": "csv", "columns": [...]}, "cacheId": "abc123"}`,
			},
			{
				ID:           "step_2",
				Type:         "synthesis",
				Status:       "pending",
				AllowedTools: []string{},
			},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "step_1", TargetID: "step_2"},
		},
		MutationBudget: &compiler.MutationBudget{
			MaxSpawns:       5,
			RemainingSpawns: 5,
		},
	}

	nodeIndex := make(map[string]*compiler.GraphNode)
	for i := range graph.Nodes {
		nodeIndex[graph.Nodes[i].ID] = &graph.Nodes[i]
	}

	e := &ExecutionEngine{}
	e.maybeInjectCacheBridge(graph, nodeIndex, nodeIndex["step_1"], "step_1")

	// Check that cache_bridge_step_1 was injected
	if _, exists := nodeIndex["cache_bridge_step_1"]; !exists {
		t.Fatal("expected cache_bridge_step_1 to be injected")
	}

	bridge := nodeIndex["cache_bridge_step_1"]
	if bridge.Action != "jq_cached_data" {
		t.Errorf("bridge action = %q, want jq_cached_data", bridge.Action)
	}
	if bridge.Status != "pending" {
		t.Errorf("bridge status = %q, want pending", bridge.Status)
	}
}

func TestMaybeInjectCacheBridge_SkipsWhenNoProfile(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "test_no_profile",
		Nodes: []compiler.GraphNode{
			{
				ID:     "step_1",
				Type:   "action",
				Status: "completed",
				Output: `{"result": "some normal output"}`,
			},
			{
				ID:     "step_2",
				Type:   "synthesis",
				Status: "pending",
			},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "step_1", TargetID: "step_2"},
		},
	}

	nodeIndex := make(map[string]*compiler.GraphNode)
	for i := range graph.Nodes {
		nodeIndex[graph.Nodes[i].ID] = &graph.Nodes[i]
	}

	e := &ExecutionEngine{}
	e.maybeInjectCacheBridge(graph, nodeIndex, nodeIndex["step_1"], "step_1")

	if _, exists := nodeIndex["cache_bridge_step_1"]; exists {
		t.Error("should not inject bridge when output lacks dataProfile/cacheId")
	}
}

func TestMaybeInjectCacheBridge_SkipsWhenCompileTimeBridgeExists(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "test_dedup_compile",
		Nodes: []compiler.GraphNode{
			{
				ID:     "step_1",
				Type:   "action",
				Status: "completed",
				Output: `{"dataProfile": {}, "cacheId": "abc"}`,
			},
			{
				ID:           "cache_bridge_step_1",
				Type:         "action",
				Action:       "jq_cached_data",
				Status:       "pending",
				AllowedTools: []string{"jq_cached_data"},
			},
			{
				ID:     "step_2",
				Type:   "synthesis",
				Status: "pending",
			},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "step_1", TargetID: "cache_bridge_step_1"},
			{SourceID: "cache_bridge_step_1", TargetID: "step_2"},
		},
	}

	nodeIndex := make(map[string]*compiler.GraphNode)
	for i := range graph.Nodes {
		nodeIndex[graph.Nodes[i].ID] = &graph.Nodes[i]
	}

	initialNodeCount := len(graph.Nodes)
	e := &ExecutionEngine{}
	e.maybeInjectCacheBridge(graph, nodeIndex, nodeIndex["step_1"], "step_1")

	if len(graph.Nodes) != initialNodeCount {
		t.Error("should not spawn new node when compile-time bridge already exists")
	}
}

func TestMaybeInjectCacheBridge_SkipsWhenDownstreamHasCacheTools(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "test_dedup_downstream",
		Nodes: []compiler.GraphNode{
			{
				ID:     "step_1",
				Type:   "action",
				Status: "completed",
				Output: `{"dataProfile": {}, "cacheId": "abc"}`,
			},
			{
				ID:           "step_2",
				Type:         "action",
				Status:       "pending",
				AllowedTools: []string{"jq_cached_data", "read_cached_data"},
			},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "step_1", TargetID: "step_2"},
		},
	}

	nodeIndex := make(map[string]*compiler.GraphNode)
	for i := range graph.Nodes {
		nodeIndex[graph.Nodes[i].ID] = &graph.Nodes[i]
	}

	initialNodeCount := len(graph.Nodes)
	e := &ExecutionEngine{}
	e.maybeInjectCacheBridge(graph, nodeIndex, nodeIndex["step_1"], "step_1")

	if len(graph.Nodes) != initialNodeCount {
		t.Error("should not spawn bridge when downstream already has cache tools")
	}
}
