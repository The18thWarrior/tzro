package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// ProgressNotifier abstracts the MCP session's NotifyProgress capability.
// This seam interface allows testing without importing the MCP SDK.
type ProgressNotifier interface {
	NotifyProgress(token any, message string, progress, total float64) error
}

// ResourceUpdater abstracts the MCP server's ResourceUpdated capability.
// This seam interface allows testing without importing the MCP SDK.
type ResourceUpdater interface {
	ResourceUpdated(uri string) error
}

// ToolDispatcher is the seam interface for MCP sampling, avoiding direct SDK dependency.
type ToolDispatcher interface {
	// CreateMessage sends a sampling request to the client and blocks for the response.
	CreateMessage(ctx context.Context, systemPrompt string, userMessage string, maxTokens int64) (string, error)
}

// MCPSubagentChannel implements SubagentChannel using MCP's NotifyProgress
// when a progressToken is present, falling back to ResourceUpdated otherwise.
type MCPSubagentChannel struct {
	taskID         string
	progressToken  any               // nil → fallback mode
	notifier       ProgressNotifier  // used when progressToken is present
	updater        ResourceUpdater   // used when progressToken is absent (fallback)
	toolDispatcher ToolDispatcher    // v2: nil = no bidirectional dispatch
	nodeCount      float64           // total nodes in graph
	completed      float64           // monotonically increasing completion counter
	mu             sync.Mutex        // v3: guards mutable state
	closed         bool
}

// NewMCPSubagentChannel creates a new MCPSubagentChannel.
//   - taskID: the task this channel tracks
//   - progressToken: from _meta.progressToken (nil = fallback mode)
//   - notifier: calls NotifyProgress (can be nil in fallback mode)
//   - updater: calls ResourceUpdated (can be nil in progress mode)
//   - nodeCount: total number of nodes in the execution graph
func NewMCPSubagentChannel(taskID string, progressToken any, notifier ProgressNotifier, updater ResourceUpdater, nodeCount float64, dispatcher ToolDispatcher) *MCPSubagentChannel {
	return &MCPSubagentChannel{
		taskID:         taskID,
		progressToken:  progressToken,
		notifier:       notifier,
		updater:        updater,
		toolDispatcher: dispatcher,
		nodeCount:      nodeCount,
	}
}

func (ch *MCPSubagentChannel) EmitEvent(event ExecutionEvent) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.closed {
		return fmt.Errorf("channel closed")
	}

	// Track monotonic progress: increment on node completion
	if event.Type == EventNodeCompleted {
		ch.completed++
	}

	if ch.progressToken != nil && ch.notifier != nil {
		return ch.emitProgress(event)
	}
	if ch.updater != nil {
		return ch.emitResourceUpdated(event)
	}
	return nil
}

func (ch *MCPSubagentChannel) emitProgress(event ExecutionEvent) error {
	message := fmt.Sprintf("[%s] %s", event.Type, event.Message)
	return ch.notifier.NotifyProgress(ch.progressToken, message, ch.completed, ch.nodeCount)
}

func (ch *MCPSubagentChannel) emitResourceUpdated(event ExecutionEvent) error {
	// Fire task aggregate URI
	taskURI := fmt.Sprintf("tzro://tasks/%s/output", ch.taskID)
	if err := ch.updater.ResourceUpdated(taskURI); err != nil {
		return err
	}

	// Fire granular node URI if present
	if event.NodeID != "" {
		nodeURI := fmt.Sprintf("tzro://tasks/%s/nodes/%s/output", ch.taskID, event.NodeID)
		return ch.updater.ResourceUpdated(nodeURI)
	}
	return nil
}

func (ch *MCPSubagentChannel) RequestToolExecution(ctx context.Context, req ToolRequest) (ToolResponse, error) {
	if ch.toolDispatcher == nil {
		return ToolResponse{}, ErrToolExecutionUnsupported
	}

	reqJSON, _ := json.Marshal(req)
	result, err := ch.toolDispatcher.CreateMessage(ctx,
		"Execute the requested tool and return a JSON response with fields: requestId, output, isError.",
		string(reqJSON),
		4096,
	)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("sampling request failed: %w", err)
	}

	var resp ToolResponse
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		// Treat raw text as successful output
		return ToolResponse{RequestID: req.RequestID, Output: result}, nil
	}
	return resp, nil
}

func (ch *MCPSubagentChannel) Close() {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.closed = true
}

func (ch *MCPSubagentChannel) UpdateTotal(total float64) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.nodeCount = total
}
