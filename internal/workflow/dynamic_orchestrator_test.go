package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"tzro/internal/memory"
)

// mockLLMClient implements LLMClient for testing.
type mockLLMClient struct {
	mu        sync.Mutex
	responses []string
	callCount int
}

func (m *mockLLMClient) CallModel(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	if m.callCount >= len(m.responses) {
		// Safety: return goal_achieved to prevent infinite loops
		return `{"action": "goal_achieved", "summary": "Fallback: no more responses"}`, nil
	}
	resp := m.responses[m.callCount]
	m.callCount++
	return resp, nil
}

// mockTaskRunner implements TaskRunner for testing.
type mockTaskRunner struct {
	mu        sync.Mutex
	runCount  int
	failAt    int // -1 means never fail
	runCalls  []string
}

func (m *mockTaskRunner) Run(ctx context.Context, instruction string, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	m.runCount++
	m.runCalls = append(m.runCalls, instruction)

	if m.failAt >= 0 && m.runCount == m.failAt {
		return fmt.Errorf("simulated task failure at task %d", m.runCount)
	}
	return nil
}

func setupDynamicOrchestratorTestDB(t *testing.T) func() {
	t.Helper()
	oldDBPath := memory.DB.GetDBPathForTesting()
	dbName := "test_dynamic_orchestrator.db"

	// Close existing connection before switching
	_ = memory.DB.Close()
	_ = os.Remove(dbName)

	memory.DB.SetDBPathForTesting(dbName)
	err := memory.DB.Init()
	if err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}

	return func() {
		_ = memory.DB.Close()
		_ = os.Remove(dbName)
		// Restore and reinitialize the original TestMain DB
		memory.DB.SetDBPathForTesting(oldDBPath)
		_ = memory.DB.Init()
	}
}

func createDynamicTestWorkflow(t *testing.T, maxToolCalls, maxTokens int) string {
	t.Helper()
	now := time.Now().Unix()
	wf := memory.WorkflowDefinition{
		ID:                "wf_dyn_test",
		Name:              "Test Dynamic Workflow",
		TriggerType:       "background",
		Status:            "active",
		CreatedAt:         now,
		UpdatedAt:         now,
		OrchestrationMode: "dynamic",
		Goal:              "Complete a test objective",
		MaxToolCalls:      maxToolCalls,
		MaxTokens:         maxTokens,
	}
	err := memory.DB.SaveWorkflow(wf, nil)
	if err != nil {
		t.Fatalf("Failed to save test workflow: %v", err)
	}
	return wf.ID
}

// --- TDD Cycle 1: Sequential tasks until goal achieved ---

func TestDynamicOrchestrator_SequentialTasksUntilGoalAchieved(t *testing.T) {
	cleanup := setupDynamicOrchestratorTestDB(t)
	defer cleanup()

	wfID := createDynamicTestWorkflow(t, 0, 0) // no budget limits

	decision1, _ := json.Marshal(LLMDecision{Action: "next_task", Instruction: "Probe error logs"})
	decision2, _ := json.Marshal(LLMDecision{Action: "next_task", Instruction: "Check DB connections"})
	decision3, _ := json.Marshal(LLMDecision{Action: "goal_achieved", Summary: "Root cause identified: DB connection pool exhausted"})

	llm := &mockLLMClient{
		responses: []string{string(decision1), string(decision2), string(decision3)},
	}
	runner := &mockTaskRunner{failAt: -1}

	err := ExecuteDynamicWorkflow(context.Background(), wfID, llm, runner)
	if err != nil {
		t.Fatalf("ExecuteDynamicWorkflow failed: %v", err)
	}

	// Verify 2 child tasks were run
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.runCount != 2 {
		t.Errorf("Expected 2 child tasks, got %d", runner.runCount)
	}
	if runner.runCalls[0] != "Probe error logs" {
		t.Errorf("Expected first task 'Probe error logs', got '%s'", runner.runCalls[0])
	}
	if runner.runCalls[1] != "Check DB connections" {
		t.Errorf("Expected second task 'Check DB connections', got '%s'", runner.runCalls[1])
	}

	// Verify workflow execution completed
	execs, err := memory.DB.GetWorkflowExecutions(wfID)
	if err != nil {
		t.Fatalf("GetWorkflowExecutions failed: %v", err)
	}
	if len(execs) != 1 {
		t.Fatalf("Expected 1 execution, got %d", len(execs))
	}
	if execs[0].Status != "completed" {
		t.Errorf("Expected execution status 'completed', got '%s'", execs[0].Status)
	}
}

// --- TDD Cycle 2: Budget exhaustion terminates workflow ---

func TestDynamicOrchestrator_BudgetExhaustionTerminates(t *testing.T) {
	cleanup := setupDynamicOrchestratorTestDB(t)
	defer cleanup()

	wfID := createDynamicTestWorkflow(t, 2, 0) // limit: 2 tool calls

	// LLM always says next_task (would loop forever without budget)
	decisions := make([]string, 10)
	for i := range decisions {
		d, _ := json.Marshal(LLMDecision{Action: "next_task", Instruction: fmt.Sprintf("Task %d", i+1)})
		decisions[i] = string(d)
	}

	llm := &mockLLMClient{responses: decisions}
	runner := &mockTaskRunner{failAt: -1}

	err := ExecuteDynamicWorkflow(context.Background(), wfID, llm, runner)
	if err != nil {
		t.Fatalf("ExecuteDynamicWorkflow failed: %v", err)
	}

	// Verify exactly 2 tasks were run (budget limit)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.runCount != 2 {
		t.Errorf("Expected 2 child tasks (budget limit), got %d", runner.runCount)
	}

	// Verify workflow execution completed (not failed)
	execs, err := memory.DB.GetWorkflowExecutions(wfID)
	if err != nil {
		t.Fatalf("GetWorkflowExecutions failed: %v", err)
	}
	if execs[0].Status != "completed" {
		t.Errorf("Expected status 'completed' on budget exhaustion, got '%s'", execs[0].Status)
	}
	if execs[0].ToolCallsConsumed != 2 {
		t.Errorf("Expected 2 tool calls consumed, got %d", execs[0].ToolCallsConsumed)
	}
}

// --- TDD Cycle 3: Child task failure consults LLM ---

func TestDynamicOrchestrator_ChildTaskFailureConsultsLLM(t *testing.T) {
	cleanup := setupDynamicOrchestratorTestDB(t)
	defer cleanup()

	wfID := createDynamicTestWorkflow(t, 0, 0)

	decision1, _ := json.Marshal(LLMDecision{Action: "next_task", Instruction: "Risky operation"})
	// After the failure, LLM decides to stop
	decision2, _ := json.Marshal(LLMDecision{Action: "goal_achieved", Summary: "Stopped after task failure"})

	llm := &mockLLMClient{
		responses: []string{string(decision1), string(decision2)},
	}
	runner := &mockTaskRunner{failAt: 1} // First task fails

	err := ExecuteDynamicWorkflow(context.Background(), wfID, llm, runner)
	if err != nil {
		t.Fatalf("ExecuteDynamicWorkflow failed: %v", err)
	}

	// Verify LLM was called twice: once for initial decision, once after failure
	llm.mu.Lock()
	llmCalls := llm.callCount
	llm.mu.Unlock()

	if llmCalls != 2 {
		t.Errorf("Expected 2 LLM calls (initial + after failure), got %d", llmCalls)
	}

	// Verify workflow completed (not failed — the LLM chose to stop gracefully)
	execs, err := memory.DB.GetWorkflowExecutions(wfID)
	if err != nil {
		t.Fatalf("GetWorkflowExecutions failed: %v", err)
	}
	if execs[0].Status != "completed" {
		t.Errorf("Expected status 'completed', got '%s'", execs[0].Status)
	}
}

// --- TDD Cycle 4: Context cancellation stops orchestrator loop ---

func TestDynamicOrchestrator_ContextCancellationStopsLoop(t *testing.T) {
	cleanup := setupDynamicOrchestratorTestDB(t)
	defer cleanup()

	wfID := createDynamicTestWorkflow(t, 0, 0)

	decision1, _ := json.Marshal(LLMDecision{Action: "next_task", Instruction: "First task"})
	decision2, _ := json.Marshal(LLMDecision{Action: "next_task", Instruction: "Second task (blocked)"})
	decision3, _ := json.Marshal(LLMDecision{Action: "goal_achieved", Summary: "Done"})

	llm := &mockLLMClient{
		responses: []string{string(decision1), string(decision2), string(decision3)},
	}

	ctx, cancel := context.WithCancel(context.Background())

	// A runner that blocks on the second task until context is cancelled
	blockingRunner := &blockingTaskRunner{cancelOnTask: 2, cancel: cancel}

	err := ExecuteDynamicWorkflow(ctx, wfID, llm, blockingRunner)
	if err != context.Canceled {
		t.Fatalf("Expected context.Canceled error, got: %v", err)
	}

	// Verify workflow execution stays in "running" (preempted, not failed)
	execs, err := memory.DB.GetWorkflowExecutions(wfID)
	if err != nil {
		t.Fatalf("GetWorkflowExecutions failed: %v", err)
	}
	if execs[0].Status != "running" {
		t.Errorf("Expected status 'running' (preempted), got '%s'", execs[0].Status)
	}
}

// blockingTaskRunner cancels the context when a specific task runs, simulating preemption.
type blockingTaskRunner struct {
	mu           sync.Mutex
	taskCount    int
	cancelOnTask int
	cancel       context.CancelFunc
}

func (b *blockingTaskRunner) Run(ctx context.Context, instruction string, taskID string) error {
	b.mu.Lock()
	b.taskCount++
	current := b.taskCount
	b.mu.Unlock()

	if current == b.cancelOnTask {
		b.cancel()
		return ctx.Err()
	}
	return nil
}
