package executor

// YieldReason classifies why the engine yielded control to the host harness.
type YieldReason string

const (
	YieldReasonCriteriaUnmet YieldReason = "acceptance_criteria_unmet"
	YieldReasonBlocked       YieldReason = "dependency_blocked"
	YieldReasonToolFailure   YieldReason = "tool_failure"
	YieldReasonTimeout       YieldReason = "timeout"
)

// YieldEnvelope is the structured payload emitted when the engine yields
// instead of completing. It preserves completed node results and provides
// diagnostic context for the host agent to re-plan.
type YieldEnvelope struct {
	Status            string                 `json:"status"` // always "yielded"
	TaskID            string                 `json:"task_id"`
	Reason            YieldReason            `json:"reason"`
	TriggerNode       string                 `json:"trigger_node"`
	Summary           string                 `json:"summary"`
	DiagnosticContext map[string]interface{} `json:"diagnostic_context"`
	CompletedNodes    []string               `json:"completed_nodes"`
}

// NewYieldEnvelope creates a YieldEnvelope from the current execution state.
func NewYieldEnvelope(taskID string, reason YieldReason, triggerNodeID string, summary string, outputs map[string]NodeOutput) *YieldEnvelope {
	var completedNodes []string
	diagnosticContext := make(map[string]interface{})

	for id, out := range outputs {
		if out.Status == "completed" {
			completedNodes = append(completedNodes, id)
		}
	}

	// Include the trigger node's output in diagnostics
	if triggerOut, ok := outputs[triggerNodeID]; ok {
		diagnosticContext["trigger_output"] = triggerOut
	}

	return &YieldEnvelope{
		Status:            "yielded",
		TaskID:            taskID,
		Reason:            reason,
		TriggerNode:       triggerNodeID,
		Summary:           summary,
		DiagnosticContext: diagnosticContext,
		CompletedNodes:    completedNodes,
	}
}
