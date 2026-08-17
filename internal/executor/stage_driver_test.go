package executor

import (
	"context"
	"strings"
	"testing"
)

func TestDeterministicQueueDriver_FileReadQueue(t *testing.T) {
	// Arrange
	items := []QueueItem{
		{Tool: "read_file", Args: map[string]interface{}{"path": "file1.go"}},
		{Tool: "read_file", Args: map[string]interface{}{"path": "file2.go"}},
		{Tool: "read_file", Args: map[string]interface{}{"path": "file3.go"}},
	}

	driver := NewDeterministicQueueDriver(items)

	phase := &Phase{
		Name:         "discover",
		AllowedTools: []string{"read_file"},
		StepBudget:   10,
	}

	var dispatchedTools []string
	var persistedSteps []string

	stepCounter := 0
	runnerCtx := &PhaseRunnerContext{
		TaskID:            "task_test_123",
		ProbeID:           "probe_test_123",
		GlobalStepCounter: &stepCounter,
		ToolDispatcher: func(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
			dispatchedTools = append(dispatchedTools, toolName+":"+args["path"].(string))
			return "content of " + args["path"].(string), nil
		},
		PersistStep: func(phaseName, toolName string, args map[string]interface{}, output, reasoning string) {
			persistedSteps = append(persistedSteps, toolName+":"+args["path"].(string))
		},
	}

	// Act
	result, err := driver.Execute(context.Background(), phase, runnerCtx)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
	if result.StepsUsed != 3 {
		t.Errorf("expected StepsUsed=3, got %d", result.StepsUsed)
	}
	if len(dispatchedTools) != 3 {
		t.Fatalf("expected 3 dispatched tools, got %d", len(dispatchedTools))
	}
	if dispatchedTools[0] != "read_file:file1.go" || dispatchedTools[1] != "read_file:file2.go" || dispatchedTools[2] != "read_file:file3.go" {
		t.Errorf("unexpected dispatched tools: %v", dispatchedTools)
	}
	if len(persistedSteps) != 3 {
		t.Errorf("expected 3 persisted steps, got %d", len(persistedSteps))
	}
	if stepCounter != 3 {
		t.Errorf("expected GlobalStepCounter=3, got %d", stepCounter)
	}
}

func TestDeterministicQueueDriver_StepBudgetEnforcement(t *testing.T) {
	// 5 items in queue, but StepBudget = 2
	items := []QueueItem{
		{Tool: "read_file", Args: map[string]interface{}{"path": "file1.go"}},
		{Tool: "read_file", Args: map[string]interface{}{"path": "file2.go"}},
		{Tool: "read_file", Args: map[string]interface{}{"path": "file3.go"}},
		{Tool: "read_file", Args: map[string]interface{}{"path": "file4.go"}},
		{Tool: "read_file", Args: map[string]interface{}{"path": "file5.go"}},
	}

	driver := NewDeterministicQueueDriver(items)

	phase := &Phase{
		Name:         "deep_read",
		AllowedTools: []string{"read_file"},
		StepBudget:   2,
	}

	var dispatchedCount int
	stepCounter := 0
	runnerCtx := &PhaseRunnerContext{
		TaskID:            "task_test_budget",
		ProbeID:           "probe_test_budget",
		GlobalStepCounter: &stepCounter,
		ToolDispatcher: func(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
			dispatchedCount++
			return "data", nil
		},
		PersistStep: func(phaseName, toolName string, args map[string]interface{}, output, reasoning string) {},
	}

	result, err := driver.Execute(context.Background(), phase, runnerCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StepsUsed != 2 {
		t.Errorf("expected StepsUsed=2, got %d", result.StepsUsed)
	}
	if dispatchedCount != 2 {
		t.Errorf("expected dispatchedCount=2, got %d", dispatchedCount)
	}
}

func TestPhaseRunner_DeterministicStageDriver(t *testing.T) {
	items := []QueueItem{
		{Tool: "read_file", Args: map[string]interface{}{"path": "main.go"}},
		{Tool: "read_file", Args: map[string]interface{}{"path": "util.go"}},
	}

	runner := &PhaseRunner{
		InitialPhase: "discover",
		PhaseOrder:   []string{"discover", "synthesize"},
		ToolDispatcher: func(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
			return "content of " + args["path"].(string), nil
		},
		Phases: map[string]*Phase{
			"discover": {
				Name:         "discover",
				AllowedTools: []string{"read_file"},
				StepBudget:   5,
				Driver:       NewDeterministicQueueDriver(items),
				Transition: func(step int, result PhaseResult, err error) string {
					return "synthesize"
				},
			},
			"synthesize": {
				Name:       "synthesize",
				StepBudget: 1,
				Driver:     NewDeterministicQueueDriver(nil),
			},
		},
	}

	// Mock inference engine for synthesis only
	mockEngine := NewMockPhaseEngine()
	mockEngine.PhaseResponses["synthesize"] = []MockPhaseStep{
		{Reasoning: "Final synthesis summary"},
	}

	results, err := runner.Run(context.Background(), "task_pr_1", "probe_pr_1", mockEngine, mockEngine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 phase results, got %d", len(results))
	}

	if results[0].PhaseName != "discover" || results[0].StepsUsed != 2 {
		t.Errorf("unexpected discover phase result: %+v", results[0])
	}
	if len(results[0].ToolsCalled) != 2 {
		t.Errorf("expected 2 tools called in discover, got %d", len(results[0].ToolsCalled))
	}
	if results[1].PhaseName != "synthesize" {
		t.Errorf("unexpected second phase: %s", results[1].PhaseName)
	}

	manifest := runner.BuildManifest(results)
	if manifest.TotalStepsUsed != 2 { // discover used 2, synthesize used 0 tool steps
		t.Errorf("expected manifest total steps 2, got %d", manifest.TotalStepsUsed)
	}
}

func TestDeterministicQueueDriver_CodeFileSkeletonAndRollingBufferCap(t *testing.T) {
	// Create 50 code file items with large 5k char function bodies
	var items []QueueItem
	for i := 0; i < 50; i++ {
		items = append(items, QueueItem{
			Tool: "read_file",
			Args: map[string]interface{}{"path": "pkg/file" + string(rune('a'+i%26)) + ".go"},
		})
	}

	driver := NewDeterministicQueueDriver(items)
	phase := &Phase{
		Name:         "deep_read",
		AllowedTools: []string{"read_file"},
		StepBudget:   50,
	}

	largeGoFileContent := `package mypkg

// User represents an entity in the system.
type User struct {
	ID   string
	Name string
}

// ProcessUserData performs complex data operations.
func ProcessUserData(u *User, data []byte) ([]byte, error) {
` + "// 100 lines of body logic\n" + strings.Repeat("\t// comment line with dummy payload\n", 100) + `
	return data, nil
}
`

	stepCounter := 0
	runnerCtx := &PhaseRunnerContext{
		TaskID:            "task_skeleton_test",
		ProbeID:           "probe_skeleton_test",
		GlobalStepCounter: &stepCounter,
		ToolDispatcher: func(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
			return largeGoFileContent, nil
		},
		PersistStep: func(phaseName, toolName string, args map[string]interface{}, output, reasoning string) {},
	}

	result, err := driver.Execute(context.Background(), phase, runnerCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1. Verify that the output log contains extracted skeletons rather than the 100 body lines
	if len(result.ToolOutputLog) == 0 {
		t.Fatalf("expected non-empty ToolOutputLog")
	}

	firstLogEntry := result.ToolOutputLog[0]
	if !strings.Contains(firstLogEntry, "func ProcessUserData") {
		t.Errorf("expected skeleton to contain func signature, got: %s", firstLogEntry)
	}
	if strings.Contains(firstLogEntry, "comment line with dummy payload") {
		t.Errorf("expected body comments to be stripped from skeleton, but found in log")
	}

	// 2. Verify that total ToolOutputLog characters are strictly bounded by 24,000 chars
	totalLogChars := 0
	for _, entry := range result.ToolOutputLog {
		totalLogChars += len(entry)
	}
	if totalLogChars > 24000 {
		t.Errorf("expected total log chars <= 24000, got %d", totalLogChars)
	}
}

