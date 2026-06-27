package comparison

import (
	"math"
	"testing"

	"tzro/internal/inference"
)

func TestEstimateCost_CalculatesFromTokenCounts(t *testing.T) {
	cloud := inference.TokenUsage{
		PromptTokens:     1000,
		CompletionTokens: 500,
	}
	local := inference.TokenUsage{
		PromptTokens:     5000,
		CompletionTokens: 2000,
	}
	pricing := PricingTable{
		PromptPer1KTokens:     0.003,
		CompletionPer1KTokens: 0.015,
	}

	cost := EstimateCost(cloud, local, pricing)

	// Expected: (1000/1000 * 0.003) + (500/1000 * 0.015) = 0.003 + 0.0075 = 0.0105
	// Local tokens should contribute $0.00
	expected := 0.0105
	if math.Abs(cost-expected) > 1e-9 {
		t.Errorf("EstimateCost = %f, want %f", cost, expected)
	}
}

func TestEstimateCost_ZeroTokensReturnsZero(t *testing.T) {
	cloud := inference.TokenUsage{}
	local := inference.TokenUsage{}
	pricing := DefaultPricing()

	cost := EstimateCost(cloud, local, pricing)
	if cost != 0.0 {
		t.Errorf("EstimateCost with zero tokens = %f, want 0.0", cost)
	}
}

func TestLoadTasks_ReturnsAllTasks(t *testing.T) {
	tasks, err := LoadTasks(0)
	if err != nil {
		t.Fatalf("LoadTasks(0) failed: %v", err)
	}
	if len(tasks) != 5 {
		t.Errorf("LoadTasks(0) returned %d tasks, want 5", len(tasks))
	}
}

func TestLoadTasks_FiltersByTier(t *testing.T) {
	tasks, err := LoadTasks(1)
	if err != nil {
		t.Fatalf("LoadTasks(1) failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("LoadTasks(1) returned %d tasks, want 1", len(tasks))
	}
	if tasks[0].ID != "cache_function_index" {
		t.Errorf("LoadTasks(1) returned task %q, want %q", tasks[0].ID, "cache_function_index")
	}
}

func TestLoadTasks_InvalidTierReturnsError(t *testing.T) {
	_, err := LoadTasks(99)
	if err == nil {
		t.Error("LoadTasks(99) should return error for non-existent tier")
	}
}

func TestAllConditions_ReturnsFiveConditions(t *testing.T) {
	conditions := AllConditions()
	if len(conditions) != 5 {
		t.Errorf("AllConditions() returned %d, want 5", len(conditions))
	}
	expected := []string{ConditionCloudReAct, ConditionCloudDAGRaw, ConditionCloudDAG, ConditionLocalOnly, ConditionCooperative}
	for i, c := range conditions {
		if c != expected[i] {
			t.Errorf("AllConditions()[%d] = %q, want %q", i, c, expected[i])
		}
	}
}
