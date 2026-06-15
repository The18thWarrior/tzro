package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tzro/internal/channel"
	"tzro/internal/stream"
)

// sessionProgressNotifier adapts *mcp.ServerSession to the channel.ProgressNotifier interface.
type sessionProgressNotifier struct {
	session *mcp.ServerSession
}

func (s *sessionProgressNotifier) NotifyProgress(token any, message string, progress, total float64) error {
	return s.session.NotifyProgress(context.Background(), &mcp.ProgressNotificationParams{
		ProgressToken: token,
		Message:       message,
		Progress:      progress,
		Total:         total,
	})
}

// serverResourceUpdater adapts *mcp.Server to the channel.ResourceUpdater interface.
type serverResourceUpdater struct {
	server *mcp.Server
}

func (s *serverResourceUpdater) ResourceUpdated(uri string) error {
	return s.server.ResourceUpdated(context.Background(), &mcp.ResourceUpdatedNotificationParams{
		URI: uri,
	})
}

// sessionToolDispatcher adapts *mcp.ServerSession to the channel.ToolDispatcher interface
// using the MCP sampling primitive (CreateMessage).
type sessionToolDispatcher struct {
	session *mcp.ServerSession
}

func (d *sessionToolDispatcher) CreateMessage(ctx context.Context, systemPrompt, userMessage string, maxTokens int64) (string, error) {
	result, err := d.session.CreateMessage(ctx, &mcp.CreateMessageParams{
		SystemPrompt: systemPrompt,
		Messages: []*mcp.SamplingMessage{{
		Role:    "user",
			Content: &mcp.TextContent{Text: userMessage},
		}},
		MaxTokens: maxTokens,
	})
	if err != nil {
		return "", err
	}
	if tc, ok := result.Content.(*mcp.TextContent); ok {
		return tc.Text, nil
	}
	return "", fmt.Errorf("unexpected content type from sampling response")
}

// startSubagentChannel creates an MCPSubagentChannel from the MCP request context
// and starts a bridge goroutine that forwards events from the GlobalBus.
// Returns the channel (caller must defer ch.Close()) or nil if the channel could
// not be created (non-fatal — task execution still proceeds without live events).
func startSubagentChannel(req *mcp.CallToolRequest, server *mcp.Server, taskID string, nodeCount float64) *channel.MCPSubagentChannel {
	progressToken := req.Params.GetProgressToken()

	var notifier channel.ProgressNotifier
	var updater channel.ResourceUpdater

	if progressToken != nil && req.Session != nil {
		notifier = &sessionProgressNotifier{session: req.Session}
	} else {
		// Fallback mode: use ResourceUpdated
		updater = &serverResourceUpdater{server: server}
	}

	// v2: Wire tool dispatcher for bidirectional dispatch via MCP sampling.
	// The dispatcher is wired whenever a session exists. If the client doesn't
	// support sampling, CreateMessage will error and ChannelToolHook falls through
	// to the ClientToolHook polling pattern.
	var dispatcher channel.ToolDispatcher
	if req.Session != nil {
		dispatcher = &sessionToolDispatcher{session: req.Session}
	}

	ch := channel.NewMCPSubagentChannel(taskID, progressToken, notifier, updater, nodeCount, dispatcher)

	// Start bridge goroutine — forwards GlobalBus events → channel
	go func() {
		channel.Bridge(ch, taskID, stream.GlobalBus)
	}()

	// Log channel creation for debugging
	if progressToken != nil {
		fmt.Printf("[tzro-mcp] SubagentChannel created for task %s (progress mode, token=%v", taskID, progressToken)
	} else {
		fmt.Printf("[tzro-mcp] SubagentChannel created for task %s (fallback mode", taskID)
	}
	if dispatcher != nil {
		fmt.Printf(", sampling=enabled)\n")
	} else {
		fmt.Printf(")\n")
	}

	return ch
}
