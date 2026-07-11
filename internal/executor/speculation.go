package executor

import (
	"context"
	"fmt"

	"tzro/internal/inference"
	"tzro/internal/tools"
)

// SpeculationMode determines how a tool call is handled during multi-branch
// Edge Thought evaluation (ADR-0045).
type SpeculationMode int

const (
	// SpecReal means the tool is executed for real during rollout evaluation.
	// Applied to tools at or below the speculation ceiling (default L0-L2).
	SpecReal SpeculationMode = iota

	// SpecImagined means the Local Model simulates the tool's output without
	// executing it. Applied to tools above the ceiling but at or below L3.
	SpecImagined

	// SpecBlocked means the tool cannot be executed during speculative evaluation.
	// Candidates requiring blocked tools are pruned. Applied to L4+ tools
	// when above the speculation ceiling.
	SpecBlocked
)

// String returns a human-readable representation of the SpeculationMode.
func (m SpeculationMode) String() string {
	switch m {
	case SpecReal:
		return "SpecReal"
	case SpecImagined:
		return "SpecImagined"
	case SpecBlocked:
		return "SpecBlocked"
	default:
		return "SpecUnknown"
	}
}

// ClassifySpeculation determines how a tool should be handled during
// multi-branch Edge Thought rollout evaluation.
//
// The Speculation Fence uses the existing Proactivity Ladder (Tool Proactivity
// Level) to classify each tool:
//
//   - Level <= ceil           → SpecReal     (execute the real tool in shadow state)
//   - Level > ceil && <= L3   → SpecImagined (Local Model simulates the output)
//   - Level > ceil && > L3    → SpecBlocked  (candidate pruned — tool cannot run speculatively)
//
// The default ceiling is L2 (Suggest), configurable via config.GetMCTSSpeculationCeil().
func ClassifySpeculation(toolName string, ceil int) SpeculationMode {
	level := tools.GetProactivityLevel(toolName)

	if level <= ceil {
		return SpecReal
	}

	if level <= tools.PLevelReversibleAction {
		return SpecImagined
	}

	return SpecBlocked
}

// ImagineToolOutput simulates a tool's output using the Local Model during
// speculative rollout evaluation. This avoids executing the real tool for
// L3 (reversible action) tools above the speculation ceiling.
//
// When the inference sidecar is available, it generates a plausible output
// based on the tool name and arguments. When unavailable, it falls back to
// a template-based simulation.
func ImagineToolOutput(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	argsStr := fmt.Sprintf("%v", args)

	// Try Local Model inference first
	req := inference.StructuredInferenceRequest{
		Messages: []inference.InferenceMessage{
			{Role: "system", Content: "You are simulating tool execution output. Generate a plausible, realistic output for the given tool call. Keep the output concise and representative of what the tool would actually return."},
			{Role: "user", Content: fmt.Sprintf("Simulate the output of tool '%s' with arguments: %s", toolName, argsStr)},
		},
		TaskID: "imagination",
	}

	if inference.GlobalWorkerModel != nil {
		result, err := inference.ExecuteRouterStructured(ctx, req)
		if err == nil && result != "" {
			return result, nil
		}
		// Fall through to template-based simulation on error
	}

	// Template-based fallback — produces a minimal plausible output
	return fmt.Sprintf("(simulated output for %s with %s)", toolName, argsStr), nil
}
