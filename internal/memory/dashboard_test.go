package memory

import (
	"fmt"
	"testing"
	"time"
)

func TestDashboardSpecs(t *testing.T) {
	cleanup := setupDynamicTestDB(t)
	defer cleanup()

	// 1. Initially, GetLatestDashboardSpec should return nil, nil
	spec, err := DB.GetLatestDashboardSpec()
	if err != nil {
		t.Fatalf("GetLatestDashboardSpec failed: %v", err)
	}
	if spec != nil {
		t.Fatalf("Expected nil spec, got %v", spec)
	}

	// 2. Save a dashboard spec
	now := time.Now().Unix()
	err = DB.SaveDashboardSpec("spec_1", `{"version":1,"layout":{"type":"Stack"}}`, now, "task_1", 14400)
	if err != nil {
		t.Fatalf("SaveDashboardSpec failed: %v", err)
	}

	// 3. Get latest dashboard spec
	spec, err = DB.GetLatestDashboardSpec()
	if err != nil {
		t.Fatalf("GetLatestDashboardSpec failed: %v", err)
	}
	if spec == nil {
		t.Fatal("Expected non-nil spec")
	}
	if spec.ID != "spec_1" {
		t.Errorf("Expected spec ID 'spec_1', got '%s'", spec.ID)
	}
	if spec.Spec != `{"version":1,"layout":{"type":"Stack"}}` {
		t.Errorf("Unexpected spec content: %s", spec.Spec)
	}
	if spec.GeneratedAt != now {
		t.Errorf("Expected GeneratedAt %d, got %d", now, spec.GeneratedAt)
	}

	// 4. Save 11 specs and ensure pruning keeps only the latest 10
	for i := 2; i <= 12; i++ {
		id := fmt.Sprintf("spec_%d", i)
		err = DB.SaveDashboardSpec(
			id,
			`{}`,
			now+int64(i),
			"task_1",
			14400,
		)
		if err != nil {
			t.Fatalf("SaveDashboardSpec failed at %d: %v", i, err)
		}
	}

	// Query row count directly from raw DB
	raw := DB.RawDB()
	if raw == nil {
		t.Fatal("Raw DB is nil")
	}
	var count int
	err = raw.QueryRow("SELECT COUNT(*) FROM dashboard_specs").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query row count: %v", err)
	}
	if count != 10 {
		t.Errorf("Expected exactly 10 rows in dashboard_specs table, got %d", count)
	}
}
