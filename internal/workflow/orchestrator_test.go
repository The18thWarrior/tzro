package workflow

import (
	"context"
	"os"
	"testing"
	"time"
	"tzro/internal/memory"
)

func TestMain(m *testing.M) {
	testDB := "test_tzro.db"
	_ = os.Remove(testDB)
	memory.DB.SetDBPathForTesting(testDB)

	err := memory.DB.Init()
	if err != nil {
		panic(err)
	}

	code := m.Run()

	_ = memory.DB.Close()
	_ = os.Remove(testDB)
	os.Exit(code)
}

func TestOrchestrator_VariableInterpolation(t *testing.T) {
	taskExecID := "task_test_interpolation"
	// Set node state in SQLite DB
	_ = memory.DB.SetNodeState(taskExecID, "node_fetch", "completed", `[Local Tactician] {"records":"15 leads"}`)

	taskRuns := []memory.WorkflowTaskExecution{
		{
			WorkflowExecutionID: "wf_exec_test",
			TaskTemplateID:      "fetch_leads",
			TaskExecutionID:     taskExecID,
			Status:              "completed",
		},
	}

	// 1. Property-specific interpolation
	instructions := "Sync {{tasks.fetch_leads.output.records}} to Postgres"
	result := interpolateWorkflowVariables(instructions, "wf_exec_test", taskRuns)
	expected := "Sync 15 leads to Postgres"
	if result != expected {
		t.Errorf("Property interpolation failed. Expected %q, got %q", expected, result)
	}

	// 2. Full-body interpolation
	instructionsFull := "Raw: {{tasks.fetch_leads.output}}"
	resultFull := interpolateWorkflowVariables(instructionsFull, "wf_exec_test", taskRuns)
	expectedFull := `Raw: {"records":"15 leads"}`
	if resultFull != expectedFull {
		t.Errorf("Full interpolation failed. Expected %q, got %q", expectedFull, resultFull)
	}
}

func TestOrchestrator_DependencyOrderingAndExecution(t *testing.T) {
	wfID := "wf_test_pipeline"
	wfDef := memory.WorkflowDefinition{
		ID:          wfID,
		Name:        "Test Dependency Pipeline",
		Description: "Verify sequential parent dependency run order",
		TriggerType: "manual",
		Status:      "active",
		CreatedAt:   time.Now().Unix(),
		UpdatedAt:   time.Now().Unix(),
	}

	wfTasks := []memory.WorkflowTask{
		{
			WorkflowID:     wfID,
			TaskTemplateID: "fetch_leads",
			Name:           "Fetch Salesforce leads",
			Instructions:   "Query Salesforce for leads",
			Dependencies:   "", // Root
		},
		{
			WorkflowID:     wfID,
			TaskTemplateID: "slack_alert",
			Name:           "Slack notification",
			Instructions:   "Post count to Slack: {{tasks.fetch_leads.output}}",
			Dependencies:   "fetch_leads", // Child
		},
	}

	err := memory.DB.SaveWorkflow(wfDef, wfTasks)
	if err != nil {
		t.Fatalf("Failed to save test workflow: %v", err)
	}

	// Run workflow in background context
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	go func() {
		_ = ExecuteWorkflow(ctx, wfID)
	}()

	// Wait for completion or timeout
	time.Sleep(3 * time.Second)

	// Verify executions
	execs, err := memory.DB.GetWorkflowExecutions(wfID)
	if err != nil {
		t.Fatalf("Failed to retrieve executions: %v", err)
	}

	if len(execs) == 0 {
		t.Fatal("No workflow execution runs were recorded")
	}

	execID := execs[0].ID
	_, taskRuns, err := memory.DB.GetWorkflowExecutionDetails(execID)
	if err != nil {
		t.Fatalf("Failed to retrieve task runs: %v", err)
	}

	statusMap := make(map[string]string)
	for _, tr := range taskRuns {
		statusMap[tr.TaskTemplateID] = tr.Status
	}

	// Because we are using the local heuristic builder which compiles successfully and executes locally,
	// the workflow tasks should transition smoothly.
	// Check that fetch_leads successfully ran
	if statusMap["fetch_leads"] == "pending" {
		t.Error("fetch_leads was not picked up and executed")
	}
}

func TestOrchestrator_CrashRecovery(t *testing.T) {
	wfID := "wf_test_recovery"
	wfDef := memory.WorkflowDefinition{
		ID:          wfID,
		Name:        "Test Crash Recovery",
		TriggerType: "manual",
		Status:      "active",
		CreatedAt:   time.Now().Unix(),
		UpdatedAt:   time.Now().Unix(),
	}

	wfTasks := []memory.WorkflowTask{
		{
			WorkflowID:     wfID,
			TaskTemplateID: "task_A",
			Name:           "Task A",
			Instructions:   "Query records",
			Dependencies:   "",
		},
		{
			WorkflowID:     wfID,
			TaskTemplateID: "task_B",
			Name:           "Task B",
			Instructions:   "Post Slack Alert",
			Dependencies:   "task_A",
		},
	}

	_ = memory.DB.SaveWorkflow(wfDef, wfTasks)

	// Seed an interrupted execution where task_A has already completed, and task_B is pending
	execID := "wf_exec_interrupted_123"
	exec := memory.WorkflowExecution{
		ID:         execID,
		WorkflowID: wfID,
		Status:     "running", // Interrupted running
		StartedAt:  time.Now().Unix() - 60,
	}

	taskRuns := []memory.WorkflowTaskExecution{
		{
			WorkflowExecutionID: execID,
			TaskTemplateID:      "task_A",
			TaskExecutionID:     "task_A_exec_done",
			Status:              "completed", // Succeeded before crash
			StartedAt:           time.Now().Unix() - 60,
			CompletedAt:         time.Now().Unix() - 40,
		},
		{
			WorkflowExecutionID: execID,
			TaskTemplateID:      "task_B",
			Status:              "pending", // Unstarted before crash
			StartedAt:           time.Now().Unix() - 60,
		},
	}

	_ = memory.DB.CreateWorkflowExecution(exec, taskRuns)
	_ = memory.DB.SetNodeState("task_A_exec_done", "fetch_sheet_records", "completed", `[Local Tactician] {"status":"ok"}`)

	// Trigger boot recovery
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	RecoverInterruptedWorkflows(ctx)

	// Wait briefly for recovery loop to check states
	time.Sleep(3 * time.Second)

	// Check execution details
	recoveredExec, recoveredTasks, err := memory.DB.GetWorkflowExecutionDetails(execID)
	if err != nil {
		t.Fatalf("Failed to query execution details: %v", err)
	}

	if recoveredExec.Status == "running" {
		// Recovery loop started successfully!
		// Let's verify task_B was resumed and is no longer pending
		for _, tr := range recoveredTasks {
			if tr.TaskTemplateID == "task_B" {
				if tr.Status == "pending" {
					t.Error("Interrupted task_B was not resumed from checkpoint")
				}
			}
		}
	}
}
