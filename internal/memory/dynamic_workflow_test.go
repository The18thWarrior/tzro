package memory

import (
	"os"
	"testing"
	"time"
)

// setupDynamicTestDB creates an isolated test DB and returns a cleanup function.
func setupDynamicTestDB(t *testing.T) func() {
	t.Helper()
	oldDBPath := DB.GetDBPathForTesting()
	dbName := "test_dynamic_workflow.db"
	DB.SetDBPathForTesting(dbName)

	_ = os.Remove(dbName)
	err := DB.Init()
	if err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}

	return func() {
		_ = DB.Close()
		_ = os.Remove(dbName)
		DB.SetDBPathForTesting(oldDBPath)
	}
}

// --- TDD Cycle 1: Dynamic Workflow CRUD round-trip ---

func TestDynamicWorkflow_CRUDRoundTrip(t *testing.T) {
	cleanup := setupDynamicTestDB(t)
	defer cleanup()

	now := time.Now().Unix()

	wf := WorkflowDefinition{
		ID:                "wf_dynamic_test_1",
		Name:              "Diagnostic Investigation",
		Description:       "Autonomous root cause analysis",
		TriggerType:       "background",
		Status:            "active",
		CreatedAt:         now,
		UpdatedAt:         now,
		OrchestrationMode: "dynamic",
		Goal:              "Investigate 3 consecutive tool failures and produce diagnosis",
		ApprovedLevel:     1, // L1
		MaxTokens:         5000,
		MaxToolCalls:      10,
		SpawnedBy:         "sentinel",
	}

	tasks := []WorkflowTask{
		{
			WorkflowID:     wf.ID,
			TaskTemplateID: "initial_probe",
			Name:           "Initial Probe",
			Instructions:   "Investigate the failure",
		},
	}

	err := DB.SaveWorkflow(wf, tasks)
	if err != nil {
		t.Fatalf("SaveWorkflow failed: %v", err)
	}

	// Retrieve and verify all fields round-trip
	workflows, err := DB.GetWorkflows()
	if err != nil {
		t.Fatalf("GetWorkflows failed: %v", err)
	}

	if len(workflows) != 1 {
		t.Fatalf("Expected 1 workflow, got %d", len(workflows))
	}

	got := workflows[0]
	if got.OrchestrationMode != "dynamic" {
		t.Errorf("OrchestrationMode: expected 'dynamic', got '%s'", got.OrchestrationMode)
	}
	if got.Goal != "Investigate 3 consecutive tool failures and produce diagnosis" {
		t.Errorf("Goal: expected investigation goal, got '%s'", got.Goal)
	}
	if got.ApprovedLevel != 1 {
		t.Errorf("ApprovedLevel: expected 1, got %d", got.ApprovedLevel)
	}
	if got.MaxTokens != 5000 {
		t.Errorf("MaxTokens: expected 5000, got %d", got.MaxTokens)
	}
	if got.MaxToolCalls != 10 {
		t.Errorf("MaxToolCalls: expected 10, got %d", got.MaxToolCalls)
	}
	if got.SpawnedBy != "sentinel" {
		t.Errorf("SpawnedBy: expected 'sentinel', got '%s'", got.SpawnedBy)
	}
	if got.TriggerType != "background" {
		t.Errorf("TriggerType: expected 'background', got '%s'", got.TriggerType)
	}
}

// --- TDD Cycle 2: Budget accumulator ---

func TestDynamicWorkflow_BudgetAccumulator(t *testing.T) {
	cleanup := setupDynamicTestDB(t)
	defer cleanup()

	now := time.Now().Unix()

	wf := WorkflowDefinition{
		ID:                "wf_budget_test",
		Name:              "Budget Test",
		TriggerType:       "background",
		Status:            "active",
		CreatedAt:         now,
		UpdatedAt:         now,
		OrchestrationMode: "dynamic",
		MaxTokens:         5000,
		MaxToolCalls:      10,
	}
	_ = DB.SaveWorkflow(wf, nil)

	exec := WorkflowExecution{
		ID:         "exec_budget_test",
		WorkflowID: wf.ID,
		Status:     "running",
		StartedAt:  now,
	}
	taskRuns := []WorkflowTaskExecution{
		{
			WorkflowExecutionID: exec.ID,
			TaskTemplateID:      "task_1",
			Status:              "pending",
			StartedAt:           now,
		},
	}
	err := DB.CreateWorkflowExecution(exec, taskRuns)
	if err != nil {
		t.Fatalf("CreateWorkflowExecution failed: %v", err)
	}

	// Increment budget
	err = DB.UpdateWorkflowExecutionBudget(exec.ID, 1000, 3)
	if err != nil {
		t.Fatalf("UpdateWorkflowExecutionBudget failed: %v", err)
	}

	// Increment again
	err = DB.UpdateWorkflowExecutionBudget(exec.ID, 500, 2)
	if err != nil {
		t.Fatalf("UpdateWorkflowExecutionBudget (2nd) failed: %v", err)
	}

	// Verify accumulated values
	gotExec, _, err := DB.GetWorkflowExecutionDetails(exec.ID)
	if err != nil {
		t.Fatalf("GetWorkflowExecutionDetails failed: %v", err)
	}
	if gotExec.TokensConsumed != 1500 {
		t.Errorf("TokensConsumed: expected 1500, got %d", gotExec.TokensConsumed)
	}
	if gotExec.ToolCallsConsumed != 5 {
		t.Errorf("ToolCallsConsumed: expected 5, got %d", gotExec.ToolCallsConsumed)
	}
}

// --- TDD Cycle 3: Interrupted status ---

func TestDynamicWorkflow_InterruptedStatus(t *testing.T) {
	cleanup := setupDynamicTestDB(t)
	defer cleanup()

	now := time.Now().Unix()

	wf := WorkflowDefinition{
		ID:                "wf_interrupt_test",
		Name:              "Interrupt Test",
		TriggerType:       "background",
		Status:            "active",
		CreatedAt:         now,
		UpdatedAt:         now,
		OrchestrationMode: "dynamic",
	}
	_ = DB.SaveWorkflow(wf, nil)

	exec := WorkflowExecution{
		ID:         "exec_interrupt_test",
		WorkflowID: wf.ID,
		Status:     "running",
		StartedAt:  now,
	}
	taskRuns := []WorkflowTaskExecution{
		{
			WorkflowExecutionID: exec.ID,
			TaskTemplateID:      "task_1",
			Status:              "completed",
			StartedAt:           now,
			CompletedAt:         now,
		},
		{
			WorkflowExecutionID: exec.ID,
			TaskTemplateID:      "task_2",
			Status:              "pending",
			StartedAt:           now,
		},
	}
	_ = DB.CreateWorkflowExecution(exec, taskRuns)

	// Mark task_2 as interrupted
	err := DB.UpdateWorkflowTaskExecution(exec.ID, "task_2", "task_2_exec", "interrupted", 0)
	if err != nil {
		t.Fatalf("UpdateWorkflowTaskExecution (interrupted) failed: %v", err)
	}

	// Verify interrupted status persisted
	_, gotTasks, err := DB.GetWorkflowExecutionDetails(exec.ID)
	if err != nil {
		t.Fatalf("GetWorkflowExecutionDetails failed: %v", err)
	}

	for _, tr := range gotTasks {
		if tr.TaskTemplateID == "task_2" {
			if tr.Status != "interrupted" {
				t.Errorf("task_2 status: expected 'interrupted', got '%s'", tr.Status)
			}
		}
	}

	// Verify GetInterruptedWorkflowExecutions finds it
	interrupted, err := DB.GetInterruptedWorkflowExecutions()
	if err != nil {
		t.Fatalf("GetInterruptedWorkflowExecutions failed: %v", err)
	}
	if len(interrupted) != 1 {
		t.Fatalf("Expected 1 interrupted execution, got %d", len(interrupted))
	}
	if interrupted[0].ID != exec.ID {
		t.Errorf("Expected exec ID '%s', got '%s'", exec.ID, interrupted[0].ID)
	}
}

// --- TDD Cycle 4: Backward compatibility ---

func TestDynamicWorkflow_BackwardCompatibility(t *testing.T) {
	cleanup := setupDynamicTestDB(t)
	defer cleanup()

	now := time.Now().Unix()

	// Create a static workflow without setting dynamic fields
	wf := WorkflowDefinition{
		ID:          "wf_static_compat",
		Name:        "Legacy Static Workflow",
		Description: "Should work without dynamic fields",
		TriggerType: "cron",
		Status:      "active",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	tasks := []WorkflowTask{
		{
			WorkflowID:     wf.ID,
			TaskTemplateID: "task_a",
			Name:           "Task A",
			Instructions:   "Do something",
		},
	}

	err := DB.SaveWorkflow(wf, tasks)
	if err != nil {
		t.Fatalf("SaveWorkflow failed for static workflow: %v", err)
	}

	workflows, err := DB.GetWorkflows()
	if err != nil {
		t.Fatalf("GetWorkflows failed: %v", err)
	}

	if len(workflows) != 1 {
		t.Fatalf("Expected 1 workflow, got %d", len(workflows))
	}

	got := workflows[0]
	// Static workflow should have sensible defaults
	if got.OrchestrationMode != "static" {
		t.Errorf("OrchestrationMode: expected 'static' default, got '%s'", got.OrchestrationMode)
	}
	if got.Goal != "" {
		t.Errorf("Goal: expected empty string, got '%s'", got.Goal)
	}
	if got.ApprovedLevel != 0 {
		t.Errorf("ApprovedLevel: expected 0, got %d", got.ApprovedLevel)
	}
	if got.MaxTokens != 0 {
		t.Errorf("MaxTokens: expected 0, got %d", got.MaxTokens)
	}
	if got.MaxToolCalls != 0 {
		t.Errorf("MaxToolCalls: expected 0, got %d", got.MaxToolCalls)
	}
	if got.SpawnedBy != "" {
		t.Errorf("SpawnedBy: expected empty string, got '%s'", got.SpawnedBy)
	}
}
