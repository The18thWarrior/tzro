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

// --- Codegen task tests ---

func TestLoadTasksByCategory_Codegen_ReturnsAllCodegenTasks(t *testing.T) {
	tasks, err := LoadTasksByCategory(CategoryCodegen, 0)
	if err != nil {
		t.Fatalf("LoadTasksByCategory(codegen, 0) failed: %v", err)
	}
	if len(tasks) != 10 {
		t.Errorf("LoadTasksByCategory(codegen, 0) returned %d tasks, want 10", len(tasks))
	}
}

func TestLoadTasksByCategory_Codegen_FiltersByTier(t *testing.T) {
	for tier := 1; tier <= 5; tier++ {
		tasks, err := LoadTasksByCategory(CategoryCodegen, tier)
		if err != nil {
			t.Fatalf("LoadTasksByCategory(codegen, %d) failed: %v", tier, err)
		}
		if len(tasks) != 2 {
			t.Errorf("LoadTasksByCategory(codegen, %d) returned %d tasks, want 2", tier, len(tasks))
		}

		// Each tier should have one create and one update
		var hasCreate, hasUpdate bool
		for _, task := range tasks {
			switch task.Action {
			case "create":
				hasCreate = true
			case "update":
				hasUpdate = true
			}
		}
		if !hasCreate || !hasUpdate {
			t.Errorf("Tier %d missing create=%v or update=%v task", tier, hasCreate, hasUpdate)
		}
	}
}

func TestLoadTasksByCategory_Docgen_BackwardsCompatible(t *testing.T) {
	tasks, err := LoadTasksByCategory(CategoryDocgen, 0)
	if err != nil {
		t.Fatalf("LoadTasksByCategory(docgen, 0) failed: %v", err)
	}
	if len(tasks) != 5 {
		t.Errorf("LoadTasksByCategory(docgen, 0) returned %d tasks, want 5", len(tasks))
	}

	// All should have category=docgen
	for _, task := range tasks {
		if task.Category != CategoryDocgen {
			t.Errorf("task %q has category %q, want %q", task.ID, task.Category, CategoryDocgen)
		}
	}
}

func TestLoadTasksByCategory_EmptyCategory_DefaultsToDocgen(t *testing.T) {
	tasks, err := LoadTasksByCategory("", 0)
	if err != nil {
		t.Fatalf("LoadTasksByCategory(\"\", 0) failed: %v", err)
	}
	if len(tasks) != 5 {
		t.Errorf("LoadTasksByCategory(\"\", 0) returned %d tasks, want 5 (docgen default)", len(tasks))
	}
}

func TestLoadTasksByCategory_UnknownCategory_ReturnsError(t *testing.T) {
	_, err := LoadTasksByCategory("bogus", 0)
	if err == nil {
		t.Error("LoadTasksByCategory with unknown category should return error")
	}
}

func TestCodegenTask_HasRequiredFields(t *testing.T) {
	tasks, err := LoadTasksByCategory(CategoryCodegen, 0)
	if err != nil {
		t.Fatalf("failed to load codegen tasks: %v", err)
	}

	for _, task := range tasks {
		if task.Category != CategoryCodegen {
			t.Errorf("task %q: category = %q, want %q", task.ID, task.Category, CategoryCodegen)
		}
		if task.Spec == "" {
			t.Errorf("task %q: spec is empty", task.ID)
		}
		if task.Filepath == "" {
			t.Errorf("task %q: filepath is empty", task.ID)
		}
		if task.Language == "" {
			t.Errorf("task %q: language is empty", task.ID)
		}
		if task.Action != "create" && task.Action != "update" {
			t.Errorf("task %q: action = %q, want 'create' or 'update'", task.ID, task.Action)
		}
		if task.Action == "update" && task.SeedFile == "" {
			t.Errorf("task %q: update task has empty seedFile", task.ID)
		}
		if task.Action == "create" && task.SeedFile != "" {
			t.Errorf("task %q: create task should not have seedFile, got %q", task.ID, task.SeedFile)
		}
		if len(task.QualityRubric.Criteria) == 0 {
			t.Errorf("task %q: qualityRubric has no criteria", task.ID)
		}
	}
}

func TestCodegenTask_UniqueIDs(t *testing.T) {
	tasks, err := LoadTasksByCategory(CategoryCodegen, 0)
	if err != nil {
		t.Fatalf("failed to load codegen tasks: %v", err)
	}

	seen := make(map[string]bool)
	for _, task := range tasks {
		if seen[task.ID] {
			t.Errorf("duplicate task ID: %q", task.ID)
		}
		seen[task.ID] = true
	}
}

func TestReadSeedFile_ValidFile(t *testing.T) {
	data, err := ReadSeedFile("validate_struct.go")
	if err != nil {
		t.Fatalf("ReadSeedFile(validate_struct.go) failed: %v", err)
	}
	if len(data) == 0 {
		t.Error("ReadSeedFile returned empty data")
	}
}

func TestReadSeedFile_AllUpdateTaskSeedsExist(t *testing.T) {
	tasks, err := LoadTasksByCategory(CategoryCodegen, 0)
	if err != nil {
		t.Fatalf("failed to load codegen tasks: %v", err)
	}

	for _, task := range tasks {
		if task.Action != "update" {
			continue
		}
		data, err := ReadSeedFile(task.SeedFile)
		if err != nil {
			t.Errorf("task %q: seed file %q not found: %v", task.ID, task.SeedFile, err)
		}
		if len(data) == 0 {
			t.Errorf("task %q: seed file %q is empty", task.ID, task.SeedFile)
		}
	}
}

func TestReadSeedFile_MissingFileReturnsError(t *testing.T) {
	_, err := ReadSeedFile("nonexistent.go")
	if err == nil {
		t.Error("ReadSeedFile with missing file should return error")
	}
}



func TestJudgeSystemPromptForCategory_Codegen(t *testing.T) {
	prompt := JudgeSystemPromptForCategory(CategoryCodegen)
	if prompt != codeJudgeSystemPrompt {
		t.Error("JudgeSystemPromptForCategory(codegen) should return codeJudgeSystemPrompt")
	}
}

func TestJudgeSystemPromptForCategory_Docgen(t *testing.T) {
	prompt := JudgeSystemPromptForCategory(CategoryDocgen)
	if prompt != judgeSystemPrompt {
		t.Error("JudgeSystemPromptForCategory(docgen) should return judgeSystemPrompt")
	}
}

func TestJudgeSystemPromptForCategory_EmptyDefaultsToDocgen(t *testing.T) {
	prompt := JudgeSystemPromptForCategory("")
	if prompt != judgeSystemPrompt {
		t.Error("JudgeSystemPromptForCategory(\"\") should default to docgen prompt")
	}
}

func TestCodegenConditions_IncludesTzroCode(t *testing.T) {
	conditions := CodegenConditions()

	// Should include tzro_code, cloud_code, and tzro_draft
	var foundTzroCode, foundCloudCode, foundDraft bool
	for _, c := range conditions {
		if c == ConditionTzroCode {
			foundTzroCode = true
		}
		if c == ConditionCloudCode {
			foundCloudCode = true
		}
		if c == ConditionTzroDraft {
			foundDraft = true
		}
	}
	if !foundTzroCode {
		t.Errorf("CodegenConditions() = %v, want %s included", conditions, ConditionTzroCode)
	}
	if !foundCloudCode {
		t.Errorf("CodegenConditions() = %v, want %s included", conditions, ConditionCloudCode)
	}
	if !foundDraft {
		t.Errorf("CodegenConditions() = %v, want %s included", conditions, ConditionTzroDraft)
	}

	// Should NOT include cloud_react (not an apples-to-apples comparison)
	for _, c := range conditions {
		if c == ConditionCloudReAct {
			t.Errorf("CodegenConditions() should not include %s", ConditionCloudReAct)
		}
	}
}

func TestAllConditions_DoesNotIncludeTzroCode(t *testing.T) {
	for _, c := range AllConditions() {
		if c == ConditionTzroCode {
			t.Errorf("AllConditions() should not include %s (codegen-only)", ConditionTzroCode)
		}
		if c == ConditionCloudCode {
			t.Errorf("AllConditions() should not include %s (codegen-only)", ConditionCloudCode)
		}
		if c == ConditionTzroDraft {
			t.Errorf("AllConditions() should not include %s (codegen-only)", ConditionTzroDraft)
		}
	}
}

func TestCodegenConditionsForTier_T1(t *testing.T) {
	conditions := CodegenConditionsForTier(1)
	assertContains(t, conditions, ConditionCloudCode, "T1")
	assertContains(t, conditions, ConditionTzroCode, "T1")
	assertContains(t, conditions, ConditionTzroDraft, "T1")
}

func TestCodegenConditionsForTier_T2(t *testing.T) {
	conditions := CodegenConditionsForTier(2)
	assertContains(t, conditions, ConditionCloudCode, "T2")
	assertNotContains(t, conditions, ConditionTzroCode, "T2")
	assertContains(t, conditions, ConditionTzroDraft, "T2")
}

func TestCodegenConditionsForTier_T3(t *testing.T) {
	conditions := CodegenConditionsForTier(3)
	assertContains(t, conditions, ConditionCloudCode, "T3")
	assertNotContains(t, conditions, ConditionTzroCode, "T3")
	assertContains(t, conditions, ConditionTzroDraft, "T3")
}

func TestCodegenConditionsForTier_T4(t *testing.T) {
	conditions := CodegenConditionsForTier(4)
	assertContains(t, conditions, ConditionCloudCode, "T4")
	assertNotContains(t, conditions, ConditionTzroCode, "T4")
	assertContains(t, conditions, ConditionTzroDraft, "T4")
}

func TestCodegenConditionsForTier_T5(t *testing.T) {
	conditions := CodegenConditionsForTier(5)
	assertContains(t, conditions, ConditionCloudCode, "T5")
	assertNotContains(t, conditions, ConditionTzroCode, "T5")
	assertContains(t, conditions, ConditionTzroDraft, "T5")
}

func assertContains(t *testing.T, conditions []string, target, tier string) {
	t.Helper()
	for _, c := range conditions {
		if c == target {
			return
		}
	}
	t.Errorf("CodegenConditionsForTier(%s) = %v, want %s included", tier, conditions, target)
}

func assertNotContains(t *testing.T, conditions []string, target, tier string) {
	t.Helper()
	for _, c := range conditions {
		if c == target {
			t.Errorf("CodegenConditionsForTier(%s) = %v, should NOT include %s", tier, conditions, target)
			return
		}
	}
}
