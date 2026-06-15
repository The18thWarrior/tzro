package channel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"tzro/internal/compiler"
	"tzro/internal/executor"
	"tzro/internal/memory"
	"tzro/internal/tools"
)

// ChannelToolHook implements executor.ExecutionHook to intercept client-side
// tool execution and dispatch it bidirectionally via the SubagentChannel.
// When the channel supports tool dispatch (RequestToolExecution), the hook
// executes the tool inline and returns ActionSkip to prevent normal execution.
// When dispatch is unsupported, it falls through (ActionContinue) so that
// the existing ClientToolHook can handle it via the polling pattern.
//
// The hook maintains a concurrent registry of taskID → SubagentChannel
// so it can be registered globally as a singleton.
type ChannelToolHook struct {
	mu       sync.RWMutex
	channels map[string]SubagentChannel
}

// GlobalChannelToolHook is the singleton hook instance registered with the executor.
var GlobalChannelToolHook = &ChannelToolHook{
	channels: make(map[string]SubagentChannel),
}

// RegisterChannel associates a SubagentChannel with a task ID.
// Call this before starting task execution.
func (h *ChannelToolHook) RegisterChannel(taskID string, ch SubagentChannel) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.channels[taskID] = ch
}

// UnregisterChannel removes the channel association for a task.
// Call this after task execution completes.
func (h *ChannelToolHook) UnregisterChannel(taskID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.channels, taskID)
}

func (h *ChannelToolHook) getChannel(taskID string) SubagentChannel {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.channels[taskID]
}

func (h *ChannelToolHook) BeforeLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (executor.HookAction, error) {
	return executor.ActionContinue, nil
}

func (h *ChannelToolHook) AfterLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (executor.HookAction, error) {
	return executor.ActionContinue, nil
}

func (h *ChannelToolHook) BeforeNode(ctx context.Context, taskID string, node *compiler.GraphNode) (executor.HookAction, error) {
	t := tools.GetTool(node.Action)
	if t == nil {
		return executor.ActionContinue, nil
	}
	_, isClientTool := t.(*tools.ClientToolAdapter)
	if !isClientTool {
		return executor.ActionContinue, nil
	}

	ch := h.getChannel(taskID)
	if ch == nil {
		return executor.ActionContinue, nil
	}

	// Build tool request
	req := ToolRequest{
		TaskID:    taskID,
		NodeID:    node.ID,
		ToolName:  node.Action,
		Arguments: make(map[string]interface{}),
		RequestID: fmt.Sprintf("%s_%s_%d", taskID, node.ID, time.Now().UnixNano()),
	}

	// Push execution to harness via channel
	resp, err := ch.RequestToolExecution(ctx, req)

	if err == ErrToolExecutionUnsupported {
		// Fall through to ClientToolHook behavior (notification + pause)
		return executor.ActionContinue, nil
	}
	if err != nil {
		return executor.ActionAbort, fmt.Errorf("channel tool execution failed: %w", err)
	}

	if resp.IsError {
		return executor.ActionAbort, fmt.Errorf("client tool returned error: %s", resp.Output)
	}

	// Inject tool output directly — no pause/resume cycle needed
	_ = memory.DB.SetNodeRawOutput(taskID, node.ID, resp.Output)
	return executor.ActionSkip, nil
}

func (h *ChannelToolHook) AfterNode(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (executor.HookAction, error) {
	return executor.ActionContinue, nil
}

func (h *ChannelToolHook) OnEdgeTraversal(ctx context.Context, taskID string, sourceNode, targetNode *compiler.GraphNode, edgeThought *memory.EdgeThought) (executor.HookAction, error) {
	return executor.ActionContinue, nil
}
