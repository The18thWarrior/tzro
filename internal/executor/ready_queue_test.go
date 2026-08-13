package executor

import (
	"context"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"tzro/internal/compiler"
	"tzro/internal/memory"
	"tzro/internal/tools"
)

// TestReadyQueueLinearDAGExecutesInOrder verifies that a simple linear DAG
// (A → B → C) executes nodes in topological order via the ready queue.
func TestReadyQueueLinearDAGExecutesInOrder(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_rq_linear.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_rq_linear.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	var executionOrder []string
	var mu sync.Mutex

	tools.Register(&MockTool{
		ToolName:   "rq_linear_tool",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			// Extract which node this is being called for from context
			nodeID := ctx.Value(contextKeyNodeID)
			mu.Lock()
			if nodeID != nil {
				executionOrder = append(executionOrder, nodeID.(string))
			}
			mu.Unlock()
			return `{"status": "ok"}`, nil
		},
	})
	defer tools.Unregister("rq_linear_tool")

	graph := &compiler.ExecutionGraph{
		TaskID: "task-rq-linear",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "deterministic", Action: "rq_linear_tool", Instructions: "Run A"},
			{ID: "B", Type: "deterministic", Action: "rq_linear_tool", Instructions: "Run B"},
			{ID: "C", Type: "deterministic", Action: "rq_linear_tool", Instructions: "Run C"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "B", TargetID: "C"},
		},
		CreatedAt: time.Now().Unix(),
	}

	engine := &ExecutionEngine{}
	engine.InitRegistry()
	ctx := context.Background()

	err := engine.ExecuteGraphReactive(ctx, graph)
	if err != nil {
		t.Fatalf("ExecuteGraphReactive failed: %v", err)
	}

	// Verify all nodes completed
	for _, nodeID := range []string{"A", "B", "C"} {
		state, ok := memory.DB.GetNodeState("task-rq-linear", nodeID)
		if !ok || state.Status != "completed" {
			t.Errorf("expected node %s to be completed, got: %+v", nodeID, state)
		}
	}

	// Verify execution order is A, B, C (serial due to dependencies)
	if len(executionOrder) != 3 || executionOrder[0] != "A" || executionOrder[1] != "B" || executionOrder[2] != "C" {
		t.Errorf("expected execution order [A B C], got %v", executionOrder)
	}
}

// TestReadyQueueParallelDAGFiresConcurrently verifies that independent nodes
// (B and C, both depending only on A) are fired concurrently.
func TestReadyQueueParallelDAGFiresConcurrently(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_rq_parallel.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_rq_parallel.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	var startTimes sync.Map // nodeID → time

	tools.Register(&MockTool{
		ToolName:   "rq_parallel_tool",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			nodeID := ctx.Value(contextKeyNodeID)
			if nodeID != nil {
				startTimes.Store(nodeID.(string), time.Now())
			}
			// Sleep briefly so we can detect overlap
			time.Sleep(50 * time.Millisecond)
			return `{"status": "ok"}`, nil
		},
	})
	defer tools.Unregister("rq_parallel_tool")

	// Graph: A → B, A → C (B and C should run in parallel after A completes)
	graph := &compiler.ExecutionGraph{
		TaskID: "task-rq-parallel",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "deterministic", Action: "rq_parallel_tool", Instructions: "Run A"},
			{ID: "B", Type: "deterministic", Action: "rq_parallel_tool", Instructions: "Run B"},
			{ID: "C", Type: "deterministic", Action: "rq_parallel_tool", Instructions: "Run C"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "A", TargetID: "C"},
		},
		CreatedAt: time.Now().Unix(),
	}

	engine := &ExecutionEngine{}
	engine.InitRegistry()
	err := engine.ExecuteGraphReactive(context.Background(), graph)
	if err != nil {
		t.Fatalf("ExecuteGraphReactive failed: %v", err)
	}

	// Verify all nodes completed
	for _, nodeID := range []string{"A", "B", "C"} {
		state, ok := memory.DB.GetNodeState("task-rq-parallel", nodeID)
		if !ok || state.Status != "completed" {
			t.Errorf("expected node %s to be completed, got: %+v", nodeID, state)
		}
	}

	// Verify B and C started at approximately the same time (within 30ms)
	bStartVal, bOk := startTimes.Load("B")
	cStartVal, cOk := startTimes.Load("C")
	if bOk && cOk {
		bStart := bStartVal.(time.Time)
		cStart := cStartVal.(time.Time)
		diff := bStart.Sub(cStart)
		if diff < 0 {
			diff = -diff
		}
		if diff > 30*time.Millisecond {
			t.Errorf("B and C should have started concurrently, but start time diff was %v", diff)
		}
	}
}

// TestReadyQueueResumesFromCheckpoint verifies that the ready queue skips
// already-completed nodes and resumes from the first pending node.
func TestReadyQueueResumesFromCheckpoint(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_rq_resume.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_rq_resume.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	var executedNodes []string
	var mu sync.Mutex

	tools.Register(&MockTool{
		ToolName:   "rq_resume_tool",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			nodeID := ctx.Value(contextKeyNodeID)
			mu.Lock()
			if nodeID != nil {
				executedNodes = append(executedNodes, nodeID.(string))
			}
			mu.Unlock()
			return `{"status": "ok"}`, nil
		},
	})
	defer tools.Unregister("rq_resume_tool")

	graph := &compiler.ExecutionGraph{
		TaskID: "task-rq-resume",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "deterministic", Action: "rq_resume_tool", Instructions: "Run A"},
			{ID: "B", Type: "deterministic", Action: "rq_resume_tool", Instructions: "Run B"},
			{ID: "C", Type: "deterministic", Action: "rq_resume_tool", Instructions: "Run C"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "B", TargetID: "C"},
		},
		CreatedAt: time.Now().Unix(),
	}

	// Pre-set A as completed (simulate a crash resume)
	_ = memory.DB.SetNodeState("task-rq-resume", "A", "completed", `{"status":"ok"}`)
	_ = memory.DB.SetNodeRawOutput("task-rq-resume", "A", `{"status":"ok"}`)

	engine := &ExecutionEngine{}
	engine.InitRegistry()
	err := engine.ExecuteGraphReactive(context.Background(), graph)
	if err != nil {
		t.Fatalf("ExecuteGraphReactive failed: %v", err)
	}

	// A should NOT have been re-executed
	sort.Strings(executedNodes)
	if len(executedNodes) != 2 {
		t.Fatalf("expected 2 nodes executed (B, C), got %d: %v", len(executedNodes), executedNodes)
	}

	if executedNodes[0] != "B" || executedNodes[1] != "C" {
		t.Errorf("expected [B C], got %v", executedNodes)
	}
}

// TestReadyQueueSkipPropagation verifies that when a node is skipped,
// all downstream dependents are also skipped.
func TestReadyQueueSkipPropagation(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_rq_skip.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_rq_skip.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	tools.Register(&MockTool{
		ToolName:   "rq_skip_tool",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"status": "ok"}`, nil
		},
	})
	defer tools.Unregister("rq_skip_tool")

	hook := &mockHook{
		beforeNodeFn: func(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error) {
			if node.ID == "A" {
				return ActionSkip, nil
			}
			return ActionContinue, nil
		},
	}

	engine := &ExecutionEngine{}
	engine.InitRegistry()
	engine.RegisterHook(hook)

	// Graph: A → B → C (A skipped → B and C should propagate skip)
	graph := &compiler.ExecutionGraph{
		TaskID: "task-rq-skip",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "deterministic", Action: "rq_skip_tool", Instructions: "Run A"},
			{ID: "B", Type: "deterministic", Action: "rq_skip_tool", Instructions: "Run B"},
			{ID: "C", Type: "deterministic", Action: "rq_skip_tool", Instructions: "Run C"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "B", TargetID: "C"},
		},
		CreatedAt: time.Now().Unix(),
	}

	err := engine.ExecuteGraphReactive(context.Background(), graph)
	if err != nil {
		t.Fatalf("ExecuteGraphReactive failed: %v", err)
	}

	for _, nodeID := range []string{"A", "B", "C"} {
		state, ok := memory.DB.GetNodeState("task-rq-skip", nodeID)
		if !ok || state.Status != "skipped" {
			t.Errorf("expected node %s to be skipped, got: %+v", nodeID, state)
		}
	}
}
