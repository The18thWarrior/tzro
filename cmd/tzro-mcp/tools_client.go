package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tzro/internal/cache"
	"tzro/internal/compiler"
	internalmcp "tzro/internal/mcp"
	"tzro/internal/memory"
	"tzro/internal/stream"
	"tzro/internal/tools"
)

// ---------------------------------------------------------------------------
// Client-side tool registration and dispatch
// ---------------------------------------------------------------------------

// ClientToolInput represents a single client-side tool definition.
type ClientToolInput struct {
	Name        string                 `json:"name" jsonschema:"required,Name of the client tool"`
	Description string                 `json:"description" jsonschema:"required,Description of the client-side tool"`
	InputSchema map[string]interface{} `json:"inputSchema" jsonschema:"required,JSON Schema parameters of the tool"`
}

// TzroRegisterClientToolsArgs defines inputs for tzro_register_client_tools
type TzroRegisterClientToolsArgs struct {
	Tools []ClientToolInput `json:"tools" jsonschema:"required,List of client-side tools to register"`
}

func handleTzroRegisterClientTools(ctx context.Context, req *mcp.CallToolRequest, args TzroRegisterClientToolsArgs) (*mcp.CallToolResult, any, error) {
	var registered []string
	for _, t := range args.Tools {
		gbnfSchema, err := internalmcp.GetGBNFSchema(t.InputSchema)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to wrap schema for tool %s: %w", t.Name, err)
		}
		adapter := &tools.ClientToolAdapter{
			NameVal:        t.Name,
			DescriptionVal: t.Description,
			SchemaVal:      gbnfSchema,
		}
		tools.Register(adapter)
		registered = append(registered, t.Name)
	}

	respMap := map[string]interface{}{
		"status":          "success",
		"registeredTools": registered,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroClientToolListArgs defines inputs for tzro_client_tool_list
type TzroClientToolListArgs struct{}

func handleTzroClientToolList(ctx context.Context, req *mcp.CallToolRequest, args TzroClientToolListArgs) (*mcp.CallToolResult, any, error) {
	notifs, err := memory.DB.GetNotifications("unread")
	if err != nil {
		return nil, nil, err
	}

	type ClientToolResponse struct {
		RequestID string                 `json:"requestId"`
		TaskID    string                 `json:"taskId,omitempty"`
		NodeID    string                 `json:"nodeId"`
		ToolName  string                 `json:"toolName"`
		Arguments map[string]interface{} `json:"arguments"`
	}

	var responseList []ClientToolResponse
	db := memory.DB.RawDB()
	graphs := make(map[string]*compiler.ExecutionGraph)

	for _, n := range notifs {
		if n.Source == "client_tool" && n.Type == "client_tool_request" {
			var arguments map[string]interface{}
			if n.ActionPayload != "" {
				_ = json.Unmarshal([]byte(n.ActionPayload), &arguments)
			}

			toolName := ""
			if db != nil {
				g, ok := graphs[n.TaskID]
				if !ok {
					var graphBytes string
					err := db.QueryRow("SELECT raw_payload FROM disk_cache WHERE cache_id = ?", "graph_"+n.TaskID).Scan(&graphBytes)
					if err == nil {
						var graph compiler.ExecutionGraph
						if json.Unmarshal([]byte(graphBytes), &graph) == nil {
							g = &graph
							graphs[n.TaskID] = g
						}
					}
				}
				if g != nil {
					for _, node := range g.Nodes {
						if node.ID == n.TargetID {
							toolName = node.Action
							break
						}
					}
				}
			}

			responseList = append(responseList, ClientToolResponse{
				RequestID: n.ID,
				TaskID:    n.TaskID,
				NodeID:    n.TargetID,
				ToolName:  toolName,
				Arguments: arguments,
			})
		}
	}

	respBytes, _ := json.MarshalIndent(responseList, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// TzroClientToolSubmitArgs defines inputs for tzro_client_tool_submit
type TzroClientToolSubmitArgs struct {
	TaskID    string `json:"taskId,omitempty"`
	NodeID    string `json:"nodeId,omitempty"`
	Output    string `json:"output,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

func handleTzroClientToolSubmit(ctx context.Context, req *mcp.CallToolRequest, args TzroClientToolSubmitArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.RequestID) == "" && (strings.TrimSpace(args.TaskID) == "" || strings.TrimSpace(args.NodeID) == "") {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "must provide either requestId or both taskId and nodeId"}`},
			},
			IsError: true,
		}, nil, nil
	}

	notifs, err := memory.DB.GetNotifications("unread")
	if err != nil {
		return nil, nil, err
	}
	var target *memory.DurableNotification
	if args.RequestID != "" {
		for _, n := range notifs {
			if n.ID == args.RequestID && n.Source == "client_tool" && n.Type == "client_tool_request" {
				target = &n
				break
			}
		}
	} else {
		for _, n := range notifs {
			if n.TaskID == args.TaskID && n.TargetID == args.NodeID && n.Source == "client_tool" && n.Type == "client_tool_request" {
				target = &n
				break
			}
		}
	}

	var taskID string
	var nodeID string
	if target != nil {
		taskID = target.TaskID
		nodeID = target.TargetID
	} else {
		taskID = args.TaskID
		nodeID = args.NodeID
	}

	output := args.Output
	if output == "" && args.Result != "" {
		output = args.Result
	}
	if output == "" && args.Error != "" {
		output = fmt.Sprintf("Error: %s", args.Error)
	}

	if target == nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf(`{"error": "unread client tool request for task '%s' node '%s' (requestId '%s') not found"}`, taskID, nodeID, args.RequestID)},
			},
			IsError: true,
		}, nil, nil
	}

	// 1. Update notification status
	status := "approved"
	if args.Error != "" {
		status = "failed"
	}
	if err := memory.DB.UpdateNotificationStatus(target.ID, status); err != nil {
		return nil, nil, err
	}

	// 2. Perform caching/compaction and write completed/failed node state
	displayOutput := output
	compactedOutput, cacheID, err := cache.Process(ctx, output, "")
	if err == nil && cacheID != "" {
		displayOutput = compactedOutput
	}

	nodeStatus := fmt.Sprintf("[Client Execution] %s", displayOutput)
	if args.Error != "" {
		_ = memory.DB.SetNodeState(taskID, nodeID, "failed", nodeStatus)
	} else {
		_ = memory.DB.SetNodeState(taskID, nodeID, "completed", nodeStatus)
	}
	_ = memory.DB.SetNodeRawOutput(taskID, nodeID, output)

	// Publish node completed event & stream update to keep UI in sync
	stream.GlobalBus.Publish(stream.StreamChunk{
		Source:  "executor",
		TaskID:  taskID,
		NodeID:  nodeID,
		Type:    "event",
		Content: fmt.Sprintf("Node %s completed client-side execution", nodeID),
	})

	statusString := "completed"
	if args.Error != "" {
		statusString = "failed"
	}
	if statePayload, err := json.Marshal(map[string]string{"status": statusString, "output": nodeStatus}); err == nil {
		stream.GlobalBus.Publish(stream.StreamChunk{
			Source:  "executor",
			TaskID:  taskID,
			NodeID:  nodeID,
			Type:    "node_state",
			Content: string(statePayload),
		})
	}

	// 3. Trigger task resume in the background
	go func() {
		_ = runResumeTask(context.Background(), taskID)
	}()

	respMap := map[string]interface{}{
		"status":  "success",
		"message": fmt.Sprintf("Client tool output submitted for node %s and task resume triggered.", nodeID),
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}
