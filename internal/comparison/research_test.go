package comparison

import (
	"testing"
)

func TestLoadResearchTasks(t *testing.T) {
	tasks, err := LoadTasksByCategory(CategoryResearch, 0)
	if err != nil {
		t.Fatalf("LoadTasksByCategory(research, 0) failed: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatal("Expected at least one research task, got 0")
	}

	// Verify all tasks have the research category
	for _, task := range tasks {
		if task.Category != CategoryResearch {
			t.Errorf("Task %s has category %q, expected %q", task.ID, task.Category, CategoryResearch)
		}
	}

	// Verify expected task IDs exist
	expectedIDs := map[string]bool{
		"compare_llm_frameworks":   false,
		"security_advisory_lookup": false,
		"market_analysis_local_ai": false,
		"technical_deep_dive_gguf": false,
		"multi_source_synthesis":   false,
	}
	for _, task := range tasks {
		if _, ok := expectedIDs[task.ID]; ok {
			expectedIDs[task.ID] = true
		}
	}
	for id, found := range expectedIDs {
		if !found {
			t.Errorf("Expected research task %q not found in loaded tasks", id)
		}
	}
}

func TestLoadResearchTasks_TierFilter(t *testing.T) {
	tasks, err := LoadTasksByCategory(CategoryResearch, 1)
	if err != nil {
		t.Fatalf("LoadTasksByCategory(research, 1) failed: %v", err)
	}
	for _, task := range tasks {
		if task.Tier != 1 {
			t.Errorf("Task %s has tier %d, expected 1", task.ID, task.Tier)
		}
	}
	// Tier 1 should have exactly 2 tasks
	if len(tasks) != 2 {
		t.Errorf("Expected 2 tier-1 research tasks, got %d", len(tasks))
	}
}

func TestLoadResearchTasks_TierFilter_Tier3(t *testing.T) {
	tasks, err := LoadTasksByCategory(CategoryResearch, 3)
	if err != nil {
		t.Fatalf("LoadTasksByCategory(research, 3) failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("Expected 1 tier-3 research task, got %d", len(tasks))
	}
	if len(tasks) > 0 && tasks[0].ID != "multi_source_synthesis" {
		t.Errorf("Expected tier-3 task to be 'multi_source_synthesis', got %q", tasks[0].ID)
	}
}

func TestResearchConditions(t *testing.T) {
	conditions := ResearchConditions()
	if len(conditions) == 0 {
		t.Fatal("ResearchConditions() returned empty slice")
	}

	// Should include cloud_react and cooperative at minimum
	has := make(map[string]bool)
	for _, c := range conditions {
		has[c] = true
	}
	required := []string{ConditionCloudReAct, ConditionCooperative, ConditionCloudDAG}
	for _, r := range required {
		if !has[r] {
			t.Errorf("ResearchConditions() missing required condition %q", r)
		}
	}
}

func TestResearchTasksHaveQualityRubrics(t *testing.T) {
	tasks, err := LoadTasksByCategory(CategoryResearch, 0)
	if err != nil {
		t.Fatalf("LoadTasksByCategory failed: %v", err)
	}
	for _, task := range tasks {
		if len(task.QualityRubric.Criteria) == 0 {
			t.Errorf("Task %s has no quality rubric criteria", task.ID)
		}
		if task.QualityRubric.MaxScore <= 0 {
			t.Errorf("Task %s has invalid max score: %f", task.ID, task.QualityRubric.MaxScore)
		}
		if task.Prompt == "" {
			t.Errorf("Task %s has empty prompt", task.ID)
		}
	}
}

func TestConditionsForCategory_Research(t *testing.T) {
	conditions := conditionsForCategory(CategoryResearch, "")
	if len(conditions) == 0 {
		t.Fatal("conditionsForCategory(research, '') returned empty slice")
	}

	// Verify it returns the same as ResearchConditions()
	expected := ResearchConditions()
	if len(conditions) != len(expected) {
		t.Errorf("conditionsForCategory returned %d conditions, expected %d", len(conditions), len(expected))
	}
}

func TestConditionsForCategory_ResearchOverride(t *testing.T) {
	conditions := conditionsForCategory(CategoryResearch, ConditionCooperative)
	if len(conditions) != 1 {
		t.Fatalf("Expected 1 condition with override, got %d", len(conditions))
	}
	if conditions[0] != ConditionCooperative {
		t.Errorf("Expected condition %q, got %q", ConditionCooperative, conditions[0])
	}
}

func TestJudgeSystemPromptForCategory_Research(t *testing.T) {
	prompt := JudgeSystemPromptForCategory(CategoryResearch)
	if prompt == "" {
		t.Fatal("JudgeSystemPromptForCategory(research) returned empty string")
	}
	if prompt == judgeSystemPrompt {
		t.Error("Research judge prompt should be different from the default docgen prompt")
	}
	if prompt == codeJudgeSystemPrompt {
		t.Error("Research judge prompt should be different from code judge prompt")
	}
}
