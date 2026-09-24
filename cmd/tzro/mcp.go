package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/spf13/cobra"
	"tzro/pkg/executor"
	"tzro/pkg/store"
)

// MCPServer implements a JSON-RPC 2.0 stdio MCP server exposing tzro_execute_graph.
type MCPServer struct {
	engine    *executor.Engine
	storeDB   *store.Store
	workspace string
	reqID     atomic.Int64
}

// NewMCPServer creates a new MCP server wrapping the executor engine.
func NewMCPServer(engine *executor.Engine, s *store.Store, workspace string) *MCPServer {
	return &MCPServer{
		engine:    engine,
		storeDB:   s,
		workspace: workspace,
	}
}

// JSON-RPC 2.0 types
type jsonRPCRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      interface{}            `json:"id"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// MCP protocol types
type mcpInitializeResult struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ServerInfo      map[string]string      `json:"serverInfo"`
}

type mcpTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type mcpToolsListResult struct {
	Tools []mcpTool `json:"tools"`
}

type mcpToolCallResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ServeStdio runs the MCP server over stdin/stdout.
func (s *MCPServer) ServeStdio(ctx context.Context) error {
	reader := bufio.NewReader(os.Stdin)
	writer := os.Stdout
	var writerMu sync.Mutex

	writeResponse := func(resp interface{}) error {
		writerMu.Lock()
		defer writerMu.Unlock()
		data, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(writer, "%s\n", data)
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("reading stdin: %w", err)
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			writeResponse(jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   &rpcError{Code: -32700, Message: "Parse error"},
			})
			continue
		}

		resp := s.handleRequest(ctx, &req, writeResponse)
		if resp != nil {
			writeResponse(resp)
		}
	}
}

func (s *MCPServer) handleRequest(ctx context.Context, req *jsonRPCRequest, notify func(interface{}) error) *jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpInitializeResult{
				ProtocolVersion: "2024-11-05",
				Capabilities: map[string]interface{}{
					"tools": map[string]interface{}{},
				},
				ServerInfo: map[string]string{
					"name":    "tzro",
					"version": "3.0.0",
				},
			},
		}

	case "notifications/initialized":
		// Client acknowledgement, no response needed
		return nil

	case "tools/list":
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpToolsListResult{
				Tools: []mcpTool{
					{
						Name:        "tzro_execute_graph",
						Description: "Execute a System 1 Graph Call DAG. Returns structured results or a yield envelope.",
						InputSchema: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"graph": map[string]interface{}{
									"type":        "object",
									"description": "The System 1 Graph Call JSON object with version, task_id, nodes, and returns.",
								},
							},
							"required": []string{"graph"},
						},
					},
				},
			},
		}

	case "tools/call":
		return s.handleToolCall(ctx, req, notify)

	default:
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
		}
	}
}

func (s *MCPServer) handleToolCall(ctx context.Context, req *jsonRPCRequest, notify func(interface{}) error) *jsonRPCResponse {
	params := req.Params
	toolName, _ := params["name"].(string)

	if toolName != "tzro_execute_graph" {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpToolCallResult{
				Content: []mcpContent{{Type: "text", Text: fmt.Sprintf("Unknown tool: %s", toolName)}},
				IsError: true,
			},
		}
	}

	// Extract the graph from arguments
	arguments, _ := params["arguments"].(map[string]interface{})
	graphData, _ := arguments["graph"]

	graphJSON, err := json.Marshal(graphData)
	if err != nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpToolCallResult{
				Content: []mcpContent{{Type: "text", Text: fmt.Sprintf("Invalid graph: %v", err)}},
				IsError: true,
			},
		}
	}

	var g executor.Graph
	if err := json.Unmarshal(graphJSON, &g); err != nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpToolCallResult{
				Content: []mcpContent{{Type: "text", Text: fmt.Sprintf("Graph parse error: %v", err)}},
				IsError: true,
			},
		}
	}

	// Emit progress notification: starting
	notify(jsonRPCNotification{
		JSONRPC: "2.0",
		Method:  "notifications/progress",
		Params: map[string]interface{}{
			"progressToken": req.ID,
			"progress":      0,
			"total":         len(g.Nodes),
			"message":       fmt.Sprintf("Starting graph execution: %s (%d nodes)", g.TaskID, len(g.Nodes)),
		},
	})

	// Execute the graph
	result, err := s.engine.Execute(ctx, &g)
	if err != nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpToolCallResult{
				Content: []mcpContent{{Type: "text", Text: fmt.Sprintf("Execution error: %v", err)}},
				IsError: true,
			},
		}
	}

	// Emit progress notification: completed
	notify(jsonRPCNotification{
		JSONRPC: "2.0",
		Method:  "notifications/progress",
		Params: map[string]interface{}{
			"progressToken": req.ID,
			"progress":      len(g.Nodes),
			"total":         len(g.Nodes),
			"message":       fmt.Sprintf("Graph execution %s: %s", result.Status, g.TaskID),
		},
	})

	// If status is yielded, emit a yield envelope matching CLI behavior
	var output interface{} = result
	if result.Status == "yielded" {
		triggerNode := ""
		for id, out := range result.Outputs {
			if out.Status == "yielded" {
				triggerNode = id
				break
			}
		}
		output = executor.NewYieldEnvelope(
			result.TaskID,
			executor.YieldReasonCriteriaUnmet,
			triggerNode,
			"Execution yielded to host harness",
			result.Outputs,
		)
	}

	resultJSON, err := json.Marshal(output)
	if err != nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpToolCallResult{
				Content: []mcpContent{{Type: "text", Text: fmt.Sprintf("Result serialization error: %v", err)}},
				IsError: true,
			},
		}
	}

	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: mcpToolCallResult{
			Content: []mcpContent{{Type: "text", Text: string(resultJSON)}},
		},
	}
}

func newMCPCmd() *cobra.Command {
	var maxConcurrency int

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start the MCP stdio server for IDE integration",
		Long: `Start a Model Context Protocol (MCP) server over stdio.

The server exposes tzro_execute_graph as an MCP tool with real-time
progress notifications. Designed for IDE environments requiring modal
authorization dialogues and progress widgets.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := getDBPath()
			s, err := store.OpenStore(dbPath)
			if err != nil {
				return fmt.Errorf("opening store: %w", err)
			}
			defer s.Close()

			workspaceRoot, _ := os.Getwd()
			toolDisp := executor.NewBuiltinDispatcher(workspaceRoot, s)

			engine := executor.NewEngine(
				executor.WithMaxConcurrency(maxConcurrency),
				executor.WithToolDispatcher(toolDisp),
			)

			server := NewMCPServer(engine, s, workspaceRoot)
			return server.ServeStdio(cmd.Context())
		},
	}

	cmd.Flags().IntVar(&maxConcurrency, "concurrency", 4, "Maximum concurrent node executions")

	return cmd
}
