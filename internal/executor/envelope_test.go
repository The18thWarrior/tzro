package executor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tzro/internal/compiler"
	"tzro/internal/memory"
)

func TestAssembleEnvelope_SynthesisNode(t *testing.T) {
	// A completed graph with a terminal_synthesis node should produce
	// an envelope with the synthesis text and correct node counts.
	graph := &compiler.ExecutionGraph{
		TaskID:     "task-env-1",
		GoalPrompt: "Research AI orchestration trends",
		Nodes: []compiler.GraphNode{
			{ID: "explore", Type: "probe"},
			{ID: "terminal_synthesis", Type: "synthesis"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "explore", TargetID: "terminal_synthesis"},
		},
		CreatedAt: time.Now().Add(-5 * time.Second).Unix(),
	}

	nodes := []memory.NodeState{
		{TaskID: "task-env-1", NodeID: "explore", Status: "completed", Output: "[local] Exploration done", RawOutput: "Exploration done"},
		{TaskID: "task-env-1", NodeID: "terminal_synthesis", Status: "completed", Output: "[local] Final summary of findings", RawOutput: "Final summary of findings"},
	}

	dispatches := []ToolDispatch{
		{ToolName: "web_search", Args: map[string]interface{}{"query": "AI orchestration"}},
		{ToolName: "save_memory", Args: map[string]interface{}{"content": "key findings"}},
	}

	startTime := time.Now().Add(-5 * time.Second)
	env := AssembleEnvelope(graph, nodes, dispatches, startTime)

	if env.Synthesis != "Final summary of findings" {
		t.Errorf("expected synthesis from terminal_synthesis node, got %q", env.Synthesis)
	}
	if env.TaskID != "task-env-1" {
		t.Errorf("expected taskId task-env-1, got %q", env.TaskID)
	}
	if env.GoalPrompt != "Research AI orchestration trends" {
		t.Errorf("expected goal prompt, got %q", env.GoalPrompt)
	}
	if env.Status != "completed" {
		t.Errorf("expected status completed, got %q", env.Status)
	}
	if env.NodeCount != 2 {
		t.Errorf("expected 2 nodes, got %d", env.NodeCount)
	}
	if env.NodesCompleted != 2 {
		t.Errorf("expected 2 completed, got %d", env.NodesCompleted)
	}
	if env.NodesFailed != 0 {
		t.Errorf("expected 0 failed, got %d", env.NodesFailed)
	}
	if env.DurationMs <= 0 {
		t.Errorf("expected positive duration, got %d", env.DurationMs)
	}
	if len(env.ToolsUsed) != 2 {
		t.Errorf("expected 2 tools, got %d: %v", len(env.ToolsUsed), env.ToolsUsed)
	}

	// Envelope should be valid JSON when marshalled
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("failed to marshal envelope: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("envelope is not valid JSON: %v", err)
	}
	if _, ok := parsed["synthesis"]; !ok {
		t.Error("marshalled envelope missing 'synthesis' key")
	}
}

func TestAssembleEnvelope_ExtractsFilePaths(t *testing.T) {
	// Tool dispatches with file-related tools should populate filesRead and filesModified.
	graph := &compiler.ExecutionGraph{
		TaskID:     "task-files-1",
		GoalPrompt: "Update config files",
		Nodes: []compiler.GraphNode{
			{ID: "step1", Type: "action", Action: "read_file"},
			{ID: "step2", Type: "action", Action: "write_file"},
			{ID: "terminal_synthesis", Type: "synthesis"},
		},
		CreatedAt: time.Now().Unix(),
	}

	nodes := []memory.NodeState{
		{TaskID: "task-files-1", NodeID: "step1", Status: "completed", RawOutput: "file contents"},
		{TaskID: "task-files-1", NodeID: "step2", Status: "completed", RawOutput: "written ok"},
		{TaskID: "task-files-1", NodeID: "terminal_synthesis", Status: "completed", RawOutput: "Changes applied"},
	}

	dispatches := []ToolDispatch{
		{ToolName: "read_file", Args: map[string]interface{}{"path": "/src/config.go"}},
		{ToolName: "read_file", Args: map[string]interface{}{"path": "/src/main.go"}},
		{ToolName: "peek_file", Args: map[string]interface{}{"path": "/src/config.go"}}, // duplicate path
		{ToolName: "write_file", Args: map[string]interface{}{"path": "/src/config.go"}},
		{ToolName: "search_files", Args: map[string]interface{}{"path": "/src"}},
		{ToolName: "web_search", Args: map[string]interface{}{"query": "golang"}}, // no path
	}

	env := AssembleEnvelope(graph, nodes, dispatches, time.Now())

	// filesRead should be deduplicated
	expectedReads := []string{"/src", "/src/config.go", "/src/main.go"}
	if len(env.FilesRead) != len(expectedReads) {
		t.Fatalf("expected %d filesRead, got %d: %v", len(expectedReads), len(env.FilesRead), env.FilesRead)
	}
	for i, f := range expectedReads {
		if env.FilesRead[i] != f {
			t.Errorf("filesRead[%d] = %q, want %q", i, env.FilesRead[i], f)
		}
	}

	// filesModified should contain only write_file paths
	if len(env.FilesModified) != 1 || env.FilesModified[0] != "/src/config.go" {
		t.Errorf("expected filesModified=[/src/config.go], got %v", env.FilesModified)
	}

	// toolsUsed should be deduplicated and sorted
	if len(env.ToolsUsed) != 5 {
		t.Errorf("expected 5 unique tools, got %d: %v", len(env.ToolsUsed), env.ToolsUsed)
	}
}

func TestAssembleEnvelope_ProbeOnlyGraph(t *testing.T) {
	// Graphs without terminal_synthesis should source synthesis from probe node output.
	graph := &compiler.ExecutionGraph{
		TaskID:     "task-probe-only",
		GoalPrompt: "Explore codebase architecture",
		Nodes: []compiler.GraphNode{
			{ID: "explore_code", Type: "probe"},
		},
		CreatedAt: time.Now().Unix(),
	}

	nodes := []memory.NodeState{
		{TaskID: "task-probe-only", NodeID: "explore_code", Status: "completed", RawOutput: "Architecture overview: modular design with 5 packages"},
	}

	env := AssembleEnvelope(graph, nodes, nil, time.Now())

	if env.Synthesis != "Architecture overview: modular design with 5 packages" {
		t.Errorf("expected synthesis from probe node, got %q", env.Synthesis)
	}
	if env.Status != "completed" {
		t.Errorf("expected completed, got %q", env.Status)
	}
	if env.NodeCount != 1 {
		t.Errorf("expected 1 node, got %d", env.NodeCount)
	}
	// Empty dispatches → empty tool/file lists (not nil)
	if env.ToolsUsed == nil || len(env.ToolsUsed) != 0 {
		t.Errorf("expected empty toolsUsed slice, got %v", env.ToolsUsed)
	}
	if env.FilesRead == nil || len(env.FilesRead) != 0 {
		t.Errorf("expected empty filesRead slice, got %v", env.FilesRead)
	}
}

func TestAssembleEnvelope_FailedNodes(t *testing.T) {
	// If any nodes failed, the envelope status should be "failed".
	graph := &compiler.ExecutionGraph{
		TaskID:     "task-fail",
		GoalPrompt: "Failing task",
		Nodes: []compiler.GraphNode{
			{ID: "step1", Type: "action"},
			{ID: "step2", Type: "action"},
			{ID: "terminal_synthesis", Type: "synthesis"},
		},
		CreatedAt: time.Now().Unix(),
	}

	nodes := []memory.NodeState{
		{TaskID: "task-fail", NodeID: "step1", Status: "completed", RawOutput: "ok"},
		{TaskID: "task-fail", NodeID: "step2", Status: "failed", RawOutput: "error occurred"},
		{TaskID: "task-fail", NodeID: "terminal_synthesis", Status: "completed", RawOutput: "Partial results"},
	}

	env := AssembleEnvelope(graph, nodes, nil, time.Now())

	if env.Status != "failed" {
		t.Errorf("expected failed status when nodes failed, got %q", env.Status)
	}
	if env.NodesFailed != 1 {
		t.Errorf("expected 1 failed, got %d", env.NodesFailed)
	}
	if env.NodesCompleted != 2 {
		t.Errorf("expected 2 completed, got %d", env.NodesCompleted)
	}
}

func TestExecutionEngine_DispatchAccumulator(t *testing.T) {
	// RecordDispatch should accumulate tool dispatches per task.
	// DrainDispatches should return all dispatches for a task and clear the map.
	engine := &ExecutionEngine{}

	// Record dispatches for two different tasks
	engine.RecordDispatch("task-a", "read_file", map[string]interface{}{"path": "/foo.go"})
	engine.RecordDispatch("task-a", "web_search", map[string]interface{}{"query": "golang"})
	engine.RecordDispatch("task-b", "write_file", map[string]interface{}{"path": "/bar.go"})

	// Drain task-a
	dispatches := engine.DrainDispatches("task-a")
	if len(dispatches) != 2 {
		t.Fatalf("expected 2 dispatches for task-a, got %d", len(dispatches))
	}
	if dispatches[0].ToolName != "read_file" {
		t.Errorf("dispatches[0].ToolName = %q, want read_file", dispatches[0].ToolName)
	}
	if dispatches[1].ToolName != "web_search" {
		t.Errorf("dispatches[1].ToolName = %q, want web_search", dispatches[1].ToolName)
	}

	// Drain again — should be empty (consumed)
	again := engine.DrainDispatches("task-a")
	if len(again) != 0 {
		t.Errorf("expected 0 dispatches after drain, got %d", len(again))
	}

	// task-b should be unaffected
	bDispatches := engine.DrainDispatches("task-b")
	if len(bDispatches) != 1 {
		t.Fatalf("expected 1 dispatch for task-b, got %d", len(bDispatches))
	}
	if bDispatches[0].ToolName != "write_file" {
		t.Errorf("bDispatches[0].ToolName = %q, want write_file", bDispatches[0].ToolName)
	}

	// Drain nonexistent task — should return empty
	empty := engine.DrainDispatches("nonexistent")
	if len(empty) != 0 {
		t.Errorf("expected 0 dispatches for nonexistent task, got %d", len(empty))
	}
}

// --- Slice 9: Phase Runner metadata in Execution Envelope ---

func TestExecutionEnvelope_PhasesSection(t *testing.T) {
	// Build a simple envelope
	graph := &compiler.ExecutionGraph{
		TaskID:     "task-phase-1",
		GoalPrompt: "Explore project",
		Nodes: []compiler.GraphNode{
			{ID: "explore", Type: "probe"},
			{ID: "terminal_synthesis", Type: "synthesis"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "explore", TargetID: "terminal_synthesis"},
		},
	}

	nodes := []memory.NodeState{
		{TaskID: "task-phase-1", NodeID: "explore", Status: "completed", RawOutput: "Explored"},
		{TaskID: "task-phase-1", NodeID: "terminal_synthesis", Status: "completed", RawOutput: "Final synthesis"},
	}

	env := AssembleEnvelope(graph, nodes, nil, time.Now().Add(-2*time.Second))

	// Populate phases from a Phase Manifest
	manifest := PhaseManifest{
		Phases: []PhaseResult{
			{PhaseName: "orient", StepsUsed: 2, ToolsCalled: []string{"list_dir"}, Backtracks: 0},
			{PhaseName: "discover", StepsUsed: 4, ToolsCalled: []string{"read_file", "search_files"}, Backtracks: 0},
			{PhaseName: "deep_read", StepsUsed: 6, ToolsCalled: []string{"read_file", "read_file", "read_file"}, Backtracks: 1},
			{PhaseName: "synthesize", StepsUsed: 1, ToolsCalled: []string{}, Backtracks: 0},
		},
		TotalStepsUsed:  13,
		TotalBacktracks: 1,
	}
	env.PopulatePhases(manifest)

	// Verify phases array
	if len(env.Phases) != 4 {
		t.Fatalf("expected 4 phases, got %d", len(env.Phases))
	}

	if env.Phases[0].Name != "orient" {
		t.Errorf("phase 0 name: expected 'orient', got %q", env.Phases[0].Name)
	}
	if env.Phases[2].StepsUsed != 6 {
		t.Errorf("phase 2 steps: expected 6, got %d", env.Phases[2].StepsUsed)
	}
	if env.Phases[2].Backtracks != 1 {
		t.Errorf("phase 2 backtracks: expected 1, got %d", env.Phases[2].Backtracks)
	}

	// Verify JSON serialization includes phases
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	jsonStr := string(data)
	if !strings.Contains(jsonStr, `"phases"`) {
		t.Error("envelope JSON missing 'phases' key")
	}
	if !strings.Contains(jsonStr, `"orient"`) {
		t.Error("envelope JSON missing phase name 'orient'")
	}
	if !strings.Contains(jsonStr, `"deep_read"`) {
		t.Error("envelope JSON missing phase name 'deep_read'")
	}
}


