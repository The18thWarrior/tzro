package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"tzro/internal/compiler"
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

	result, err := RunProbe(context.Background(), "task_test", "probe_test_1", config, mock)
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
	_, err := RunProbe(context.Background(), "task_test", probeID, config, mock)
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

func TestRunProbe_RollingCompaction(t *testing.T) {
	cleanup := setupProbeTestDB(t)
	defer cleanup()
	setupProbeTestTools(t)

	// Set up 4 steps — compaction should trigger at step 3 (compactEvery=3)
	mock := &MockProbeInference{
		Responses: []string{
			`Step 1
<ACTION>{"tool":"list_dir","arguments":{"path":"."}}</ACTION>`,
			`Step 2
<ACTION>{"tool":"list_dir","arguments":{"path":"."}}</ACTION>`,
			// Step 3 triggers compaction. The mock needs an extra response for the compaction inference call.
			`Step 3
<ACTION>{"tool":"list_dir","arguments":{"path":"."}}</ACTION>`,
			"Compacted summary of steps 1-3", // This is the compaction inference response
			`Done
<SYNTHESIZE_READY>`,
			`{"synthesis":"Final synthesis after compaction"}`,
		},
	}

	config := compiler.ProbeConfig{
		Goal:         "Test compaction",
		AllowedTools: []string{"list_dir"},
		StepBudget:   5,
		CompactEvery: 3,
	}

	probeID := "probe_compact_test"
	result, err := RunProbe(context.Background(), "task_test", probeID, config, mock)
	if err != nil {
		t.Fatalf("RunProbe failed: %v", err)
	}

	if result != "Final synthesis after compaction" {
		t.Errorf("unexpected result: %s", result)
	}

	// Verify compaction summary was persisted
	summary, err := memory.DB.GetLatestSummary(probeID)
	if err != nil {
		t.Fatalf("GetLatestSummary failed: %v", err)
	}
	if summary.Summary != "Compacted summary of steps 1-3" {
		t.Errorf("unexpected summary: %s", summary.Summary)
	}
	if summary.StepRange != "1-3" {
		t.Errorf("unexpected step range: %s", summary.StepRange)
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

	result, err := RunProbe(context.Background(), "task_test", "probe_converge", config, mock)
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

	result, err := RunProbe(context.Background(), "task_test", "probe_budget", config, mock)
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
	_, err := RunProbe(context.Background(), "task_test", probeID, config, mock)
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
			text:     "Read the file at /Users/jp/Desktop/Repos/tzro/CONTEXT.md to understand the architecture",
			expected: "/Users/jp/Desktop/Repos/tzro/CONTEXT.md",
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
