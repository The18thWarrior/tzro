package comparison

import (
	"testing"
)

// Phase 6: Data Analysis Benchmark tests

func TestLoadTasksByCategory_DatanalLoadsAllTasks(t *testing.T) {
	tasks, err := LoadTasksByCategory(CategoryDatanal, 0)
	if err != nil {
		t.Fatalf("failed to load datanal tasks: %v", err)
	}
	if len(tasks) != 5 {
		t.Errorf("expected 5 datanal tasks, got %d", len(tasks))
	}
	// Verify all tasks have expected fields
	for _, task := range tasks {
		if task.Category != CategoryDatanal {
			t.Errorf("task %s has category %q, want %q", task.ID, task.Category, CategoryDatanal)
		}
		if task.ExpectedAnswer == "" {
			t.Errorf("task %s is missing expectedAnswer", task.ID)
		}
		if len(task.QualityRubric.Criteria) == 0 {
			t.Errorf("task %s is missing quality rubric criteria", task.ID)
		}
	}
}

func TestLoadTasksByCategory_DatanalTier1(t *testing.T) {
	tasks, err := LoadTasksByCategory(CategoryDatanal, 1)
	if err != nil {
		t.Fatalf("failed to load datanal tier 1 tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tier-1 datanal tasks, got %d", len(tasks))
	}
	for _, task := range tasks {
		if task.Tier != 1 {
			t.Errorf("task %s has tier %d, want 1", task.ID, task.Tier)
		}
	}
}

func TestLoadTasksByCategory_DatanalTier2(t *testing.T) {
	tasks, err := LoadTasksByCategory(CategoryDatanal, 2)
	if err != nil {
		t.Fatalf("failed to load datanal tier 2 tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tier-2 datanal tasks, got %d", len(tasks))
	}
}

func TestLoadTasksByCategory_DatanalTier3(t *testing.T) {
	tasks, err := LoadTasksByCategory(CategoryDatanal, 3)
	if err != nil {
		t.Fatalf("failed to load datanal tier 3 tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 tier-3 datanal task, got %d", len(tasks))
	}
}

func TestDatanalConditions(t *testing.T) {
	conds := DatanalConditions()
	expected := []string{ConditionCloudReAct, ConditionLocalOnly, ConditionCooperative}
	if len(conds) != len(expected) {
		t.Fatalf("expected %d conditions, got %d", len(expected), len(conds))
	}
	for i, c := range conds {
		if c != expected[i] {
			t.Errorf("condition[%d] = %q, want %q", i, c, expected[i])
		}
	}
}

func TestConditionsForCategory_Datanal(t *testing.T) {
	conds := conditionsForCategory(CategoryDatanal, "")
	expected := DatanalConditions()
	if len(conds) != len(expected) {
		t.Fatalf("expected %d conditions for datanal, got %d", len(expected), len(conds))
	}
	for i := range conds {
		if conds[i] != expected[i] {
			t.Errorf("condition[%d] = %q, want %q", i, conds[i], expected[i])
		}
	}
}

func TestConditionsForCategory_DatanalWithOverride(t *testing.T) {
	conds := conditionsForCategory(CategoryDatanal, ConditionCooperative)
	if len(conds) != 1 || conds[0] != ConditionCooperative {
		t.Errorf("expected single override condition, got %v", conds)
	}
}

func TestJudgeSystemPromptForCategory_Datanal(t *testing.T) {
	prompt := JudgeSystemPromptForCategory(CategoryDatanal)
	if prompt != datanalJudgeSystemPrompt {
		t.Error("expected datanalJudgeSystemPrompt for datanal category")
	}
}

func TestDatanalTasksHaveExpectedAnswers(t *testing.T) {
	tasks, err := LoadTasksByCategory(CategoryDatanal, 0)
	if err != nil {
		t.Fatalf("failed to load datanal tasks: %v", err)
	}
	for _, task := range tasks {
		if task.ExpectedAnswer == "" {
			t.Errorf("task %s (tier %d) is missing expectedAnswer", task.ID, task.Tier)
		}
	}
}
