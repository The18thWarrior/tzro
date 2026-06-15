package executor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"tzro/internal/compiler"
	"tzro/internal/memory"
	"tzro/internal/tools"
)

type mockHook struct {
	beforeLevelFn     func(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error)
	afterLevelFn      func(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error)
	beforeNodeFn      func(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error)
	afterNodeFn       func(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (HookAction, error)
	onEdgeTraversalFn func(ctx context.Context, taskID string, sourceNode, targetNode *compiler.GraphNode, edgeThought *memory.EdgeThought) (HookAction, error)
}

func (m *mockHook) BeforeLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error) {
	if m.beforeLevelFn != nil {
		return m.beforeLevelFn(ctx, taskID, levelNodes)
	}
	return ActionContinue, nil
}

func (m *mockHook) AfterLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error) {
	if m.afterLevelFn != nil {
		return m.afterLevelFn(ctx, taskID, levelNodes)
	}
	return ActionContinue, nil
}

func (m *mockHook) BeforeNode(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error) {
	if m.beforeNodeFn != nil {
		return m.beforeNodeFn(ctx, taskID, node)
	}
	return ActionContinue, nil
}

func (m *mockHook) AfterNode(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (HookAction, error) {
	if m.afterNodeFn != nil {
		return m.afterNodeFn(ctx, taskID, node, rawOutput)
	}
	return ActionContinue, nil
}

func (m *mockHook) OnEdgeTraversal(ctx context.Context, taskID string, sourceNode, targetNode *compiler.GraphNode, edgeThought *memory.EdgeThought) (HookAction, error) {
	if m.onEdgeTraversalFn != nil {
		return m.onEdgeTraversalFn(ctx, taskID, sourceNode, targetNode, edgeThought)
	}
	return ActionContinue, nil
}

func TestHooksSequencing(t *testing.T) {
	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_hooks_seq.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_hooks_seq.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	tools.Register(&MockTool{
		ToolName:   "test_tool_seq",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"status": "ok"}`, nil
		},
	})
	defer tools.Unregister("test_tool_seq")

	var sequence []string
	var mu sync.Mutex
	record := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		sequence = append(sequence, s)
	}

	hook := &mockHook{
		beforeLevelFn: func(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error) {
			record("before_level:" + levelNodes[0].ID)
			return ActionContinue, nil
		},
		afterLevelFn: func(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error) {
			record("after_level:" + levelNodes[0].ID)
			return ActionContinue, nil
		},
		beforeNodeFn: func(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error) {
			record("before_node:" + node.ID)
			return ActionContinue, nil
		},
		afterNodeFn: func(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (HookAction, error) {
			record("after_node:" + node.ID)
			return ActionContinue, nil
		},
	}

	engine := &ExecutionEngine{}
	engine.RegisterHook(hook)

	graph := &compiler.ExecutionGraph{
		TaskID: "task-seq-test",
		Nodes: []compiler.GraphNode{
			{ID: "node1", Type: "deterministic", Action: "test_tool_seq", Instructions: "Run Node 1"},
		},
		CreatedAt: time.Now().Unix(),
	}

	levels := [][]string{{"node1"}}
	ctx := context.Background()

	err := engine.ExecuteGraph(ctx, graph, levels)
	if err != nil {
		t.Fatalf("ExecuteGraph failed: %v", err)
	}

	expected := []string{"before_level:node1", "before_node:node1", "after_node:node1", "after_level:node1"}
	if len(sequence) != len(expected) {
		t.Fatalf("expected sequence length %d, got %d (sequence: %v)", len(expected), len(sequence), sequence)
	}

	for i, v := range expected {
		if sequence[i] != v {
			t.Errorf("at index %d: expected %s, got %s", i, v, sequence[i])
		}
	}
}

func TestHooksActionSkipAndPropagation(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_hooks_skip.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_hooks_skip.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	tools.Register(&MockTool{
		ToolName:   "mock_skip_action",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"status": "should-not-run"}`, nil
		},
	})
	defer tools.Unregister("mock_skip_action")

	hook := &mockHook{
		beforeNodeFn: func(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error) {
			if node.ID == "nodeA" {
				return ActionSkip, nil
			}
			return ActionContinue, nil
		},
	}

	engine := &ExecutionEngine{}
	engine.RegisterHook(hook)

	// A: skipped by hook, C: downstream of A -> should be skipped via propagation
	graph := &compiler.ExecutionGraph{
		TaskID: "task-skip-test",
		Nodes: []compiler.GraphNode{
			{ID: "nodeA", Type: "deterministic", Action: "mock_skip_action", Instructions: "Run A"},
			{ID: "nodeC", Type: "deterministic", Action: "mock_skip_action", Instructions: "Run C"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "nodeA", TargetID: "nodeC"},
		},
		CreatedAt: time.Now().Unix(),
	}

	levels := [][]string{{"nodeA"}, {"nodeC"}}
	ctx := context.Background()

	err := engine.ExecuteGraph(ctx, graph, levels)
	if err != nil {
		t.Fatalf("ExecuteGraph failed: %v", err)
	}

	stateA, ok := memory.DB.GetNodeState("task-skip-test", "nodeA")
	if !ok || stateA.Status != "skipped" {
		t.Errorf("expected nodeA to be skipped, got: %+v", stateA)
	}

	stateC, ok := memory.DB.GetNodeState("task-skip-test", "nodeC")
	if !ok || stateC.Status != "skipped" {
		t.Errorf("expected nodeC to be skipped via propagation, got: %+v", stateC)
	}
}

func TestHooksActionAbort(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_hooks_abort.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_hooks_abort.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	tools.Register(&MockTool{
		ToolName:   "mock_abort_action",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"status": "ok"}`, nil
		},
	})
	defer tools.Unregister("mock_abort_action")

	hook := &mockHook{
		beforeNodeFn: func(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error) {
			return ActionAbort, nil
		},
	}

	engine := &ExecutionEngine{}
	engine.RegisterHook(hook)

	graph := &compiler.ExecutionGraph{
		TaskID: "task-abort-test",
		Nodes: []compiler.GraphNode{
			{ID: "nodeA", Type: "deterministic", Action: "mock_abort_action", Instructions: "Run A"},
		},
		CreatedAt: time.Now().Unix(),
	}

	levels := [][]string{{"nodeA"}}
	ctx := context.Background()

	err := engine.ExecuteGraph(ctx, graph, levels)
	if err == nil {
		t.Fatal("expected ExecuteGraph to return error on abort, got nil")
	}

	if !strings.Contains(err.Error(), "BeforeNode hook aborted execution") {
		t.Errorf("expected beforeNode abort error, got: %v", err)
	}
}

func TestHooksActionPauseAndResume(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_hooks_pause.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_hooks_pause.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	tools.Register(&MockTool{
		ToolName:   "mock_pause_action",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"status": "executed"}`, nil
		},
	})
	defer tools.Unregister("mock_pause_action")

	shouldPause := true
	hook := &mockHook{
		beforeNodeFn: func(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error) {
			if node.ID == "nodeB" && shouldPause {
				return ActionPause, nil
			}
			return ActionContinue, nil
		},
	}

	engine := &ExecutionEngine{}
	engine.RegisterHook(hook)

	graph := &compiler.ExecutionGraph{
		TaskID: "task-pause-test",
		Nodes: []compiler.GraphNode{
			{ID: "nodeA", Type: "deterministic", Action: "mock_pause_action", Instructions: "Run A"},
			{ID: "nodeB", Type: "deterministic", Action: "mock_pause_action", Instructions: "Run B"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "nodeA", TargetID: "nodeB"},
		},
		CreatedAt: time.Now().Unix(),
	}

	levels := [][]string{{"nodeA"}, {"nodeB"}}
	ctx := context.Background()

	// First execution: should run A, then pause on B
	err := engine.ExecuteGraph(ctx, graph, levels)
	if err != ErrTaskPaused {
		t.Fatalf("expected ErrTaskPaused, got: %v", err)
	}

	// Verify A completed and B is pending/paused
	stateA, ok := memory.DB.GetNodeState("task-pause-test", "nodeA")
	if !ok || stateA.Status != "completed" {
		t.Errorf("expected nodeA to be completed, got: %+v", stateA)
	}

	stateB, ok := memory.DB.GetNodeState("task-pause-test", "nodeB")
	if !ok || stateB.Status != "pending" {
		t.Errorf("expected nodeB to be pending, got: %+v", stateB)
	}

	// Second execution: user resumes (we flip shouldPause to false)
	shouldPause = false
	err = engine.ExecuteGraph(ctx, graph, levels)
	if err != nil {
		t.Fatalf("expected successful resume, got error: %v", err)
	}

	stateBResumed, ok := memory.DB.GetNodeState("task-pause-test", "nodeB")
	if !ok || stateBResumed.Status != "completed" {
		t.Errorf("expected nodeB to be completed after resume, got: %+v", stateBResumed)
	}
}

func TestHooksOutputMutation(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_hooks_mutate.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_hooks_mutate.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	tools.Register(&MockTool{
		ToolName:   "mock_mutate_action",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"value": "original"}`, nil
		},
	})
	defer tools.Unregister("mock_mutate_action")

	hook := &mockHook{
		afterNodeFn: func(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (HookAction, error) {
			*rawOutput = `{"value": "mutated"}`
			return ActionContinue, nil
		},
	}

	engine := &ExecutionEngine{}
	engine.RegisterHook(hook)

	graph := &compiler.ExecutionGraph{
		TaskID: "task-mutate-test",
		Nodes: []compiler.GraphNode{
			{ID: "node1", Type: "deterministic", Action: "mock_mutate_action", Instructions: "Run 1"},
		},
		CreatedAt: time.Now().Unix(),
	}

	levels := [][]string{{"node1"}}
	ctx := context.Background()

	err := engine.ExecuteGraph(ctx, graph, levels)
	if err != nil {
		t.Fatalf("ExecuteGraph failed: %v", err)
	}

	state, ok := memory.DB.GetNodeState("task-mutate-test", "node1")
	if !ok {
		t.Fatal("expected node state to exist")
	}

	if !strings.Contains(state.RawOutput, "mutated") {
		t.Errorf("expected RawOutput to contain 'mutated', got: %s", state.RawOutput)
	}
}

func TestHooksConcurrencySafety(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_hooks_concur.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_hooks_concur.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	tools.Register(&MockTool{
		ToolName:   "mock_concur_action",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"status": "ok"}`, nil
		},
	})
	defer tools.Unregister("mock_concur_action")

	hook := &mockHook{
		beforeNodeFn: func(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error) {
			time.Sleep(5 * time.Millisecond)
			return ActionContinue, nil
		},
		afterNodeFn: func(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (HookAction, error) {
			time.Sleep(5 * time.Millisecond)
			return ActionContinue, nil
		},
	}

	engine := &ExecutionEngine{}
	engine.RegisterHook(hook)

	var wg sync.WaitGroup
	tasksCount := 5

	for i := 0; i < tasksCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			taskID := fmt.Sprintf("task-concur-%d", idx)
			graph := &compiler.ExecutionGraph{
				TaskID: taskID,
				Nodes: []compiler.GraphNode{
					{ID: "node1", Type: "deterministic", Action: "mock_concur_action", Instructions: "Run 1"},
				},
				CreatedAt: time.Now().Unix(),
			}
			levels := [][]string{{"node1"}}
			_ = engine.ExecuteGraph(context.Background(), graph, levels)
		}(i)
	}

	wg.Wait()

	// Verify all tasks recorded success
	for i := 0; i < tasksCount; i++ {
		taskID := fmt.Sprintf("task-concur-%d", i)
		state, ok := memory.DB.GetNodeState(taskID, "node1")
		if !ok || state.Status != "completed" {
			t.Errorf("expected task %s node1 to complete, got: %+v", taskID, state)
		}
	}
}
