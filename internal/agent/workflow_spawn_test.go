package agent

import (
	"context"
	"os"
	"testing"

	"tzro/internal/memory"
)

func setupSpawnTestDB(t *testing.T) func() {
	t.Helper()
	oldDBPath := memory.DB.GetDBPathForTesting()
	dbName := "test_spawn_workflow.db"
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

// --- TDD Cycle 1: SpawnWorkflow creates a dynamic workflow ---

func TestSpawnWorkflow_CreatesWorkflow(t *testing.T) {
	cleanup := setupSpawnTestDB(t)
	defer cleanup()

	agent := NewBackgroundAgent("sentinel")

	wfID, err := agent.SpawnWorkflow(
		context.Background(),
		"Investigate 3 consecutive tool failures",
		WorkflowSpawnBudget{MaxTokens: 5000, MaxToolCalls: 10},
		1, // ApprovedLevel L1
		3, // MaxLevel L3 (sentinel's max)
	)
	if err != nil {
		t.Fatalf("SpawnWorkflow failed: %v", err)
	}
	if wfID == "" {
		t.Fatal("Expected non-empty workflow ID")
	}

	// Verify the workflow was created in the DB
	workflows, err := memory.DB.GetWorkflows()
	if err != nil {
		t.Fatalf("GetWorkflows failed: %v", err)
	}

	if len(workflows) != 1 {
		t.Fatalf("Expected 1 workflow, got %d", len(workflows))
	}

	wf := workflows[0]
	if wf.ID != wfID {
		t.Errorf("Workflow ID mismatch: expected '%s', got '%s'", wfID, wf.ID)
	}
	if wf.OrchestrationMode != "dynamic" {
		t.Errorf("OrchestrationMode: expected 'dynamic', got '%s'", wf.OrchestrationMode)
	}
	if wf.Goal != "Investigate 3 consecutive tool failures" {
		t.Errorf("Goal mismatch: got '%s'", wf.Goal)
	}
	if wf.ApprovedLevel != 1 {
		t.Errorf("ApprovedLevel: expected 1, got %d", wf.ApprovedLevel)
	}
	if wf.MaxTokens != 5000 {
		t.Errorf("MaxTokens: expected 5000, got %d", wf.MaxTokens)
	}
	if wf.MaxToolCalls != 10 {
		t.Errorf("MaxToolCalls: expected 10, got %d", wf.MaxToolCalls)
	}
	if wf.SpawnedBy != "sentinel" {
		t.Errorf("SpawnedBy: expected 'sentinel', got '%s'", wf.SpawnedBy)
	}
	if wf.TriggerType != "background" {
		t.Errorf("TriggerType: expected 'background', got '%s'", wf.TriggerType)
	}
}

// --- TDD Cycle 2: SpawnWorkflow blocks when ApprovedLevel > MaxLevel ---

func TestSpawnWorkflow_BlockedAboveMaxLevel(t *testing.T) {
	cleanup := setupSpawnTestDB(t)
	defer cleanup()

	agent := NewBackgroundAgent("observer")

	_, err := agent.SpawnWorkflow(
		context.Background(),
		"Attempt high-level spawn",
		WorkflowSpawnBudget{MaxTokens: 5000, MaxToolCalls: 10},
		3, // ApprovedLevel L3
		1, // MaxLevel L1 (observer's max)
	)
	if err == nil {
		t.Fatal("Expected error when ApprovedLevel > MaxLevel, got nil")
	}

	// Verify no workflow was created
	workflows, err := memory.DB.GetWorkflows()
	if err != nil {
		t.Fatalf("GetWorkflows failed: %v", err)
	}
	if len(workflows) != 0 {
		t.Errorf("Expected 0 workflows after blocked spawn, got %d", len(workflows))
	}
}

// --- TDD Cycle 3: SpawnWorkflow at exact MaxLevel succeeds ---

func TestSpawnWorkflow_ExactMaxLevelSucceeds(t *testing.T) {
	cleanup := setupSpawnTestDB(t)
	defer cleanup()

	agent := NewBackgroundAgent("sentinel")

	wfID, err := agent.SpawnWorkflow(
		context.Background(),
		"Spawn at exact limit",
		WorkflowSpawnBudget{MaxTokens: 1000, MaxToolCalls: 5},
		2, // ApprovedLevel L2
		2, // MaxLevel L2 (exactly equal)
	)
	if err != nil {
		t.Fatalf("SpawnWorkflow failed at exact MaxLevel: %v", err)
	}
	if wfID == "" {
		t.Fatal("Expected non-empty workflow ID")
	}

	workflows, _ := memory.DB.GetWorkflows()
	if len(workflows) != 1 {
		t.Fatalf("Expected 1 workflow, got %d", len(workflows))
	}
	if workflows[0].ApprovedLevel != 2 {
		t.Errorf("ApprovedLevel: expected 2, got %d", workflows[0].ApprovedLevel)
	}
}
