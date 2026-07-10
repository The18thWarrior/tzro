package comparison

import (
	"context"
	"fmt"
	"os"
	"testing"
	"tzro/internal/config"
)

func TestRepro_CacheFunctionIndex_Cooperative(t *testing.T) {
	// Skip if we don't have a local model running or if it's too slow for CI
	if os.Getenv("REPRO_TEST") == "" {
		t.Skip("Set REPRO_TEST=1 to run this reproduction test")
	}

	ctx := context.Background()
	config.Load()

	tasks, err := LoadTasks(1) // cache_function_index is Tier 1
	if err != nil {
		t.Fatalf("Failed to load tasks: %v", err)
	}

	var task *ComparisonTask
	for _, tk := range tasks {
		if tk.ID == "cache_function_index" {
			task = &tk
			break
		}
	}

	if task == nil {
		t.Fatal("Task cache_function_index not found")
	}

	// Setup pricing
	pricing := DefaultPricing()

	// Create a temporary output dir
	outDir := fmt.Sprintf("repro_%s", task.ID)
	os.MkdirAll(outDir, 0755)
	defer os.RemoveAll(outDir)

	// In cooperative mode, RunDAGCondition is called
	res, err := RunDAGCondition(ctx, ConditionCooperative, *task, pricing, outDir)
	if err != nil {
		t.Fatalf("RunDAGCondition failed: %v", err)
	}

	t.Logf("Score: %f", res.QualityScore)
	t.Logf("Summary: %s", res.QualityNotes)

	if res.QualityScore < 4.0 {
		t.Errorf("Score too low: %f", res.QualityScore)
	}
}
