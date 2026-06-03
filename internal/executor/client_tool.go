package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"tzro/internal/compiler"
	"tzro/internal/memory"
	"tzro/internal/notification"
	"tzro/internal/tools"
)

// ClientToolHook implements ExecutionHook to intercept client-side tool execution,
// posting notifications and pausing the task for execution outcome submission.
type ClientToolHook struct{}

func (h *ClientToolHook) BeforeLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error) {
	return ActionContinue, nil
}

func (h *ClientToolHook) AfterLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error) {
	return ActionContinue, nil
}

func (h *ClientToolHook) BeforeNode(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error) {
	t := tools.GetTool(node.Action)
	if t == nil {
		return ActionContinue, nil
	}
	_, isClientTool := t.(*tools.ClientToolAdapter)
	if !isClientTool {
		return ActionContinue, nil
	}

	// Check if notification already exists
	notifs, err := memory.DB.GetNotifications("")
	if err == nil {
		for _, n := range notifs {
			if n.TaskID == taskID && n.TargetID == node.ID && n.Source == "client_tool" && n.Type == "client_tool_request" {
				if n.Status == "unread" {
					fmt.Fprintf(os.Stderr, "[Client Tool Hook] Node %s in Task %s is awaiting client execution. Pausing execution.\n", node.ID, taskID)
					return ActionPause, nil
				}
				if n.Status == "approved" {
					fmt.Fprintf(os.Stderr, "[Client Tool Hook] Node %s in Task %s has been executed by client. Proceeding.\n", node.ID, taskID)
					return ActionContinue, nil
				}
			}
		}
	}

	// Extract and interpolate arguments
	interpolatedPrompt := InterpolateVariables(node.Instructions, taskID)
	toolArguments := extractToolArguments(interpolatedPrompt)

	coerceNumericArguments(toolArguments, interpolatedPrompt)
	coerceStringArguments(toolArguments, interpolatedPrompt, node.Action)
	resolveInterpolatedArguments(toolArguments, interpolatedPrompt, node.Instructions, taskID)

	serializedArgs, err := json.Marshal(toolArguments)
	if err != nil {
		return ActionAbort, fmt.Errorf("failed to serialize client tool arguments: %w", err)
	}

	msg := fmt.Sprintf("Node %s (Action: %s) in Task %s requires client-side execution.", node.ID, node.Action, taskID)
	_, err = notification.Send(ctx, "client_tool", "client_tool_request", "Client Tool Execution Required", msg,
		notification.WithTaskID(taskID),
		notification.WithTargetID(node.ID),
		notification.WithActionPayload(string(serializedArgs)),
	)
	if err != nil {
		return ActionAbort, fmt.Errorf("failed to send client tool request notification: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[Client Tool Hook] Node %s in Task %s requires client execution. Notification created. Pausing execution.\n", node.ID, taskID)
	return ActionPause, nil
}

func (h *ClientToolHook) AfterNode(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (HookAction, error) {
	return ActionContinue, nil
}
