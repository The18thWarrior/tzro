package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tzro/internal/compiler"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/tools"
)

// MockProbeInference is a test double for the inference engine.
// It returns pre-configured responses for each step, allowing tests
// to control the probe's behavior deterministically.
type MockProbeInference struct {
	Responses []string // One response per call, in order
	CallCount int
}

func (m *MockProbeInference) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error) {
	if m.CallCount >= len(m.Responses) {
		// Default: synthesize immediately
		return `{"synthesis":"default synthesis"}`, nil
	}
	response := m.Responses[m.CallCount]
	m.CallCount++
	return response, nil
}

func (m *MockProbeInference) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string) (string, error) {
	// Delegate to Infer by extracting system and user messages
	var sys, usr string
	for _, msg := range messages {
		if msg.Role == "system" {
			sys = msg.Content
		} else if msg.Role == "user" {
			usr = msg.Content
		}
	}
	return m.Infer(ctx, sys, usr, jsonSchema)
}

func setupProbeTestDB(t *testing.T) func() {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "probe_test.db")

	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting(dbPath)

	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init test DB: %v", err)
	}

	return func() {
		memory.DB.Close()
		os.Remove(dbPath)
		memory.DB.SetDBPathForTesting(oldDBPath)
	}
}

func setupProbeTestTools(t *testing.T) {
	t.Helper()
	// Create test fixture directory for filesystem tools
	tempDir := t.TempDir()
	// Resolve symlinks (macOS: /var → /private/var) so PathValidator's
	// root prefix check matches EvalSymlinks'd paths during validation.
	tempDir, _ = filepath.EvalSymlinks(tempDir)
	testFile := filepath.Join(tempDir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Set TZRO_DIR and register tools
	os.Setenv("TZRO_DIR", tempDir)
	if err := tools.Init(""); err != nil {
		t.Fatalf("failed to init tools: %v", err)
	}
}

func TestRunProbe_ExecutesToolCallsAndReturns(t *testing.T) {
	cleanup := setupProbeTestDB(t)
	defer cleanup()
	setupProbeTestTools(t)

	mock := &MockProbeInference{
		Responses: []string{
			// Step 1: tool call
			`Let me explore the directory
<ACTION>{"tool":"list_dir","arguments":{"path":"."}}</ACTION>`,
			// Step 2: synthesize ready
			`Found the structure
<SYNTHESIZE_READY>`,
			// Pass 2: synthesis
			`{"synthesis":"The project contains a main.go file with a simple hello program."}`,
		},
	}

	config := compiler.ProbeConfig{
		Goal:         "Explore the test project",
		AllowedTools: []string{"read_file", "list_dir", "search_files"},
		StepBudget:   5,
		CompactEvery: 3,
	}

	result, err := RunProbe(context.Background(), "task_test", "probe_test_1", config, mock, mock, nil)
	if err != nil {
		t.Fatalf("RunProbe failed: %v", err)
	}

	if result != "The project contains a main.go file with a simple hello program." {
		t.Errorf("unexpected synthesis result: %s", result)
	}

	if mock.CallCount != 3 {
		t.Errorf("expected 3 inference calls, got %d", mock.CallCount)
	}
}

func TestRunProbe_PersistsThoughtSteps(t *testing.T) {
	cleanup := setupProbeTestDB(t)
	defer cleanup()
	setupProbeTestTools(t)

	mock := &MockProbeInference{
		Responses: []string{
			`Exploring directory
<ACTION>{"tool":"list_dir","arguments":{"path":"."}}</ACTION>`,
			`Looking deeper
<ACTION>{"tool":"list_dir","arguments":{"path":"."}}</ACTION>`,
			`Got enough info
<SYNTHESIZE_READY>`,
			`{"synthesis":"All done"}`,
		},
	}

	config := compiler.ProbeConfig{
		Goal:         "Test persistence",
		AllowedTools: []string{"list_dir"},
		StepBudget:   5,
		CompactEvery: 10, // Don't compact during this test
	}

	probeID := "probe_persist_test"
	_, err := RunProbe(context.Background(), "task_test", probeID, config, mock, mock, nil)
	if err != nil {
		t.Fatalf("RunProbe failed: %v", err)
	}

	// Verify steps were persisted
	steps, err := memory.DB.GetThoughtSteps(probeID)
	if err != nil {
		t.Fatalf("GetThoughtSteps failed: %v", err)
	}

	// Steps 1 and 2 are tool calls, step 3 is synthesize (also persisted)
	if len(steps) != 3 {
		t.Fatalf("expected 3 persisted steps, got %d", len(steps))
	}

	if steps[0].Thought != "Exploring directory\n<ACTION>{\"tool\":\"list_dir\",\"arguments\":{\"path\":\".\"}}</ACTION>" {
		t.Errorf("step 1 thought mismatch: %s", steps[0].Thought)
	}
	if steps[0].ToolName != "list_dir" {
		t.Errorf("step 1 tool name mismatch: %s", steps[0].ToolName)
	}
	if steps[1].Thought != "Looking deeper\n<ACTION>{\"tool\":\"list_dir\",\"arguments\":{\"path\":\".\"}}</ACTION>" {
		t.Errorf("step 2 thought mismatch: %s", steps[1].Thought)
	}
}

// TestRunProbe_EdgeEntryAccumulation verifies that the probe loop accumulates
// EdgeEntry records and passes them to synthesis instead of calling compactThoughtChain.
// ADR-0059: Supersedes the old TestRunProbe_RollingCompaction test.
func TestRunProbe_EdgeEntryAccumulation(t *testing.T) {
	cleanup := setupProbeTestDB(t)
	defer cleanup()
	setupProbeTestTools(t)

	// Use the test file created by setupProbeTestTools for tool calls that succeed.
	tzroDir, _ := filepath.EvalSymlinks(os.Getenv("TZRO_DIR"))
	testFile := filepath.Join(tzroDir, "test.go")

	// Set up 4 steps — previously compaction would trigger at step 3.
	// With ADR-0059, no compaction occurs; edge entries accumulate instead.
	mock := &MockProbeInference{
		Responses: []string{
			fmt.Sprintf("Step 1\n<ACTION>{\"tool\":\"read_file\",\"arguments\":{\"path\":\"%s\"}}</ACTION>", testFile),
			fmt.Sprintf("Step 2\n<ACTION>{\"tool\":\"read_file\",\"arguments\":{\"path\":\"%s\"}}</ACTION>", testFile),
			fmt.Sprintf("Step 3\n<ACTION>{\"tool\":\"read_file\",\"arguments\":{\"path\":\"%s\"}}</ACTION>", testFile),
			`Done
<SYNTHESIZE_READY>`,
			`{"synthesis":"Final synthesis via edge entries"}`,
		},
	}

	config := compiler.ProbeConfig{
		Goal:         "Test edge entry accumulation",
		AllowedTools: []string{"read_file", "list_dir"},
		StepBudget:   5,
		CompactEvery: 3, // still set but no longer triggers compaction
	}

	probeID := "probe_compact_test"
	result, err := RunProbe(context.Background(), "task_test", probeID, config, mock, mock, nil)
	if err != nil {
		t.Fatalf("RunProbe failed: %v", err)
	}

	if result != "Final synthesis via edge entries" {
		t.Errorf("unexpected result: %s", result)
	}

	// Verify NO compaction summaries were created (ADR-0059: compaction removed from loop)
	_, err = memory.DB.GetLatestSummary(probeID)
	if err == nil {
		t.Error("expected no compaction summaries with ADR-0059, but GetLatestSummary succeeded")
	}

	// Verify thought steps WERE still persisted to SQLite (for Recall Node)
	steps, _ := memory.DB.GetThoughtSteps(probeID)
	if len(steps) < 3 {
		t.Errorf("expected at least 3 persisted thought steps, got %d", len(steps))
	}
}

func TestRunProbe_ConvergesOnHighConfidence(t *testing.T) {
	cleanup := setupProbeTestDB(t)
	defer cleanup()
	setupProbeTestTools(t)

	// Step 1 returns synthesize ready immediately
	mock := &MockProbeInference{
		Responses: []string{
			`I already know the answer
<SYNTHESIZE_READY>`,
			`{"synthesis":"Immediate convergence result"}`,
		},
	}

	config := compiler.ProbeConfig{
		Goal:         "Quick answer",
		AllowedTools: []string{"read_file"},
		StepBudget:   2, // Small budget: minStepBudget = 2/2 = 1, allows synthesis at step 1
		CompactEvery: 3,
	}

	result, err := RunProbe(context.Background(), "task_test", "probe_converge", config, mock, mock, nil)
	if err != nil {
		t.Fatalf("RunProbe failed: %v", err)
	}

	if result != "Immediate convergence result" {
		t.Errorf("unexpected result: %s", result)
	}
	if mock.CallCount != 2 {
		t.Errorf("expected 2 inference calls (1 introspect, 1 synthesis), got %d", mock.CallCount)
	}
}

func TestRunProbe_BudgetExhaustionForcesSynthesis(t *testing.T) {
	cleanup := setupProbeTestDB(t)
	defer cleanup()
	setupProbeTestTools(t)

	// All steps return tool calls — never converges
	mock := &MockProbeInference{
		Responses: []string{
			`Step 1
<ACTION>{"tool":"list_dir","arguments":{"path":"."}}</ACTION>`,
			`Step 2
<ACTION>{"tool":"list_dir","arguments":{"path":"."}}</ACTION>`,
			`Step 3
<ACTION>{"tool":"list_dir","arguments":{"path":"."}}</ACTION>`,
			// Budget exhausted (stepBudget=3), forced synthesis inference call:
			`{"synthesis":"Forced synthesis: explored 3 steps but couldn't converge"}`,
		},
	}

	config := compiler.ProbeConfig{
		Goal:         "Test budget exhaustion",
		AllowedTools: []string{"list_dir"},
		StepBudget:   3,
		CompactEvery: 10, // Don't compact during this test
	}

	result, err := RunProbe(context.Background(), "task_test", "probe_budget", config, mock, mock, nil)
	if err != nil {
		t.Fatalf("RunProbe failed: %v", err)
	}

	if result != "Forced synthesis: explored 3 steps but couldn't converge" {
		t.Errorf("unexpected forced synthesis result: %s", result)
	}
}

func TestRunProbe_RejectsDisallowedTools(t *testing.T) {
	cleanup := setupProbeTestDB(t)
	defer cleanup()
	setupProbeTestTools(t)

	// Try to use a tool that's not in the allowed list
	mock := &MockProbeInference{
		Responses: []string{
			`Trying disallowed tool
<ACTION>{"tool":"web_search","arguments":{"query":"hack"}}</ACTION>`,
			`Done
<SYNTHESIZE_READY>`,
			`{"synthesis":"Tool was rejected"}`,
		},
	}

	config := compiler.ProbeConfig{
		Goal:         "Test tool restrictions",
		AllowedTools: []string{"read_file", "list_dir"}, // web_search NOT allowed
		StepBudget:   5,
		CompactEvery: 10,
	}

	probeID := "probe_disallowed"
	_, err := RunProbe(context.Background(), "task_test", probeID, config, mock, mock, nil)
	if err != nil {
		t.Fatalf("RunProbe failed: %v", err)
	}

	// Verify the step was persisted with an error in tool_output
	steps, err := memory.DB.GetThoughtSteps(probeID)
	if err != nil {
		t.Fatalf("GetThoughtSteps failed: %v", err)
	}

	if len(steps) < 1 {
		t.Fatal("expected at least 1 step")
	}

	// The first step should have an error about disallowed tool
	if steps[0].ToolOutput == "" {
		t.Error("expected tool output to contain error about disallowed tool")
	}
}

func TestCompiler_AcceptsProbeNodes(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "test_probe_compile",
		Nodes: []compiler.GraphNode{
			{ID: "explore", Type: "probe", Instructions: "Explore the codebase", ProbeConfig: &compiler.ProbeConfig{
				Goal:         "Understand the architecture",
				AllowedTools: []string{"read_file", "list_dir"},
				StepBudget:   10,
				CompactEvery: 3,
			}},
			{ID: "report", Type: "action", Action: "save_memory", Instructions: "Save findings from {{nodes.explore.output}}"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "explore", TargetID: "report"},
		},
	}

	levels, err := compiler.CompileAndSort(graph)
	if err != nil {
		t.Fatalf("CompileAndSort failed with probe node: %v", err)
	}

	if len(levels) != 2 {
		t.Errorf("expected 2 levels, got %d: %v", len(levels), levels)
	}

	if levels[0][0] != "explore" || levels[1][0] != "report" {
		t.Errorf("unexpected level order: %v", levels)
	}
}

func TestSCT_PassesProbeNodeThrough(t *testing.T) {
	graph := &compiler.ExecutionGraph{
		TaskID: "test_probe_sct",
		Nodes: []compiler.GraphNode{
			{ID: "probe1", Type: "probe", Instructions: "Explore", ProbeConfig: &compiler.ProbeConfig{
				Goal:       "Find main.go",
				StepBudget: 5,
			}},
			{ID: "action1", Type: "action", Action: "save_memory", Instructions: "Save {{nodes.probe1.output}}"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "probe1", TargetID: "action1"},
		},
	}

	expanded, err := compiler.ExpandToSCTGraph(graph, func(action string) (string, error) {
		return `{"mock": "schema"}`, nil
	})
	if err != nil {
		t.Fatalf("ExpandToSCTGraph failed: %v", err)
	}

	// Find the probe node in expanded graph — should be unchanged
	nodeMap := make(map[string]compiler.GraphNode)
	for _, n := range expanded.Nodes {
		nodeMap[n.ID] = n
	}

	probeNode, ok := nodeMap["probe1"]
	if !ok {
		t.Fatal("probe node 'probe1' not found in expanded graph")
	}
	if probeNode.Type != "probe" {
		t.Errorf("expected probe node type to be 'probe', got '%s'", probeNode.Type)
	}
	if probeNode.ProbeConfig == nil {
		t.Error("expected ProbeConfig to be preserved on probe node")
	}

	// action1 should be expanded into validator + exec
	if _, ok := nodeMap["action1_validator"]; !ok {
		t.Error("expected action1 to be expanded into action1_validator")
	}
	if _, ok := nodeMap["action1_exec"]; !ok {
		t.Error("expected action1 to be expanded into action1_exec")
	}

	// Topological sort should work
	_, err = compiler.CompileAndSort(expanded)
	if err != nil {
		t.Fatalf("CompileAndSort on expanded SCT graph failed: %v", err)
	}
}

// Suppress unused import warnings
var _ = json.Marshal
var _ = fmt.Sprintf

func TestExtractPathFromText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected string
	}{
		{
			name:     "absolute path",
			text:     "Read the file at /home/user/project/tzro/CONTEXT.md to understand the architecture",
			expected: "/home/user/project/tzro/CONTEXT.md",
		},
		{
			name:     "quoted bare filename without extension",
			text:     "The file 'tzro-mcp' is the main binary entry point",
			expected: "tzro-mcp",
		},
		{
			name:     "backtick bare filename",
			text:     "I need to read `bootstrap.go` to understand initialization",
			expected: "bootstrap.go",
		},
		{
			name:     "filename with extension",
			text:     "Read CONTEXT.md to understand the architecture",
			expected: "CONTEXT.md",
		},
		{
			name:     "known directory pattern",
			text:     "Explore internal/compiler to find the DAG compilation logic",
			expected: "internal/compiler",
		},
		{
			name:     "bare hyphenated name without quotes",
			text:     "The tzro-mcp binary handles all MCP protocol communication",
			expected: "tzro-mcp",
		},
		{
			name:     "bare known directory",
			text:     "List the contents of bin to find executables",
			expected: "bin",
		},
		{
			name:     "empty text",
			text:     "",
			expected: "",
		},
		{
			name:     "no path at all",
			text:     "I need more information about the system",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPathFromText(tt.text)
			if result != tt.expected {
				t.Errorf("extractPathFromText(%q) = %q, want %q", tt.text, result, tt.expected)
			}
		})
	}
}

func TestRescueEmptyPath_ResolvesRelativePaths(t *testing.T) {
	// Set TZRO_DIR so config.ResolvePath can resolve
	oldTzroDir := os.Getenv("TZRO_DIR")
	os.Setenv("TZRO_DIR", "/test/project")
	defer os.Setenv("TZRO_DIR", oldTzroDir)

	tests := []struct {
		name         string
		toolName     string
		args         map[string]interface{}
		thought      string
		expectedPath string
	}{
		{
			name:         "resolves relative path from existing args",
			toolName:     "list_dir",
			args:         map[string]interface{}{"path": "bin"},
			thought:      "List the bin directory",
			expectedPath: "/test/project/bin",
		},
		{
			name:         "resolves relative path rescued from thought",
			toolName:     "read_file",
			args:         map[string]interface{}{},
			thought:      "Read CONTEXT.md to understand the domain",
			expectedPath: "/test/project/CONTEXT.md",
		},
		{
			name:         "does not modify absolute paths",
			toolName:     "read_file",
			args:         map[string]interface{}{"path": "/absolute/path/file.go"},
			thought:      "Reading a specific file",
			expectedPath: "/absolute/path/file.go",
		},
		{
			name:         "skips non-filesystem tools",
			toolName:     "web_search",
			args:         map[string]interface{}{},
			thought:      "Search for something",
			expectedPath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rescueEmptyPathFromThought(tt.toolName, tt.args, tt.thought)
			pathVal, _ := result["path"].(string)
			if pathVal != tt.expectedPath {
				t.Errorf("rescueEmptyPathFromThought path = %q, want %q", pathVal, tt.expectedPath)
			}
		})
	}
}

func TestRunProbe_ConsecutiveErrorsForceSynthesis(t *testing.T) {
	cleanup := setupProbeTestDB(t)
	defer cleanup()
	setupProbeTestTools(t)

	// Mock engine that produces 4 tool calls hitting errors (nonexistent paths),
	// then gets the synthesis hint, and produces synthesis.
	// With stepBudget=15 and minStepBudget=7, without the error guard rail
	// this would burn through all 15 steps. With it, after 3 consecutive
	// errors the min step is lowered and synthesis is allowed.
	mock := &MockProbeInference{
		Responses: []string{
			// Steps 1-3: tool calls that will fail (paths don't exist)
			"Reading nonexistent path\n<ACTION>{\"tool\":\"read_file\",\"arguments\":{\"path\":\"/nonexistent/path1.go\"}}</ACTION>",
			"Trying another path\n<ACTION>{\"tool\":\"read_file\",\"arguments\":{\"path\":\"/nonexistent/path2.go\"}}</ACTION>",
			"One more path\n<ACTION>{\"tool\":\"list_dir\",\"arguments\":{\"path\":\"/nonexistent/dir\"}}</ACTION>",
			// Step 4: after error warning, model signals synthesis
			"I should synthesize what I have\n<SYNTHESIZE_READY>",
			// Pass 2: synthesis
			`{"synthesis":"Found limited information due to path errors but synthesized what was available."}`,
		},
	}

	config := compiler.ProbeConfig{
		Goal:         "Explore a project",
		AllowedTools: []string{"read_file", "list_dir", "search_files"},
		StepBudget:   15,
		CompactEvery: 5,
	}

	result, err := RunProbe(context.Background(), "task_error_test", "probe_error_1", config, mock, mock, nil)
	if err != nil {
		t.Fatalf("RunProbe failed: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty synthesis result")
	}

	// The probe should have completed in ≤5 steps (3 errors + synthesis signal + synthesis pass),
	// not burned through all 15
	if mock.CallCount > 6 {
		t.Errorf("probe made %d inference calls, expected ≤6 (should have been forced to synthesize early)", mock.CallCount)
	}
}

func TestRunProbe_AdaptiveMinStepAllowsEarlySynthesis(t *testing.T) {
	cleanup := setupProbeTestDB(t)
	defer cleanup()
	setupProbeTestTools(t)

	// Set TZRO_DIR to the temp dir so that relative paths resolve to valid locations
	tempDir := t.TempDir()
	for _, name := range []string{"file1.go", "file2.go", "file3.go", "file4.go", "file5.go", "file6.go", "file7.go", "file8.go"} {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte("package main"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}
	os.Setenv("TZRO_DIR", tempDir)

	// Mock engine: 8 tool call responses + synthesis signal + synthesis pass.
	// Note: The tool calls will fail because the paths are in a different tempDir
	// than the one registered with setupProbeTestTools. With adaptive futility
	// detection (max(5, stepBudget/4) = 7 for budget 30), the probe aborts
	// after 5 consecutive failed initial steps (earliest point where both
	// maxConsecutiveErrors=3 and futilityThreshold=7 conditions are met).
	responses := make([]string, 0, 11)
	for i := 1; i <= 8; i++ {
		responses = append(responses, fmt.Sprintf(
			"Reading file %d\n<ACTION>{\"tool\":\"read_file\",\"arguments\":{\"path\":\"%s\"}}</ACTION>",
			i, filepath.Join(tempDir, fmt.Sprintf("file%d.go", i)),
		))
	}
	// Step 9: synthesis ready
	responses = append(responses, "I have read all relevant files\n<SYNTHESIZE_READY>")
	// Pass 2: synthesis
	responses = append(responses, `{"synthesis":"Complete analysis of all 8 source files."}`)

	mock := &MockProbeInference{Responses: responses}

	cfg := compiler.ProbeConfig{
		Goal:         "Analyze source files",
		AllowedTools: []string{"read_file", "list_dir", "search_files"},
		StepBudget:   30,
		CompactEvery: 5,
	}

	result, err := RunProbe(context.Background(), "task_adaptive_test", "probe_adaptive_1", cfg, mock, mock, nil)
	if err != nil {
		t.Fatalf("RunProbe failed: %v", err)
	}

	// The probe should produce some synthesis output (the futility abort
	// triggers synthesis with whatever context is available).
	if result == "" {
		t.Error("expected non-empty synthesis result")
	}

	// With adaptive futility detection (max(5, stepBudget/4) = 7 for budget 30),
	// the probe aborts after at most 7 failed initial steps
	// (7 tool call inferences + 1 synthesis pass = 8 calls max).
	if mock.CallCount > 9 {
		t.Errorf("probe made %d inference calls, expected ≤9 (adaptive futility detection should abort within 7 failed steps)", mock.CallCount)
	}
}

// ContextCapturingMock records whether MaxTokensKey was present in context for
// each InferMessages (step) vs Infer (synthesis) call.
type ContextCapturingMock struct {
	Responses             []string
	CallCount             int
	StepMaxTokensPresent  []bool // one entry per InferMessages call
	SynthMaxTokensPresent []bool // one entry per Infer call
}

func (m *ContextCapturingMock) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error) {
	_, hasMaxTokens := ctx.Value(inference.MaxTokensKey).(int)
	m.SynthMaxTokensPresent = append(m.SynthMaxTokensPresent, hasMaxTokens)

	if m.CallCount >= len(m.Responses) {
		return `{"synthesis":"default synthesis"}`, nil
	}
	response := m.Responses[m.CallCount]
	m.CallCount++
	return response, nil
}

func (m *ContextCapturingMock) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string) (string, error) {
	_, hasMaxTokens := ctx.Value(inference.MaxTokensKey).(int)
	m.StepMaxTokensPresent = append(m.StepMaxTokensPresent, hasMaxTokens)

	if m.CallCount >= len(m.Responses) {
		return `<SYNTHESIZE_READY>`, nil
	}
	response := m.Responses[m.CallCount]
	m.CallCount++
	return response, nil
}

func TestRunProbe_StepCallsSetsMaxTokensKey_SynthesisDoesNot(t *testing.T) {
	cleanup := setupProbeTestDB(t)
	defer cleanup()
	setupProbeTestTools(t)

	mock := &ContextCapturingMock{
		Responses: []string{
			// Step 1: tool call
			`Let me list the dir
<ACTION>{"tool":"list_dir","arguments":{"path":"."}}</ACTION>`,
			// Step 2: synthesize ready
			`Done
<SYNTHESIZE_READY>`,
			// Synthesis pass
			`{"synthesis":"The project is explored."}`,
		},
	}

	cfg := compiler.ProbeConfig{
		Goal:         "Test max tokens propagation",
		AllowedTools: []string{"list_dir", "read_file"},
		StepBudget:   5,
		CompactEvery: 3,
	}

	_, err := RunProbe(context.Background(), "task_maxtok", "probe_maxtok", cfg, mock, mock, nil)
	if err != nil {
		t.Fatalf("RunProbe failed: %v", err)
	}

	// All step calls (InferMessages) should have MaxTokensKey set
	for i, present := range mock.StepMaxTokensPresent {
		if !present {
			t.Errorf("step call %d: expected MaxTokensKey in context, but it was absent", i)
		}
	}

	// Synthesis calls (Infer) should also have MaxTokensKey set (4096 for synthesis).
	// This was added intentionally to prevent content-heavy synthesis truncation.
	for i, present := range mock.SynthMaxTokensPresent {
		if !present {
			t.Errorf("synthesis call %d: expected MaxTokensKey in synthesis context (set to 4096), but it was absent", i)
		}
	}
}

// TestProbeOutputFingerprintConvergence verifies that when consecutive tool
// outputs match existing fingerprints (diminishing information gain), the
// probe lowers minStepBudget to allow synthesis earlier rather than grinding
// through the full step budget with redundant reads.
func TestProbeOutputFingerprintConvergence(t *testing.T) {
	cleanup := setupProbeTestDB(t)
	defer cleanup()
	setupProbeTestTools(t)

	// Create files with identical content to trigger fingerprint deduplication.
	// The first 200 chars of each file's read_file output will be the same.
	tempDir := t.TempDir()
	tempDir, _ = filepath.EvalSymlinks(tempDir)
	identicalContent := "package main\n\n// This is a boilerplate file with identical content across all modules.\n// It contains standard setup code that doesn't vary between instances.\nfunc init() { /* standard init */ }\n"
	for i := 1; i <= 15; i++ {
		name := fmt.Sprintf("module_%d.go", i)
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte(identicalContent), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}
	os.Setenv("TZRO_DIR", tempDir)
	if err := tools.Init(""); err != nil {
		t.Fatalf("failed to init tools: %v", err)
	}

	// Build mock responses: 12 successful read_file calls on files with
	// identical content. After the first read, every subsequent read returns
	// a matching fingerprint. After 3 consecutive duplicates (steps 4-6),
	// the convergence check fires and lowers minStepBudget.
	responses := make([]string, 0, 15)
	for i := 1; i <= 12; i++ {
		responses = append(responses, fmt.Sprintf(
			"Reading module %d\n<ACTION>{\"tool\":\"read_file\",\"arguments\":{\"path\":\"%s\"}}</ACTION>",
			i, filepath.Join(tempDir, fmt.Sprintf("module_%d.go", i)),
		))
	}
	// After convergence lowers minStepBudget, the model signals synthesis
	responses = append(responses, "Enough data gathered\n<SYNTHESIZE_READY>")
	// Pass 2: synthesis
	responses = append(responses, `{"synthesis":"All modules contain identical boilerplate."}`)

	mock := &MockProbeInference{Responses: responses}

	cfg := compiler.ProbeConfig{
		Goal:         "Analyze all modules in the project",
		AllowedTools: []string{"read_file", "list_dir", "search_files"},
		StepBudget:   20,
		CompactEvery: 3, // convergence requires successfulToolCalls >= compactEvery*2 = 6
	}

	result, err := RunProbe(context.Background(), "task_fingerprint_test", "probe_fingerprint_1", cfg, mock, mock, nil)
	if err != nil {
		t.Fatalf("RunProbe failed: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty synthesis result")
	}

	// Without fingerprint convergence, the probe would use all 12+ steps.
	// With convergence, after 6+ successful calls and 3 consecutive duplicate
	// outputs, minStepBudget is lowered, allowing synthesis much earlier.
	// The probe should complete in significantly fewer than 12 tool steps.
	// Allow up to 12 calls total (steps + synthesis) as a generous upper bound;
	// the key assertion is that it doesn't burn through all 20 budget steps.
	if mock.CallCount > 14 {
		t.Errorf("probe made %d inference calls, expected ≤14 (fingerprint convergence should allow earlier synthesis). "+
			"Without convergence detection the probe would use all 20 budget steps.", mock.CallCount)
	}
}

func TestGoalImpliesExtraction(t *testing.T) {
	tests := []struct {
		name     string
		goal     string
		expected bool
	}{
		// Extraction goals — should return true
		{
			name:     "find the names and emails",
			goal:     "Find the names and email addresses for all Walmart leads",
			expected: true,
		},
		{
			name:     "list all matching records",
			goal:     "List all leads where account_name equals 'Walmart'",
			expected: true,
		},
		{
			name:     "extract specific columns",
			goal:     "Extract the name and email columns for each matching row",
			expected: true,
		},
		{
			name:     "return the values",
			goal:     "Return the name and email for each lead associated with Walmart",
			expected: true,
		},
		{
			name:     "show the records",
			goal:     "Show the lead details for accounts matching 'Walmart'",
			expected: true,
		},
		{
			name:     "get the matching data",
			goal:     "Get the full lead records where account_name = 'Walmart'",
			expected: true,
		},
		{
			name:     "look up leads",
			goal:     "Look up all leads by company name and return their contact info",
			expected: true,
		},
		{
			name:     "retrieve specific fields",
			goal:     "Retrieve the name column value and the email column value for each matching row",
			expected: true,
		},
		{
			name:     "for each matching lead",
			goal:     "Filter rows where account equals Walmart, for each matching lead return name and email",
			expected: true,
		},
		// Aggregation goals — should return false
		{
			name:     "count by country",
			goal:     "Count the total number of leads for each unique country and return the top 5",
			expected: false,
		},
		{
			name:     "sector breakdown",
			goal:     "Group all leads by Sector column and compute the count and percentage for each",
			expected: false,
		},
		{
			name:     "aggregate by owner",
			goal:     "Count the total leads per Account_Owner and collect distinct Lead_Source values",
			expected: false,
		},
		{
			name:     "distribution analysis",
			goal:     "Analyze the distribution of leads across CDN providers",
			expected: false,
		},
		{
			name:     "summary statistics",
			goal:     "Compute summary statistics for the dataset including total records and unique values",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := goalImpliesExtraction(tt.goal)
			if result != tt.expected {
				t.Errorf("goalImpliesExtraction(%q) = %v, want %v", tt.goal, result, tt.expected)
			}
		})
	}
}

func TestExtractionStrategySection(t *testing.T) {
	t.Run("extraction mode adds strategy block", func(t *testing.T) {
		section := extractionStrategySection(true)
		if section == "" {
			t.Fatal("expected non-empty extraction strategy section")
		}
		if !strings.Contains(section, "EXTRACTION MODE") {
			t.Error("expected section to contain 'EXTRACTION MODE' header")
		}
		if !strings.Contains(section, "PRIORITIZE queries that SELECT") {
			t.Error("expected section to contain SELECT guidance")
		}
		if !strings.Contains(section, "Do NOT waste queries on COUNT(*)") {
			t.Error("expected section to warn against COUNT(*)-only queries")
		}
	})

	t.Run("aggregation mode returns empty", func(t *testing.T) {
		section := extractionStrategySection(false)
		if section != "" {
			t.Errorf("expected empty section for aggregation mode, got %q", section)
		}
	})
}

func TestBuildAnalyzeSystemPrompt_ExtractionMode(t *testing.T) {
	goal := "Find the names and emails for all Walmart leads"
	tools := []string{"introspect_cache", "sql_cached_data"}
	cacheIds := []string{"cache_1234567890123456"}

	t.Run("extraction mode injects strategy section", func(t *testing.T) {
		prompt := buildAnalyzeSystemPrompt(goal, tools, "", cacheIds, "", true)
		if !strings.Contains(prompt, "EXTRACTION MODE") {
			t.Error("expected extraction mode strategy block in system prompt")
		}
		if !strings.Contains(prompt, "PRIORITIZE queries that SELECT") {
			t.Error("expected SELECT guidance in system prompt")
		}
	})

	t.Run("aggregation mode omits strategy section", func(t *testing.T) {
		prompt := buildAnalyzeSystemPrompt(goal, tools, "", cacheIds, "", false)
		if strings.Contains(prompt, "EXTRACTION MODE") {
			t.Error("expected no extraction mode block in aggregation system prompt")
		}
	})

	t.Run("no extraction flag defaults to aggregation", func(t *testing.T) {
		prompt := buildAnalyzeSystemPrompt(goal, tools, "", cacheIds, "")
		if strings.Contains(prompt, "EXTRACTION MODE") {
			t.Error("expected no extraction mode block when flag omitted")
		}
	})
}
