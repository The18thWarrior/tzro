package proactivity

import (
	"os"
	"sync"
	"testing"
	"time"

	"tzro/internal/memory"
)

func setupPreemptionTestDB(t *testing.T) func() {
	t.Helper()
	oldDBPath := memory.DB.GetDBPathForTesting()
	dbName := "test_preemption.db"
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

// --- TDD Cycle 1: Foreground preemption cancels background contexts ---

func TestPreemption_ForegroundCancelsBackground(t *testing.T) {
	ClearActiveTasks()
	ClearCallbacks()
	defer ClearActiveTasks()
	defer ClearCallbacks()

	scheduler := NewDefaultAttentionScheduler()

	// Simulate a running background action
	var cancelled bool
	var mu sync.Mutex

	scheduler.mu.Lock()
	scheduler.activeCancels["bg_task_1"] = func() {
		mu.Lock()
		cancelled = true
		mu.Unlock()
	}
	scheduler.mu.Unlock()

	RegisterPreemptionCallback(scheduler.CancelActiveBackgroundActions)

	// Register a foreground task — this should trigger preemption
	RegisterActiveUserTask("user_task_1")

	// Allow preemption to fire
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if !cancelled {
		t.Error("Expected background action to be cancelled when foreground registered")
	}
}

// --- TDD Cycle 2: Interrupted task is distinct from failed ---

func TestPreemption_InterruptedDistinctFromFailed(t *testing.T) {
	cleanup := setupPreemptionTestDB(t)
	defer cleanup()

	now := time.Now().Unix()

	wf := memory.WorkflowDefinition{
		ID:                "wf_interrupt_distinct",
		Name:              "Interrupt Distinction Test",
		TriggerType:       "background",
		Status:            "active",
		CreatedAt:         now,
		UpdatedAt:         now,
		OrchestrationMode: "dynamic",
	}
	_ = memory.DB.SaveWorkflow(wf, nil)

	exec := memory.WorkflowExecution{
		ID:         "exec_interrupt_distinct",
		WorkflowID: wf.ID,
		Status:     "running",
		StartedAt:  now,
	}
	taskRuns := []memory.WorkflowTaskExecution{
		{
			WorkflowExecutionID: exec.ID,
			TaskTemplateID:      "task_ok",
			Status:              "completed",
			StartedAt:           now,
			CompletedAt:         now,
		},
		{
			WorkflowExecutionID: exec.ID,
			TaskTemplateID:      "task_interrupted",
			Status:              "running",
			StartedAt:           now,
		},
		{
			WorkflowExecutionID: exec.ID,
			TaskTemplateID:      "task_failed",
			Status:              "failed",
			StartedAt:           now,
			CompletedAt:         now,
		},
	}
	_ = memory.DB.CreateWorkflowExecution(exec, taskRuns)

	// Mark task_interrupted as interrupted
	_ = memory.DB.UpdateWorkflowTaskExecution(exec.ID, "task_interrupted", "", "interrupted", 0)

	// Retrieve and verify each status is distinct
	_, tasks, err := memory.DB.GetWorkflowExecutionDetails(exec.ID)
	if err != nil {
		t.Fatalf("GetWorkflowExecutionDetails failed: %v", err)
	}

	statusMap := make(map[string]string)
	for _, tr := range tasks {
		statusMap[tr.TaskTemplateID] = tr.Status
	}

	if statusMap["task_ok"] != "completed" {
		t.Errorf("task_ok: expected 'completed', got '%s'", statusMap["task_ok"])
	}
	if statusMap["task_interrupted"] != "interrupted" {
		t.Errorf("task_interrupted: expected 'interrupted', got '%s'", statusMap["task_interrupted"])
	}
	if statusMap["task_failed"] != "failed" {
		t.Errorf("task_failed: expected 'failed', got '%s'", statusMap["task_failed"])
	}
}

// --- TDD Cycle 3: Clearing foreground triggers resume callbacks ---

func TestPreemption_ClearingForegroundTriggersResume(t *testing.T) {
	ClearActiveTasks()
	ClearCallbacks()
	defer ClearActiveTasks()
	defer ClearCallbacks()

	var resumed bool
	var mu sync.Mutex

	RegisterResumeCallback(func() {
		mu.Lock()
		resumed = true
		mu.Unlock()
	})

	// Register foreground task
	RegisterActiveUserTask("user_task_1")

	// Deregister — foreground clears, should trigger resume
	DeregisterActiveUserTask("user_task_1")

	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if !resumed {
		t.Error("Expected resume callback to fire when foreground cleared")
	}
}

// --- TDD Cycle 4: Multiple foreground tasks — resume only fires when ALL clear ---

func TestPreemption_ResumeOnlyWhenAllForegroundClears(t *testing.T) {
	ClearActiveTasks()
	ClearCallbacks()
	defer ClearActiveTasks()
	defer ClearCallbacks()

	var resumeCount int
	var mu sync.Mutex

	RegisterResumeCallback(func() {
		mu.Lock()
		resumeCount++
		mu.Unlock()
	})

	// Register two foreground tasks
	RegisterActiveUserTask("user_task_a")
	RegisterActiveUserTask("user_task_b")

	// Deregister first — should NOT trigger resume (one still active)
	DeregisterActiveUserTask("user_task_a")
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	afterFirst := resumeCount
	mu.Unlock()

	if afterFirst != 0 {
		t.Errorf("Expected 0 resume callbacks after first deregister, got %d", afterFirst)
	}

	// Deregister second — should trigger resume (all clear)
	DeregisterActiveUserTask("user_task_b")
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	afterSecond := resumeCount
	mu.Unlock()

	if afterSecond != 1 {
		t.Errorf("Expected 1 resume callback after all foreground cleared, got %d", afterSecond)
	}
}
