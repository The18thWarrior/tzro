package memory

import (
	"path/filepath"
	"testing"
)

// TestEdgeThoughtPersistAndRetrieve verifies that edge thoughts can be
// persisted to SQLite and retrieved by target node or by latest in a task.
func TestEdgeThoughtPersistAndRetrieve(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_edge_thought.db")
	jsonPath := filepath.Join(tempDir, "test_edge_thought_db.json")
	defer cleanupTestDBs(t, dbPath, jsonPath)

	db := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
	if err := db.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	// Persist two edge thoughts for the same task, different edges
	et1 := EdgeThought{
		ID:             "et_1",
		TaskID:         "task-et-1",
		SourceNode:     "node_a",
		TargetNode:     "node_b",
		Thought:        "Found 15 search results about AI orchestration.",
		GoalConfidence: 0.4,
		StepIndex:      1,
		CreatedAt:      1000,
	}
	et2 := EdgeThought{
		ID:             "et_2",
		TaskID:         "task-et-1",
		SourceNode:     "node_b",
		TargetNode:     "node_c",
		Thought:        "Narrowed to 3 relevant papers on GAPG routing.",
		GoalConfidence: 0.75,
		StepIndex:      2,
		CreatedAt:      2000,
	}

	if err := db.AddEdgeThought(et1); err != nil {
		t.Fatalf("AddEdgeThought(et1) failed: %v", err)
	}
	if err := db.AddEdgeThought(et2); err != nil {
		t.Fatalf("AddEdgeThought(et2) failed: %v", err)
	}

	// Retrieve edge thoughts for node_b (should get et1)
	thoughtsForB, err := db.GetEdgeThoughtsForNode("task-et-1", "node_b")
	if err != nil {
		t.Fatalf("GetEdgeThoughtsForNode failed: %v", err)
	}
	if len(thoughtsForB) != 1 {
		t.Fatalf("expected 1 edge thought for node_b, got %d", len(thoughtsForB))
	}
	if thoughtsForB[0].ID != "et_1" || thoughtsForB[0].GoalConfidence != 0.4 {
		t.Errorf("unexpected edge thought for node_b: %+v", thoughtsForB[0])
	}

	// Retrieve edge thoughts for node_c (should get et2)
	thoughtsForC, err := db.GetEdgeThoughtsForNode("task-et-1", "node_c")
	if err != nil {
		t.Fatalf("GetEdgeThoughtsForNode failed: %v", err)
	}
	if len(thoughtsForC) != 1 || thoughtsForC[0].ID != "et_2" {
		t.Errorf("unexpected edge thoughts for node_c: %v", thoughtsForC)
	}

	// Retrieve latest edge thought for the task (should be et2, highest step_index)
	latest, err := db.GetLatestEdgeThought("task-et-1")
	if err != nil {
		t.Fatalf("GetLatestEdgeThought failed: %v", err)
	}
	if latest == nil {
		t.Fatal("expected non-nil latest edge thought")
	}
	if latest.ID != "et_2" || latest.GoalConfidence != 0.75 {
		t.Errorf("unexpected latest edge thought: %+v", latest)
	}

	// Retrieve latest for non-existent task → nil, no error
	missing, err := db.GetLatestEdgeThought("task-nonexistent")
	if err != nil {
		t.Fatalf("GetLatestEdgeThought for missing task failed: %v", err)
	}
	if missing != nil {
		t.Errorf("expected nil for missing task, got: %+v", missing)
	}
}
