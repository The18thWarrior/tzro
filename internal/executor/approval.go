package executor

import (
	"context"
	"fmt"
	"os"
	"tzro/internal/compiler"
	"tzro/internal/memory"
	"tzro/internal/notification"
)

// McpApprovalHook implements ExecutionHook to intercept node executions
// and block them awaiting human supervisor approval.
type McpApprovalHook struct{}

func (h *McpApprovalHook) BeforeLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error) {
	return ActionContinue, nil
}

func (h *McpApprovalHook) AfterLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error) {
	return ActionContinue, nil
}

func (h *McpApprovalHook) BeforeNode(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error) {
	if !node.RequireApproval {
		return ActionContinue, nil
	}

	// Node requires manual approval. Check if it has been approved.
	notifs, err := memory.DB.GetNotifications("")
	if err == nil {
		for _, n := range notifs {
			if n.TaskID == taskID && n.TargetID == node.ID && n.Source == "human_approval" && n.Type == "approval_request" {
				if n.Status == "approved" {
					fmt.Fprintf(os.Stderr, "[Approval Hook] Node %s in Task %s has been APPROVED. Proceeding with execution.\n", node.ID, taskID)
					return ActionContinue, nil
				}
				// If it already exists but is not approved, pause execution
				fmt.Fprintf(os.Stderr, "[Approval Hook] Node %s in Task %s is awaiting approval. Pausing execution.\n", node.ID, taskID)
				return ActionPause, nil
			}
		}
	}

	// First time encountering: Create an approval request notification
	msg := fmt.Sprintf("Node %s (Action: %s) in Task %s requires manual supervisor approval to execute.", node.ID, node.Action, taskID)
	_, err = notification.Send(ctx, "human_approval", "approval_request", "Human Approval Required", msg,
		notification.WithTaskID(taskID),
		notification.WithTargetID(node.ID),
	)
	if err != nil {
		return ActionAbort, fmt.Errorf("failed to send approval notification: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[Approval Hook] Node %s in Task %s requires approval. Notification created. Pausing execution.\n", node.ID, taskID)
	return ActionPause, nil
}

func (h *McpApprovalHook) AfterNode(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (HookAction, error) {
	return ActionContinue, nil
}
