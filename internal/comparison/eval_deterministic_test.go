package comparison

import (
	"testing"
)

func TestAllTasksHaveDeterministicIdentifiers(t *testing.T) {
	categories := []string{CategoryDocgen, CategoryCodegen, CategoryDatanal, CategoryResearch}

	for _, cat := range categories {
		// Dev tasks
		devTasks, err := LoadTasksByCategory(cat, 0)
		if err != nil {
			t.Fatalf("failed to load %s dev tasks: %v", cat, err)
		}
		if len(devTasks) == 0 {
			t.Fatalf("no %s dev tasks loaded", cat)
		}
		for _, task := range devTasks {
			if len(task.ExpectedTools) == 0 {
				t.Errorf("task %s (%s) missing expectedTools", task.ID, cat)
			}
			if len(task.ExpectedFiles) == 0 && len(task.TargetPaths) == 0 && task.Filepath == "" && cat != CategoryResearch {
				t.Errorf("task %s (%s) missing file expectations", task.ID, cat)
			}
			if len(task.ExpectedSymbols) == 0 && task.ExpectedAnswer == "" && cat != CategoryDocgen {
				t.Errorf("task %s (%s) missing expectedSymbols or expectedAnswer", task.ID, cat)
			}
		}

		// Holdout tasks
		holdoutTasks, err := LoadHoldoutTasks(cat, 0)
		if err != nil {
			t.Fatalf("failed to load %s holdout tasks: %v", cat, err)
		}
		for _, task := range holdoutTasks {
			if len(task.ExpectedTools) == 0 {
				t.Errorf("holdout task %s (%s) missing expectedTools", task.ID, cat)
			}
			if len(task.ExpectedFiles) == 0 && len(task.TargetPaths) == 0 && task.Filepath == "" && cat != CategoryResearch {
				t.Errorf("holdout task %s (%s) missing file expectations", task.ID, cat)
			}
			if len(task.ExpectedSymbols) == 0 && task.ExpectedAnswer == "" {
				t.Errorf("holdout task %s (%s) missing expectedSymbols or expectedAnswer", task.ID, cat)
			}
		}
	}
}

func TestEvaluateDeterministic_WithExplicitExpectedFields(t *testing.T) {
	task := ComparisonTask{
		ID:                 "custom_check_task",
		Category:           CategoryCodegen,
		Language:           "go",
		Filepath:           "pkg/custom/handler.go",
		ExpectedTools:      []string{"write_file"},
		ExpectedFiles:      []string{"pkg/custom/handler.go"},
		ExpectedSymbols:    []string{"CustomHandler", "ServeHTTP", "Response"},
		ExpectedSignatures: []string{"func (h *CustomHandler) ServeHTTP"},
	}

	result := ComparisonResult{
		TaskID:        "custom_check_task",
		Condition:     ConditionCooperative,
		ToolCallCount: 1,
		OutputText: `package custom

import "net/http"

type CustomHandler struct{}

func (h *CustomHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	type Response struct {
		Message string
	}
}
`,
		Logs: "[Probe] Tool call: write_file pkg/custom/handler.go",
	}

	scorecard := EvaluateDeterministic(&result, &task)
	if scorecard.OverallScore < 4.0 {
		t.Errorf("expected overall score >= 4.0 for matching explicit expected fields, got %.2f", scorecard.OverallScore)
	}

	var foundToolsCheck, foundFilesCheck, foundSymbolsCheck, foundSigCheck bool
	for _, c := range scorecard.Checks {
		switch c.Name {
		case "ExpectedToolsCalled":
			foundToolsCheck = true
			if !c.Passed {
				t.Errorf("ExpectedToolsCalled check failed: %s", c.Message)
			}
		case "ExpectedFilesAccessed":
			foundFilesCheck = true
			if !c.Passed {
				t.Errorf("ExpectedFilesAccessed check failed: %s", c.Message)
			}
		case "ExpectedSymbolsPresent":
			foundSymbolsCheck = true
			if !c.Passed {
				t.Errorf("ExpectedSymbolsPresent check failed: %s", c.Message)
			}
		case "ExpectedSignaturePattern":
			foundSigCheck = true
			if !c.Passed {
				t.Errorf("ExpectedSignaturePattern check failed: %s", c.Message)
			}
		}
	}

	if !foundToolsCheck {
		t.Error("missing ExpectedToolsCalled check item in scorecard")
	}
	if !foundFilesCheck {
		t.Error("missing ExpectedFilesAccessed check item in scorecard")
	}
	if !foundSymbolsCheck {
		t.Error("missing ExpectedSymbolsPresent check item in scorecard")
	}
	if !foundSigCheck {
		t.Error("missing ExpectedSignaturePattern check item in scorecard")
	}
}

func TestEvaluateDeterministic_GoASTParse_ValidAndInvalid(t *testing.T) {
	task := ComparisonTask{
		ID:       "create_hello_handler",
		Category: CategoryCodegen,
		Language: "go",
		Filepath: "handlers/hello.go",
	}

	validResult := ComparisonResult{
		TaskID:        "create_hello_handler",
		Condition:     ConditionCooperative,
		ToolCallCount: 1,
		OutputText: `package handlers

import (
	"encoding/json"
	"net/http"
)

func HelloHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "hello, world"})
}
`,
		Logs: "[Probe] Tool call: write_file handlers/hello.go",
	}

	scorecard := EvaluateDeterministic(&validResult, &task)
	if scorecard.OverallScore < 4.0 {
		t.Errorf("expected overall score >= 4.0 for valid Go code, got %.2f", scorecard.OverallScore)
	}

	var goASTCheck *DeterministicCheckItem
	for _, c := range scorecard.Checks {
		if c.Name == "GoASTParse" {
			goASTCheck = &c
			break
		}
	}
	if goASTCheck == nil || !goASTCheck.Passed {
		t.Errorf("expected GoASTParse check to pass for valid Go code")
	}

	// Invalid Go code (syntax error)
	invalidResult := ComparisonResult{
		TaskID:        "create_hello_handler",
		Condition:     ConditionCooperative,
		ToolCallCount: 1,
		OutputText: `package handlers
func BrokenHandler(w http.ResponseWriter, r *http.Request { // missing closing paren
	w.Write([]byte("broken")
}`,
		Logs: "[Probe] Tool call: write_file handlers/hello.go",
	}

	invalidScorecard := EvaluateDeterministic(&invalidResult, &task)
	if invalidScorecard.OverallScore > 2.0 {
		t.Errorf("expected overall score <= 2.0 for syntax-errored Go code, got %.2f", invalidScorecard.OverallScore)
	}

	for _, c := range invalidScorecard.Checks {
		if c.Name == "GoASTParse" && c.Passed {
			t.Errorf("expected GoASTParse check to fail for syntax-errored Go code")
		}
	}
}

func TestEvaluateDeterministic_ToolUsage_ExplorationTasks(t *testing.T) {
	task := ComparisonTask{
		ID:          "cache_function_index",
		Category:    CategoryDocgen,
		TargetPaths: []string{"internal/cache/"},
	}

	// Case 1: 0 tool calls on a docgen exploration task
	noToolsResult := ComparisonResult{
		TaskID:        "cache_function_index",
		Condition:     ConditionCloudReAct,
		ToolCallCount: 0,
		OutputText:    "# Cache Function Index\n\nSome hallucinated content without reading any files.",
		Logs:          "Starting ReAct without calling any tools...",
	}

	scorecard := EvaluateDeterministic(&noToolsResult, &task)
	if scorecard.ToolUsageScore > 2.0 {
		t.Errorf("expected ToolUsageScore <= 2.0 when 0 tools called, got %.2f", scorecard.ToolUsageScore)
	}

	// Case 2: Tool calls executed and logged
	toolsResult := ComparisonResult{
		TaskID:        "cache_function_index",
		Condition:     ConditionCooperative,
		ToolCallCount: 5,
		OutputText:    "# Cache Function Index\n\n## Exported Functions\n- `PruneColumns`\n- `Process`\n",
		Logs: `[Probe] Tool call: list_dir internal/cache/
[Probe] Tool call: read_file internal/cache/cache.go
[Probe] Tool call: read_file internal/cache/metrics.go
[PhaseRunner] Phase "discover" completed: 3 steps, 3 tools called`,
	}

	toolsScorecard := EvaluateDeterministic(&toolsResult, &task)
	if toolsScorecard.ToolUsageScore < 4.0 {
		t.Errorf("expected ToolUsageScore >= 4.0 with real tool calls, got %.2f", toolsScorecard.ToolUsageScore)
	}
	if toolsScorecard.FileCoverageScore < 4.0 {
		t.Errorf("expected FileCoverageScore >= 4.0 when internal/cache files accessed, got %.2f", toolsScorecard.FileCoverageScore)
	}
}

func TestEvaluateDeterministic_Datanal_ExpectedAnswer(t *testing.T) {
	task := ComparisonTask{
		ID:             "lead_conversion_rate",
		Category:       CategoryDatanal,
		ExpectedAnswer: "Total Leads: 1000, Converted: 350, Conversion Rate: 35.0%",
	}

	correctResult := ComparisonResult{
		TaskID:        "lead_conversion_rate",
		Condition:     ConditionCooperative,
		ToolCallCount: 2,
		OutputText:    "Analysis Results:\n- Total Leads: 1000\n- Converted: 350\n- Conversion Rate: 35.0%\n\nThe top performing channel was Organic Search.",
		Logs:          "[Probe] Tool call: read_file helpers/LeadSuccess.csv",
	}

	scorecard := EvaluateDeterministic(&correctResult, &task)
	if scorecard.DomainScore < 4.0 {
		t.Errorf("expected DomainScore >= 4.0 for matching expected answer, got %.2f", scorecard.DomainScore)
	}

	wrongResult := ComparisonResult{
		TaskID:        "lead_conversion_rate",
		Condition:     ConditionCooperative,
		ToolCallCount: 2,
		OutputText:    "Analysis Results:\n- Total Leads: 500\n- Converted: 50\n- Conversion Rate: 10.0%",
		Logs:          "[Probe] Tool call: read_file helpers/LeadSuccess.csv",
	}

	wrongScorecard := EvaluateDeterministic(&wrongResult, &task)
	if wrongScorecard.DomainScore > 3.0 {
		t.Errorf("expected DomainScore <= 3.0 for incorrect numerical answers, got %.2f", wrongScorecard.DomainScore)
	}
}

func TestEvaluateDeterministic_Research_Citations(t *testing.T) {
	task := ComparisonTask{
		ID:       "ai_orchestration_trends",
		Category: CategoryResearch,
		Prompt:   "Research AI orchestration trends and cite authoritative sources.",
	}

	citedResult := ComparisonResult{
		TaskID:        "ai_orchestration_trends",
		Condition:     ConditionCooperative,
		ToolCallCount: 4,
		OutputText: `# AI Orchestration Trends

| Pattern | Trade-offs | Sources |
|---------|------------|---------|
| DAG Pipelines | Parallelism | https://arxiv.org/abs/2310.08560 |
| ReAct Loops | Dynamic routing | https://langchain.com/blog/agents |
| Speculative Exec | Low latency | https://github.com/vllm-project/vllm |
`,
		Logs: `[Probe] Tool call: web_search AI orchestration
[Probe] Tool call: web_browse https://arxiv.org/abs/2310.08560`,
	}

	scorecard := EvaluateDeterministic(&citedResult, &task)
	if scorecard.DomainScore < 4.5 {
		t.Errorf("expected DomainScore >= 4.5 with real citations and table, got %.2f", scorecard.DomainScore)
	}

	uncitedResult := ComparisonResult{
		TaskID:        "ai_orchestration_trends",
		Condition:     ConditionCooperative,
		ToolCallCount: 1,
		OutputText:    "# AI Orchestration Trends\n\nHere are some trends without any sources cited.",
		Logs:          "[Probe] Tool call: web_search AI orchestration",
	}

	uncitedScorecard := EvaluateDeterministic(&uncitedResult, &task)
	if uncitedScorecard.DomainScore > 2.5 {
		t.Errorf("expected DomainScore <= 2.5 when no citations present, got %.2f", uncitedScorecard.DomainScore)
	}
}

func TestCalculateCompositeScore_Guardrails(t *testing.T) {
	// Case 1: Normal blend (50/50)
	scorecard := &DeterministicScorecard{OverallScore: 4.0}
	composite, _ := CalculateCompositeScore(scorecard, 4.0, 0.5)
	if composite != 4.0 {
		t.Errorf("expected composite 4.0, got %.2f", composite)
	}

	// Case 2: Deterministic failure (e.g. 1.0) with hallucinating LLM judge (5.0) -> Guardrail caps score
	failedScorecard := &DeterministicScorecard{OverallScore: 1.0}
	cappedComposite, reason := CalculateCompositeScore(failedScorecard, 5.0, 0.5)
	if cappedComposite > 2.0 {
		t.Errorf("expected capped composite <= 2.0 on deterministic failure, got %.2f", cappedComposite)
	}
	if reason == "" {
		t.Error("expected reason to explain deterministic cap")
	}

	// Case 3: Offline / No LLM score
	offlineScorecard := &DeterministicScorecard{OverallScore: 4.5}
	offlineComposite, _ := CalculateCompositeScore(offlineScorecard, 0.0, 0.5)
	if offlineComposite != 4.5 {
		t.Errorf("expected offline composite to equal deterministic score 4.5, got %.2f", offlineComposite)
	}
}
