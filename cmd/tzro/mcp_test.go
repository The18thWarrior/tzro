package main

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"tzro/pkg/executor"
)

func TestMCP_Handshake(t *testing.T) {
	server := newTestMCPServer(t)

	// Initialize
	initResp := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
		},
	}, noopNotify)

	if initResp == nil {
		t.Fatal("expected initialize response")
	}
	if initResp.Error != nil {
		t.Fatalf("initialize failed: %s", initResp.Error.Message)
	}

	resultMap, ok := initResp.Result.(mcpInitializeResult)
	if !ok {
		t.Fatalf("expected mcpInitializeResult, got %T", initResp.Result)
	}
	if resultMap.ServerInfo["name"] != "tzro" {
		t.Errorf("expected server name 'tzro', got %v", resultMap.ServerInfo["name"])
	}

	// tools/list
	toolsResp := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(2),
		Method:  "tools/list",
	}, noopNotify)

	if toolsResp == nil {
		t.Fatal("expected tools/list response")
	}
	if toolsResp.Error != nil {
		t.Fatalf("tools/list failed: %s", toolsResp.Error.Message)
	}

	toolsResult, ok := toolsResp.Result.(mcpToolsListResult)
	if !ok {
		t.Fatalf("expected mcpToolsListResult, got %T", toolsResp.Result)
	}

	found := false
	for _, tool := range toolsResult.Tools {
		if tool.Name == "tzro_execute_graph" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected tzro_execute_graph in tools list")
	}
}

func TestMCP_ExecuteGraph(t *testing.T) {
	server := newTestMCPServer(t)

	graphObj := map[string]interface{}{
		"version": "3.0",
		"task_id": "mcp-exec-test",
		"nodes": []interface{}{
			map[string]interface{}{
				"id":   "echo_test",
				"type": "tool",
				"tool": "bash",
				"args": map[string]interface{}{"command": "echo mcp_works"},
			},
		},
	}

	resp := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(3),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_execute_graph",
			"arguments": map[string]interface{}{
				"graph": graphObj,
			},
		},
	}, noopNotify)

	if resp == nil {
		t.Fatal("expected response to tools/call")
	}
	if resp.Error != nil {
		t.Fatalf("tools/call failed: %s", resp.Error.Message)
	}

	result, ok := resp.Result.(mcpToolCallResult)
	if !ok {
		t.Fatalf("expected mcpToolCallResult, got %T", resp.Result)
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content in tool call result")
	}

	// Parse the execution result from content text
	var execResult map[string]interface{}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &execResult); err != nil {
		t.Fatalf("failed to parse execution result: %v", err)
	}

	if execResult["status"] != "completed" {
		t.Errorf("expected status 'completed', got %v", execResult["status"])
	}
	if execResult["task_id"] != "mcp-exec-test" {
		t.Errorf("expected task_id 'mcp-exec-test', got %v", execResult["task_id"])
	}
}

func TestMCP_ProgressNotifications(t *testing.T) {
	server := newTestMCPServer(t)

	graphObj := map[string]interface{}{
		"version": "3.0",
		"task_id": "progress-test",
		"nodes": []interface{}{
			map[string]interface{}{
				"id":   "step1",
				"type": "tool",
				"tool": "bash",
				"args": map[string]interface{}{"command": "echo step1"},
			},
		},
	}

	var notifications []jsonRPCNotification
	collectNotify := func(n interface{}) error {
		if notif, ok := n.(jsonRPCNotification); ok {
			notifications = append(notifications, notif)
		}
		return nil
	}

	resp := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(5),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_execute_graph",
			"arguments": map[string]interface{}{
				"graph": graphObj,
			},
		},
	}, collectNotify)

	if resp == nil {
		t.Fatal("expected response")
	}

	if len(notifications) < 2 {
		t.Errorf("expected at least 2 progress notifications (start + end), got %d", len(notifications))
	}

	// Verify notifications are progress type
	for _, n := range notifications {
		if n.Method != "notifications/progress" {
			t.Errorf("expected notifications/progress, got %q", n.Method)
		}
	}
}

func TestMCP_UnknownMethod(t *testing.T) {
	server := newTestMCPServer(t)

	resp := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(10),
		Method:  "nonexistent/method",
	}, noopNotify)

	if resp == nil {
		t.Fatal("expected error response")
	}
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected error code -32601, got %d", resp.Error.Code)
	}
}

func TestMCP_StdioProtocol(t *testing.T) {
	// Test the full ServeStdio loop with piped input
	server := newTestMCPServer(t)

	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

	// We test the handleRequest path directly since ServeStdio uses os.Stdin/Stdout
	reader := bufio.NewScanner(strings.NewReader(input))
	var responses []*jsonRPCResponse

	for reader.Scan() {
		var req jsonRPCRequest
		if err := json.Unmarshal(reader.Bytes(), &req); err != nil {
			t.Fatalf("failed to parse request: %v", err)
		}
		resp := server.handleRequest(context.Background(), &req, noopNotify)
		if resp != nil {
			responses = append(responses, resp)
		}
	}

	if len(responses) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(responses))
	}
}

// Test helpers

func newTestMCPServer(t *testing.T) *MCPServer {
	t.Helper()
	engine := executor.NewEngine(executor.WithMaxConcurrency(2))
	return NewMCPServer(engine, nil, t.TempDir())
}

func noopNotify(n interface{}) error {
	return nil
}
