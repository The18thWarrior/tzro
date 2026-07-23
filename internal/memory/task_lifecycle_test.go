package memory

import (
	"testing"
)

// Slice 1 tests for ADR-0054: tasks table CRUD

func TestCreateTask_InsertsAndRetrievable(t *testing.T) {
	sdb := &SqliteDatabase{dbPath: ":memory:", jsonPath: "test.json"}
	if err := sdb.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer sdb.Close()

	err := sdb.CreateTask("task_123", "Analyze benchmark results")
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	rec, err := sdb.GetTask("task_123")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if rec == nil {
		t.Fatal("GetTask returned nil for existing task")
	}
	if rec.TaskID != "task_123" {
		t.Errorf("TaskID = %q, want %q", rec.TaskID, "task_123")
	}
	if rec.Status != "planning" {
		t.Errorf("Status = %q, want %q", rec.Status, "planning")
	}
	if rec.Prompt != "Analyze benchmark results" {
		t.Errorf("Prompt = %q, want %q", rec.Prompt, "Analyze benchmark results")
	}
	if rec.CreatedAt == 0 {
		t.Error("CreatedAt should be non-zero")
	}
}

func TestUpdateTaskStatus_SetsStatusAndError(t *testing.T) {
	sdb := &SqliteDatabase{dbPath: ":memory:", jsonPath: "test.json"}
	if err := sdb.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer sdb.Close()

	_ = sdb.CreateTask("task_fail", "bad prompt")

	err := sdb.UpdateTaskStatus("task_fail", "failed", "SCT expansion failed: unknown tool")
	if err != nil {
		t.Fatalf("UpdateTaskStatus failed: %v", err)
	}

	rec, _ := sdb.GetTask("task_fail")
	if rec == nil {
		t.Fatal("GetTask returned nil")
	}
	if rec.Status != "failed" {
		t.Errorf("Status = %q, want %q", rec.Status, "failed")
	}
	if rec.Error != "SCT expansion failed: unknown tool" {
		t.Errorf("Error = %q, want %q", rec.Error, "SCT expansion failed: unknown tool")
	}
}

func TestUpdateTaskStatus_CompletedSetsTimestamp(t *testing.T) {
	sdb := &SqliteDatabase{dbPath: ":memory:", jsonPath: "test.json"}
	if err := sdb.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer sdb.Close()

	_ = sdb.CreateTask("task_done", "explore the codebase")

	err := sdb.UpdateTaskStatus("task_done", "completed", "")
	if err != nil {
		t.Fatalf("UpdateTaskStatus failed: %v", err)
	}

	rec, _ := sdb.GetTask("task_done")
	if rec == nil {
		t.Fatal("GetTask returned nil")
	}
	if rec.CompletedAt == 0 {
		t.Error("CompletedAt should be non-zero for completed tasks")
	}
}

func TestGetTask_ReturnsNilForMissing(t *testing.T) {
	sdb := &SqliteDatabase{dbPath: ":memory:", jsonPath: "test.json"}
	if err := sdb.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer sdb.Close()

	rec, err := sdb.GetTask("nonexistent")
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if rec != nil {
		t.Errorf("GetTask should return nil for missing task, got %+v", rec)
	}
}

func TestGetRecentTasks_QueriesTasksTable(t *testing.T) {
	sdb := &SqliteDatabase{dbPath: ":memory:", jsonPath: "test.json"}
	if err := sdb.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer sdb.Close()

	_ = sdb.CreateTask("task_a", "prompt a")
	_ = sdb.UpdateTaskStatus("task_a", "completed", "")

	_ = sdb.CreateTask("task_b", "prompt b")
	_ = sdb.UpdateTaskStatus("task_b", "failed", "error")

	_ = sdb.CreateTask("task_c", "prompt c")
	// task_c stays in "planning" status

	// Get all tasks
	tasks, err := sdb.GetRecentTasks(10, "")
	if err != nil {
		t.Fatalf("GetRecentTasks failed: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("Expected 3 tasks, got %d", len(tasks))
	}

	// Get only failed tasks
	failed, err := sdb.GetRecentTasks(10, "failed")
	if err != nil {
		t.Fatalf("GetRecentTasks(failed) failed: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("Expected 1 failed task, got %d", len(failed))
	}
	if failed[0].TaskID != "task_b" {
		t.Errorf("Expected task_b, got %s", failed[0].TaskID)
	}
}
