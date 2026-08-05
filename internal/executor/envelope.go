package executor

import (
	"sort"
	"time"

	"tzro/internal/compiler"
	"tzro/internal/memory"
)

// dispatchRecorderKeyType is a private type for the context key to avoid collisions.
type dispatchRecorderKeyType struct{}

// DispatchRecorderKey is the context key for injecting a tool dispatch recorder
// into probe execution. The value must be a func(toolName string, args map[string]interface{}).
var DispatchRecorderKey = dispatchRecorderKeyType{}

// ToolDispatch records a single tool invocation during task execution.
// Accumulated in-memory by the ExecutionEngine and consumed by AssembleEnvelope.
type ToolDispatch struct {
	ToolName string
	Args     map[string]interface{}
}

// ExecutionEnvelope is the deterministic JSON structure assembled at task completion (ADR-0055).
// Wraps the terminal synthesis text with structured execution metadata.
// All fields are computed from graph state and tool dispatch history — no LLM generation.
type ExecutionEnvelope struct {
	Synthesis      string              `json:"synthesis"`
	TaskID         string              `json:"taskId"`
	GoalPrompt     string              `json:"goalPrompt"`
	Status         string              `json:"status"`
	ToolsUsed      []string            `json:"toolsUsed"`
	FilesRead      []string            `json:"filesRead"`
	FilesModified  []string            `json:"filesModified"`
	NodeCount      int                 `json:"nodeCount"`
	NodesCompleted int                 `json:"nodesCompleted"`
	NodesFailed    int                 `json:"nodesFailed"`
	NodesSkipped   int                 `json:"nodesSkipped"`
	DurationMs     int64               `json:"durationMs"`
	Verification   *VerificationResult `json:"verification,omitempty"` // ADR-0067: populated by Verification Gate
	Phases         []PhaseEnvelopeEntry `json:"phases,omitempty"`       // Phase Runner metadata
}

// PhaseEnvelopeEntry carries per-phase execution metadata in the Execution Envelope.
// Populated from the Phase Manifest when a node uses the Phase Runner.
type PhaseEnvelopeEntry struct {
	Name        string   `json:"name"`
	StepsUsed   int      `json:"stepsUsed"`
	ToolsCalled []string `json:"toolsCalled"`
	Backtracks  int      `json:"backtracks,omitempty"`
}

// fileReadTools are tool names whose "path" or "filepath" argument indicates a file was read.
var fileReadTools = map[string]bool{
	"read_file":    true,
	"peek_file":    true,
	"search_files": true,
}

// fileWriteTools are tool names whose "path" or "filepath" argument indicates a file was modified.
var fileWriteTools = map[string]bool{
	"write_file": true,
}

// AssembleEnvelope builds a deterministic ExecutionEnvelope from completed task data.
// This is pure Go — no LLM inference. Called by the executor after task completion.
func AssembleEnvelope(graph *compiler.ExecutionGraph, nodes []memory.NodeState, dispatches []ToolDispatch, startTime time.Time) ExecutionEnvelope {
	env := ExecutionEnvelope{
		TaskID:     graph.TaskID,
		GoalPrompt: graph.GoalPrompt,
		NodeCount:  len(graph.Nodes),
		DurationMs: time.Since(startTime).Milliseconds(),
	}

	// Count node statuses
	for _, n := range nodes {
		switch n.Status {
		case "completed":
			env.NodesCompleted++
		case "failed":
			env.NodesFailed++
		case "skipped":
			env.NodesSkipped++
		}
	}

	// Derive task-level status
	if env.NodesFailed > 0 {
		env.Status = "failed"
	} else if env.NodesCompleted == env.NodeCount {
		env.Status = "completed"
	} else {
		env.Status = "completed" // partial completion still counts
	}

	// Find effective terminal node for synthesis text.
	// Priority: terminal_synthesis > last recall > last probe > last synthesis-type
	env.Synthesis = findSynthesisText(graph, nodes)

	// Extract tools used and file paths from dispatches
	toolSet := make(map[string]bool)
	readSet := make(map[string]bool)
	writeSet := make(map[string]bool)

	for _, d := range dispatches {
		toolSet[d.ToolName] = true

		path := extractFilePath(d.Args)
		if path == "" {
			continue
		}

		if fileReadTools[d.ToolName] {
			readSet[path] = true
		}
		if fileWriteTools[d.ToolName] {
			writeSet[path] = true
		}
	}

	env.ToolsUsed = sortedKeys(toolSet)
	env.FilesRead = sortedKeys(readSet)
	env.FilesModified = sortedKeys(writeSet)

	return env
}

// PopulatePhases adds phase-level metadata from a PhaseManifest to an envelope.
func (env *ExecutionEnvelope) PopulatePhases(manifest PhaseManifest) {
	for _, phase := range manifest.Phases {
		env.Phases = append(env.Phases, PhaseEnvelopeEntry{
			Name:        phase.PhaseName,
			StepsUsed:   phase.StepsUsed,
			ToolsCalled: phase.ToolsCalled,
			Backtracks:  phase.Backtracks,
		})
	}
}

// findSynthesisText locates the effective terminal node and returns its RawOutput.
// Search order: terminal_synthesis node, then last completed recall, probe, or synthesis node.
func findSynthesisText(graph *compiler.ExecutionGraph, nodes []memory.NodeState) string {
	// Build a node type lookup from the graph
	nodeTypes := make(map[string]string)
	for _, gn := range graph.Nodes {
		nodeTypes[gn.ID] = gn.Type
	}

	// First: look for explicit terminal_synthesis
	for _, n := range nodes {
		if n.NodeID == "terminal_synthesis" && n.Status == "completed" {
			if n.RawOutput != "" {
				return n.RawOutput
			}
			return n.Output
		}
	}

	// Fallback: find the last completed node of type recall > probe > synthesis
	var lastRecall, lastProbe, lastSynthesis string
	for _, n := range nodes {
		if n.Status != "completed" {
			continue
		}
		raw := n.RawOutput
		if raw == "" {
			raw = n.Output
		}

		switch nodeTypes[n.NodeID] {
		case "recall":
			lastRecall = raw
		case "probe":
			lastProbe = raw
		case "synthesis":
			lastSynthesis = raw
		}
	}

	if lastRecall != "" {
		return lastRecall
	}
	if lastProbe != "" {
		return lastProbe
	}
	return lastSynthesis
}

// extractFilePath pulls the file path from tool arguments.
// Checks common keys: "path", "filepath", "file_path", "filePath".
func extractFilePath(args map[string]interface{}) string {
	for _, key := range []string{"path", "filepath", "file_path", "filePath"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// sortedKeys returns sorted keys from a boolean set.
func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return []string{}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// findTerminalNodeID identifies which node should receive the Execution Envelope.
// Same priority as findSynthesisText: terminal_synthesis > last recall > last probe > last synthesis.
func findTerminalNodeID(graph *compiler.ExecutionGraph, nodes []memory.NodeState) string {
	nodeTypes := make(map[string]string)
	for _, gn := range graph.Nodes {
		nodeTypes[gn.ID] = gn.Type
	}

	// First: explicit terminal_synthesis
	for _, n := range nodes {
		if n.NodeID == "terminal_synthesis" && n.Status == "completed" {
			return n.NodeID
		}
	}

	// Fallback: last completed recall > probe > synthesis
	var lastRecall, lastProbe, lastSynthesis string
	for _, n := range nodes {
		if n.Status != "completed" {
			continue
		}
		switch nodeTypes[n.NodeID] {
		case "recall":
			lastRecall = n.NodeID
		case "probe":
			lastProbe = n.NodeID
		case "synthesis":
			lastSynthesis = n.NodeID
		}
	}

	if lastRecall != "" {
		return lastRecall
	}
	if lastProbe != "" {
		return lastProbe
	}
	return lastSynthesis
}
