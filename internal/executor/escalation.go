package executor

import (
	"context"
	"fmt"
	"tzro/internal/compiler"
	"tzro/internal/memory"
	"tzro/internal/notification"
	"tzro/internal/tools"
)

// EscalationHook is an ExecutionHook that gates tool execution against a Proactivity Ladder ceiling.
// Tools whose ProactivityLevel exceeds the ApprovedLevel trigger a pause and escalation notification.
type EscalationHook struct {
	ApprovedLevel int // Proactivity Ladder ceiling (0-4)
}

func (h *EscalationHook) BeforeLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error) {
	return ActionContinue, nil
}

func (h *EscalationHook) AfterLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error) {
	return ActionContinue, nil
}

func (h *EscalationHook) BeforeNode(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error) {
	if node.Action == "" {
		// Synthesis nodes and other non-tool nodes don't need escalation checks
		return ActionContinue, nil
	}

	toolLevel := tools.GetProactivityLevel(node.Action)
	if toolLevel > h.ApprovedLevel {
		// Tool exceeds approved ceiling — create escalation notification and pause
		msg := fmt.Sprintf("Tool '%s' requires proactivity level L%d, but workflow approved ceiling is L%d. Approval required to proceed.",
			node.Action, toolLevel, h.ApprovedLevel)

		_, _ = notification.Send(ctx, "escalation_gate", "warning",
			fmt.Sprintf("Escalation Required: %s", node.Action),
			msg,
			notification.WithTaskID(taskID),
			notification.WithTargetID(node.ID))

		return ActionPause, nil
	}

	return ActionContinue, nil
}

func (h *EscalationHook) AfterNode(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (HookAction, error) {
	return ActionContinue, nil
}

func (h *EscalationHook) OnEdgeTraversal(ctx context.Context, taskID string, sourceNode, targetNode *compiler.GraphNode, edgeThought *memory.EdgeThought) (HookAction, error) {
	return ActionContinue, nil
}
