package executor

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"tzro/internal/compiler"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/tools"
)

// TestNeuralTraversalSpawnsThenContinues verifies the full neural traversal flow:
// 1. Node A completes
// 2. Edge A→B is traversed, B has ActivationThreshold 0.7
// 3. Edge Thought generated with confidence 0.3 (below threshold)
// 4. ApplySpawn inserts spawned_1 between A and B
// 5. spawned_1 executes
// 6. Edge spawned_1→B traversed, new Edge Thought has confidence 0.8 (above threshold)
// 7. B executes normally
func TestNeuralTraversalSpawnsThenContinues(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_neural_traversal.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_neural_traversal.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	var executedNodes []string
	var mu sync.Mutex

	tools.Register(&MockTool{
		ToolName:   "neural_tool",
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
	defer tools.Unregister("neural_tool")

	graph := &compiler.ExecutionGraph{
		TaskID: "task-neural-1",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "deterministic", Action: "neural_tool", Instructions: "Run A"},
			{ID: "B", Type: "deterministic", Action: "neural_tool", Instructions: "Run B",
				ActivationThreshold: 0.7}, // Sufficiency gate
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
		},
		MutationBudget: &compiler.MutationBudget{MaxSpawns: 5, RemainingSpawns: 5},
		CreatedAt:      time.Now().Unix(),
	}

	// Mock inference: first call returns low confidence (spawn), second returns high (continue)
	callCount := 0
	mockInference := &StatefulMockEdgeThoughtInference{
		generateFn: func(ctx context.Context, taskID string, src, tgt *compiler.GraphNode, output string, step int) (*memory.EdgeThought, error) {
			callCount++
			confidence := 0.3 // First call: below threshold, triggers spawn
			if callCount > 1 {
				confidence = 0.8 // Subsequent calls: above threshold, continue
			}
			return &memory.EdgeThought{
				ID:             fmt.Sprintf("et_%d", callCount),
				TaskID:         taskID,
				SourceNode:     src.ID,
				TargetNode:     tgt.ID,
				Thought:        fmt.Sprintf("Thought %d", callCount),
				GoalConfidence: confidence,
				StepIndex:      step,
				CreatedAt:      time.Now().Unix(),
			}, nil
		},
	}

	engine := &ExecutionEngine{EdgeThoughtGen: mockInference}
	engine.InitRegistry()
	err := engine.ExecuteGraphReactive(context.Background(), graph)
	if err != nil {
		t.Fatalf("ExecuteGraphReactive failed: %v", err)
	}

	// Verify: A executed, then a spawned node, then B
	if len(executedNodes) < 3 {
		t.Fatalf("expected at least 3 node executions (A, spawned, B), got %d: %v", len(executedNodes), executedNodes)
	}
	if executedNodes[0] != "A" {
		t.Errorf("expected first execution to be A, got %s", executedNodes[0])
	}
	// Last should be B
	if executedNodes[len(executedNodes)-1] != "B" {
		t.Errorf("expected last execution to be B, got %s", executedNodes[len(executedNodes)-1])
	}

	// Verify B completed successfully
	state, ok := memory.DB.GetNodeState("task-neural-1", "B")
	if !ok || state.Status != "completed" {
		t.Errorf("expected B to be completed, got: %+v", state)
	}

	// Verify mutation budget was decremented
	if graph.MutationBudget.RemainingSpawns != 4 {
		t.Errorf("expected 4 remaining spawns (1 used), got %d", graph.MutationBudget.RemainingSpawns)
	}

	// Verify edge thought was persisted
	thoughts, err := memory.DB.GetEdgeThoughtsForNode("task-neural-1", "B")
	if err != nil {
		t.Fatalf("GetEdgeThoughtsForNode failed: %v", err)
	}
	if len(thoughts) < 1 {
		t.Error("expected at least 1 edge thought persisted for node B")
	}
}

// TestNeuralTraversalHaltSkipsDownstream verifies that GoalAchieved=true on
// an edge thought causes all downstream nodes to be skipped.
func TestNeuralTraversalHaltSkipsDownstream(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_neural_halt.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_neural_halt.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	var executedNodes []string
	var mu sync.Mutex

	tools.Register(&MockTool{
		ToolName:   "halt_tool",
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
	defer tools.Unregister("halt_tool")

	graph := &compiler.ExecutionGraph{
		TaskID: "task-neural-halt",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "deterministic", Action: "halt_tool", Instructions: "Run A"},
			{ID: "B", Type: "deterministic", Action: "halt_tool", Instructions: "Run B",
				ActivationThreshold: 0.5},
			{ID: "C", Type: "deterministic", Action: "halt_tool", Instructions: "Run C"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "B", TargetID: "C"},
		},
		CreatedAt: time.Now().Unix(),
	}

	// Edge thought returns GoalAchieved=true → halt
	mockInference := &StatefulMockEdgeThoughtInference{
		generateFn: func(ctx context.Context, taskID string, src, tgt *compiler.GraphNode, output string, step int) (*memory.EdgeThought, error) {
			return &memory.EdgeThought{
				ID:             "et_halt",
				TaskID:         taskID,
				SourceNode:     src.ID,
				TargetNode:     tgt.ID,
				Thought:        "Goal already achieved by A's output",
				GoalConfidence: 0.95,
				GoalAchieved:   true, // HALT
				StepIndex:      step,
				CreatedAt:      time.Now().Unix(),
			}, nil
		},
	}

	engine := &ExecutionEngine{EdgeThoughtGen: mockInference}
	engine.InitRegistry()
	err := engine.ExecuteGraphReactive(context.Background(), graph)
	if err != nil {
		t.Fatalf("ExecuteGraphReactive failed: %v", err)
	}

	// Only A should have executed
	if len(executedNodes) != 1 || executedNodes[0] != "A" {
		t.Errorf("expected only [A] to execute, got %v", executedNodes)
	}

	// B and C should be skipped
	for _, nodeID := range []string{"B", "C"} {
		state, ok := memory.DB.GetNodeState("task-neural-halt", nodeID)
		if !ok || state.Status != "skipped" {
			t.Errorf("expected node %s to be skipped, got: %+v", nodeID, state)
		}
	}
}

// TestNeuralTraversalNoThresholdSkipsEdgeThought verifies that nodes without
// an activation threshold (0.0) execute normally without edge thought generation.
func TestNeuralTraversalNoThresholdSkipsEdgeThought(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_neural_nothreshold.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_neural_nothreshold.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	tools.Register(&MockTool{
		ToolName:   "nothreshold_tool",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"status": "ok"}`, nil
		},
	})
	defer tools.Unregister("nothreshold_tool")

	graph := &compiler.ExecutionGraph{
		TaskID: "task-neural-nothreshold",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "deterministic", Action: "nothreshold_tool", Instructions: "Run A"},
			{ID: "B", Type: "deterministic", Action: "nothreshold_tool", Instructions: "Run B"}, // No threshold
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
		},
		CreatedAt: time.Now().Unix(),
	}

	// Mock inference should never be called
	mockInference := &StatefulMockEdgeThoughtInference{
		generateFn: func(ctx context.Context, taskID string, src, tgt *compiler.GraphNode, output string, step int) (*memory.EdgeThought, error) {
			t.Error("edge thought inference should not be called when threshold is 0.0")
			return nil, fmt.Errorf("should not be called")
		},
	}

	engine := &ExecutionEngine{EdgeThoughtGen: mockInference}
	engine.InitRegistry()
	err := engine.ExecuteGraphReactive(context.Background(), graph)
	if err != nil {
		t.Fatalf("ExecuteGraphReactive failed: %v", err)
	}

	// Both should complete normally
	for _, nodeID := range []string{"A", "B"} {
		state, ok := memory.DB.GetNodeState("task-neural-nothreshold", nodeID)
		if !ok || state.Status != "completed" {
			t.Errorf("expected node %s to be completed, got: %+v", nodeID, state)
		}
	}
}

// TestNeuralTraversalHookFiringOnEdge verifies that OnEdgeTraversal hooks
// fire during neural edge traversal.
func TestNeuralTraversalHookFiringOnEdge(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_neural_hook.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_neural_hook.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	tools.Register(&MockTool{
		ToolName:   "hook_tool",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"status": "ok"}`, nil
		},
	})
	defer tools.Unregister("hook_tool")

	var traversedEdges []string
	var hookMu sync.Mutex

	hook := &mockHook{
		onEdgeTraversalFn: func(ctx context.Context, taskID string, src, tgt *compiler.GraphNode, et *memory.EdgeThought) (HookAction, error) {
			hookMu.Lock()
			traversedEdges = append(traversedEdges, src.ID+"→"+tgt.ID)
			hookMu.Unlock()
			return ActionContinue, nil
		},
	}

	graph := &compiler.ExecutionGraph{
		TaskID: "task-neural-hook",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "deterministic", Action: "hook_tool", Instructions: "Run A"},
			{ID: "B", Type: "deterministic", Action: "hook_tool", Instructions: "Run B",
				ActivationThreshold: 0.5},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
		},
		CreatedAt: time.Now().Unix(),
	}

	mockInference := &StatefulMockEdgeThoughtInference{
		generateFn: func(ctx context.Context, taskID string, src, tgt *compiler.GraphNode, output string, step int) (*memory.EdgeThought, error) {
			return &memory.EdgeThought{
				ID:             "et_hook",
				TaskID:         taskID,
				SourceNode:     src.ID,
				TargetNode:     tgt.ID,
				Thought:        "Sufficient",
				GoalConfidence: 0.9, // Above threshold, continue
				StepIndex:      step,
				CreatedAt:      time.Now().Unix(),
			}, nil
		},
	}

	engine := &ExecutionEngine{EdgeThoughtGen: mockInference}
	engine.InitRegistry()
	engine.RegisterHook(hook)

	err := engine.ExecuteGraphReactive(context.Background(), graph)
	if err != nil {
		t.Fatalf("ExecuteGraphReactive failed: %v", err)
	}

	// Verify hook was called for edge A→B
	hookMu.Lock()
	defer hookMu.Unlock()
	if len(traversedEdges) < 1 {
		t.Error("expected OnEdgeTraversal hook to fire at least once")
	}
	found := false
	for _, e := range traversedEdges {
		if e == "A→B" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected hook to fire for A→B, got: %v", traversedEdges)
	}
}

// StatefulMockEdgeThoughtInference allows custom behavior per call.
type StatefulMockEdgeThoughtInference struct {
	generateFn func(ctx context.Context, taskID string, src, tgt *compiler.GraphNode, output string, step int) (*memory.EdgeThought, error)
}

func (m *StatefulMockEdgeThoughtInference) GenerateEdgeThought(
	ctx context.Context, taskID string, src, tgt *compiler.GraphNode, output string, step int,
) (*memory.EdgeThought, error) {
	return m.generateFn(ctx, taskID, src, tgt, output, step)
}

// TestNeuralTraversalNilBudgetSpawnsSuccessfully verifies that if a graph starts with a nil
// MutationBudget, the execution engine dynamically sets a default budget and executes without panic.
func TestNeuralTraversalNilBudgetSpawnsSuccessfully(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_neural_traversal_nil_budget.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_neural_traversal_nil_budget.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	var executedNodes []string
	var mu sync.Mutex

	tools.Register(&MockTool{
		ToolName:   "neural_nil_budget_tool",
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
	defer tools.Unregister("neural_nil_budget_tool")

	graph := &compiler.ExecutionGraph{
		TaskID: "task-neural-nil-budget",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "deterministic", Action: "neural_nil_budget_tool", Instructions: "Run A"},
			{ID: "B", Type: "deterministic", Action: "neural_nil_budget_tool", Instructions: "Run B",
				ActivationThreshold: 0.7},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
		},
		MutationBudget: nil, // Test nil budget initialization
		CreatedAt:      time.Now().Unix(),
	}

	callCount := 0
	mockInference := &StatefulMockEdgeThoughtInference{
		generateFn: func(ctx context.Context, taskID string, src, tgt *compiler.GraphNode, output string, step int) (*memory.EdgeThought, error) {
			callCount++
			confidence := 0.3 // below threshold, triggers spawn
			if callCount > 1 {
				confidence = 0.8 // above threshold, continue
			}
			return &memory.EdgeThought{
				ID:             fmt.Sprintf("et_nil_budget_%d", callCount),
				TaskID:         taskID,
				SourceNode:     src.ID,
				TargetNode:     tgt.ID,
				Thought:        fmt.Sprintf("Thought %d", callCount),
				GoalConfidence: confidence,
				StepIndex:      step,
				CreatedAt:      time.Now().Unix(),
			}, nil
		},
	}

	engine := &ExecutionEngine{EdgeThoughtGen: mockInference}
	engine.InitRegistry()
	err := engine.ExecuteGraphReactive(context.Background(), graph)
	if err != nil {
		t.Fatalf("ExecuteGraphReactive failed: %v", err)
	}

	// Verify A executed, then spawned node, then B
	if len(executedNodes) < 3 {
		t.Fatalf("expected at least 3 node executions, got %d: %v", len(executedNodes), executedNodes)
	}

	// Verify budget was initialized to default and decremented
	if graph.MutationBudget == nil {
		t.Error("expected MutationBudget to be initialized, but was nil")
	} else if graph.MutationBudget.RemainingSpawns != 14 {
		t.Errorf("expected 14 remaining spawns (15 default - 1 used), got %d", graph.MutationBudget.RemainingSpawns)
	}
}

// TestActionNodeClassificationFallback verifies that if an action node is executed
// with an unregistered or empty action name, the execution engine calls classifyToolName
// and resolves it to a valid registered tool.
func TestActionNodeClassificationFallback(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_action_classification.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_action_classification.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	var executedNodes []string
	var mu sync.Mutex

	// Register a mock tool that we want our empty action to be classified as
	tools.Register(&MockTool{
		ToolName:   "real_target_tool",
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
	defer tools.Unregister("real_target_tool")

	graph := &compiler.ExecutionGraph{
		TaskID: "task-action-classification",
		Nodes: []compiler.GraphNode{
			// Setting Action to empty to simulate a rewritten probe node
			{ID: "explore", Type: "action", Action: "", Instructions: "Explore the directory using real_target_tool"},
		},
		CreatedAt: time.Now().Unix(),
	}

	// Mock the InferenceBackend to return "real_target_tool" when classifyToolName is called.
	// classifyToolName expects a JSON response conforming to the GBNF schema:
	// {"tool": "real_target_tool"}
	mockBackend := &MockInferenceBackend{
		CallModelFn: func(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string) (*inference.InferenceResult, error) {
			return &inference.InferenceResult{
				Content: `{"tool": "real_target_tool"}`,
			}, nil
		},
	}
	oldBackend := inference.ActiveBackend
	inference.ActiveBackend = mockBackend
	defer func() {
		inference.ActiveBackend = oldBackend
	}()

	engine := &ExecutionEngine{}
	engine.InitRegistry()
	err := engine.ExecuteGraphReactive(context.Background(), graph)
	if err != nil {
		t.Fatalf("ExecuteGraphReactive failed: %v", err)
	}

	// Verify that the explore node executed using real_target_tool (evidenced by execution record)
	mu.Lock()
	defer mu.Unlock()
	if len(executedNodes) != 1 || executedNodes[0] != "explore" {
		t.Errorf("expected node 'explore' to be executed, got: %v", executedNodes)
	}

	// Verify the node action was rewritten to 'real_target_tool' in the graph node
	if graph.Nodes[0].Action != "real_target_tool" {
		t.Errorf("expected graph node Action to be updated to 'real_target_tool', got '%s'", graph.Nodes[0].Action)
	}
}

type MockInferenceBackend struct {
	CallModelFn func(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string) (*inference.InferenceResult, error)
}

func (m *MockInferenceBackend) CallModel(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string) (*inference.InferenceResult, error) {
	return m.CallModelFn(ctx, messages, jsonSchema)
}

func (m *MockInferenceBackend) CallModelStream(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, meta inference.StreamMeta) (*inference.InferenceResult, error) {
	return nil, nil
}

func (m *MockInferenceBackend) Status() string {
	return "active"
}

func (m *MockInferenceBackend) Start(ctx context.Context) error {
	return nil
}

func (m *MockInferenceBackend) Stop() error {
	return nil
}

func TestNeuralTraversalFailureDampening(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_neural_dampen.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_neural_dampen.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	var executedNodes []string
	var mu sync.Mutex

	tools.Register(&MockTool{
		ToolName:   "dampen_tool",
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
	defer tools.Unregister("dampen_tool")

	graph := &compiler.ExecutionGraph{
		TaskID: "task-neural-dampen",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "deterministic", Action: "dampen_tool", Instructions: "Run A"},
			{ID: "B", Type: "deterministic", Action: "dampen_tool", Instructions: "Run B",
				ActivationThreshold: 0.7}, // Sufficiency gate
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
		},
		MutationBudget: &compiler.MutationBudget{MaxSpawns: 5, RemainingSpawns: 5},
		CreatedAt:      time.Now().Unix(),
	}

	// Persistent low confidence (0.0), goalAchieved = false
	callCount := 0
	mockInference := &StatefulMockEdgeThoughtInference{
		generateFn: func(ctx context.Context, taskID string, src, tgt *compiler.GraphNode, output string, step int) (*memory.EdgeThought, error) {
			callCount++
			return &memory.EdgeThought{
				ID:             fmt.Sprintf("et_dampen_%d", callCount),
				TaskID:         taskID,
				SourceNode:     src.ID,
				TargetNode:     tgt.ID,
				Thought:        fmt.Sprintf("Persistent failure thought %d", callCount),
				GoalConfidence: 0.0, // Persistently low confidence
				StepIndex:      step,
				CreatedAt:      time.Now().Unix(),
			}, nil
		},
	}

	engine := &ExecutionEngine{EdgeThoughtGen: mockInference}
	engine.InitRegistry()
	err := engine.ExecuteGraphReactive(context.Background(), graph)
	if err != nil {
		t.Fatalf("ExecuteGraphReactive failed: %v", err)
	}

	// Verify that ConsecutiveFailures reached 3 and remained at 3
	if graph.MutationBudget.ConsecutiveFailures != 3 {
		t.Errorf("expected ConsecutiveFailures to be 3 (dampened), got %d", graph.MutationBudget.ConsecutiveFailures)
	}

	// Verify that exactly 3 spawned nodes were successfully created
	// RemainingSpawns should be 5 - 3 = 2
	if graph.MutationBudget.RemainingSpawns != 2 {
		t.Errorf("expected 2 remaining spawns, got %d", graph.MutationBudget.RemainingSpawns)
	}

	// We expect executedNodes to contain A, 3 spawned nodes, and B.
	// Executed list: ["A", "spawned_A_1", "spawned_spawned_A_1_2", "spawned_spawned_spawned_A_1_2_3", "B"]
	mu.Lock()
	nodesCount := len(executedNodes)
	mu.Unlock()
	if nodesCount != 5 {
		t.Errorf("expected exactly 5 node executions (A, 3 spawns, B), got %d: %v", nodesCount, executedNodes)
	}
}
