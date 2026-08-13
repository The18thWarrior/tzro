package executor

import (
	"context"
	"os"
	"testing"
	"time"

	"tzro/internal/compiler"
	"tzro/internal/memory"
	"tzro/internal/tools"
)

func setupEscalationTestDB(t *testing.T) func() {
	t.Helper()
	oldDBPath := memory.DB.GetDBPathForTesting()
	dbName := "test_escalation_hook.db"
	memory.DB.SetDBPathForTesting(dbName)

	_ = os.Remove(dbName)
	err := memory.DB.Init()
	if err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}

	return func() {
		_ = memory.DB.Close()
		_ = os.Remove(dbName)
		memory.DB.SetDBPathForTesting(oldDBPath)
	}
}

// --- TDD Cycle 1: Tool at or below approved level executes normally ---

func TestEscalationHook_ToolBelowApprovedLevelContinues(t *testing.T) {
	tools.ClearAllProactivityLevelOverrides()
	defer tools.ClearAllProactivityLevelOverrides()

	hook := &EscalationHook{ApprovedLevel: 3} // L3 ceiling

	// save_memory is L1 by default (built-in)
	node := &compiler.GraphNode{
		ID:     "node_test",
		Action: "save_memory",
	}

	action, err := hook.BeforeNode(context.Background(), "task_test", node)
	if err != nil {
		t.Fatalf("BeforeNode returned error: %v", err)
	}
	if action != ActionContinue {
		t.Errorf("Expected ActionContinue for L1 tool with L3 ceiling, got %s", action)
	}
}

// --- TDD Cycle 2: Tool above approved level triggers pause ---

func TestEscalationHook_ToolAboveApprovedLevelPauses(t *testing.T) {
	cleanup := setupEscalationTestDB(t)
	defer cleanup()
	tools.ClearAllProactivityLevelOverrides()
	defer tools.ClearAllProactivityLevelOverrides()

	hook := &EscalationHook{ApprovedLevel: 1} // L1 ceiling

	// web_search is L4 by default
	node := &compiler.GraphNode{
		ID:     "node_web",
		Action: "web_search",
	}

	action, err := hook.BeforeNode(context.Background(), "task_escalation", node)
	if err != nil {
		t.Fatalf("BeforeNode returned error: %v", err)
	}
	if action != ActionPause {
		t.Errorf("Expected ActionPause for L4 tool with L1 ceiling, got %s", action)
	}
}

// --- TDD Cycle 3: After approval, paused task resumes ---

func TestEscalationHook_ResumedAfterApproval(t *testing.T) {
	cleanup := setupEscalationTestDB(t)
	defer cleanup()
	tools.ClearAllProactivityLevelOverrides()
	defer tools.ClearAllProactivityLevelOverrides()

	// Register a test tool
	tools.Register(&MockTool{
		ToolName:   "test_escalation_tool",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"status": "executed"}`, nil
		},
	})
	defer tools.Unregister("test_escalation_tool")

	// Set tool level to L3
	tools.SetProactivityLevelOverride("test_escalation_tool", tools.PLevelReversibleAction)

	// First: hook with L1 ceiling — should pause
	hook := &EscalationHook{ApprovedLevel: 1}

	engine := &ExecutionEngine{}
	engine.InitRegistry()
	engine.RegisterHook(hook)

	graph := &compiler.ExecutionGraph{
		TaskID: "task-escalation-resume",
		Nodes: []compiler.GraphNode{
			{ID: "nodeA", Type: "deterministic", Action: "test_escalation_tool", Instructions: "Do something"},
		},
		CreatedAt: time.Now().Unix(),
	}
	levels := [][]string{{"nodeA"}}

	err := engine.ExecuteGraph(context.Background(), graph, levels)
	if err != ErrTaskPaused {
		t.Fatalf("Expected ErrTaskPaused, got: %v", err)
	}

	stateA, ok := memory.DB.GetNodeState("task-escalation-resume", "nodeA")
	if !ok || stateA.Status != "pending" {
		t.Errorf("Expected nodeA to be pending (paused), got: %+v", stateA)
	}

	// Simulate approval by upgrading the hook's approved level
	hook.ApprovedLevel = 4 // Now allows L3 tools

	// Resume execution
	err = engine.ExecuteGraph(context.Background(), graph, levels)
	if err != nil {
		t.Fatalf("Expected successful resume, got: %v", err)
	}

	stateAResumed, ok := memory.DB.GetNodeState("task-escalation-resume", "nodeA")
	if !ok || stateAResumed.Status != "completed" {
		t.Errorf("Expected nodeA to be completed after resume, got: %+v", stateAResumed)
	}
}

// --- TDD Cycle 4: Synthesis nodes skip escalation check ---

func TestEscalationHook_SynthesisNodeSkipsCheck(t *testing.T) {
	hook := &EscalationHook{ApprovedLevel: 0} // L0 ceiling (most restrictive)

	node := &compiler.GraphNode{
		ID:     "node_synth",
		Type:   "synthesis",
		Action: "", // No action for synthesis nodes
	}

	action, err := hook.BeforeNode(context.Background(), "task_test", node)
	if err != nil {
		t.Fatalf("BeforeNode returned error: %v", err)
	}
	if action != ActionContinue {
		t.Errorf("Expected ActionContinue for synthesis node (no action), got %s", action)
	}
}
