package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/pkg/executor"
	"tzro/pkg/store"
)

func TestMCP_ToolsList_Issue08(t *testing.T) {
	tempDir := t.TempDir()
	engine := executor.NewEngine()
	s, _ := store.OpenStore(":memory:")
	defer s.Close()
	server := NewMCPServer(engine, s, tempDir)

	// 1. Initialize with legacy protocol 2024-11-05
	initResp := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
		},
	}, noopNotify)

	initResult := initResp.Result.(mcpInitializeResult)
	if initResult.ProtocolVersion != "2024-11-05" {
		t.Errorf("expected 2024-11-05, got %s", initResult.ProtocolVersion)
	}

	// List tools under legacy
	listResp := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(2),
		Method:  "tools/list",
	}, noopNotify)

	listResult := listResp.Result.(mcpToolsListResult)
	toolMap := make(map[string]mcpTool)
	for _, tool := range listResult.Tools {
		toolMap[tool.Name] = tool
	}

	for _, name := range []string{"tzro_execute_graph", "tzro_get_context_pack", "tzro_get_impact_report"} {
		if _, ok := toolMap[name]; !ok {
			t.Errorf("missing tool %s in tools/list", name)
		}
	}
	if toolMap["tzro_get_context_pack"].OutputSchema != nil {
		t.Errorf("expected outputSchema omitted under 2024-11-05")
	}

	// 2. Initialize with 2025-06-18 protocol
	initResp2 := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(3),
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-06-18",
		},
	}, noopNotify)

	initResult2 := initResp2.Result.(mcpInitializeResult)
	if initResult2.ProtocolVersion != "2025-06-18" {
		t.Errorf("expected negotiated 2025-06-18, got %s", initResult2.ProtocolVersion)
	}

	listResp2 := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(4),
		Method:  "tools/list",
	}, noopNotify)

	listResult2 := listResp2.Result.(mcpToolsListResult)
	for _, tool := range listResult2.Tools {
		if tool.Name == "tzro_get_context_pack" || tool.Name == "tzro_get_impact_report" {
			if tool.OutputSchema == nil {
				t.Errorf("expected outputSchema present for %s under 2025-06-18", tool.Name)
			}
		}
	}
}

func TestMCP_GetContextPack_LegacyAndStructured(t *testing.T) {
	tempDir := t.TempDir()

	code := `package auth

// VerifyCredentials validates user login credentials.
func VerifyCredentials(user, pass string) bool {
	return user != "" && pass != ""
}
`
	_ = os.WriteFile(filepath.Join(tempDir, "auth.go"), []byte(code), 0644)

	engine := executor.NewEngine()
	s, _ := store.OpenStore(":memory:")
	defer s.Close()
	server := NewMCPServer(engine, s, tempDir)

	// 1. Call under legacy protocol 2024-11-05
	server.protocolVersion = "2024-11-05"
	respLegacy := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(10),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_get_context_pack",
			"arguments": map[string]interface{}{
				"symbol": "VerifyCredentials",
				"format": "markdown",
			},
		},
	}, noopNotify)

	if respLegacy == nil || respLegacy.Error != nil {
		t.Fatalf("call failed: %v", respLegacy)
	}
	resL, ok := respLegacy.Result.(mcpToolCallResult)
	if !ok || resL.IsError {
		t.Fatalf("tool call error: %+v", resL)
	}
	if len(resL.Content) == 0 || !strings.Contains(resL.Content[0].Text, "VerifyCredentials") {
		t.Errorf("missing symbol in content: %v", resL.Content)
	}
	if resL.StructuredContent != nil {
		t.Errorf("expected StructuredContent nil under 2024-11-05")
	}

	// 2. Call under negotiated 2025-06-18
	server.protocolVersion = "2025-06-18"
	respStruct := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(11),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_get_context_pack",
			"arguments": map[string]interface{}{
				"symbol": "VerifyCredentials",
				"format": "markdown",
			},
		},
	}, noopNotify)

	resS, ok := respStruct.Result.(mcpToolCallResult)
	if !ok || resS.IsError {
		t.Fatalf("tool call error: %+v", resS)
	}
	if resS.StructuredContent == nil {
		t.Fatal("expected StructuredContent present under 2025-06-18")
	}
	if len(resS.Content) == 0 || !strings.Contains(resS.Content[0].Text, "VerifyCredentials") {
		t.Errorf("expected compatibility text content retained alongside structured content")
	}
}

func TestMCP_GetImpactReport_ValidationAndFormats(t *testing.T) {
	tempDir := t.TempDir()

	code := `package calc

func CalculateTotal(a, b int) int {
	return a + b
}
`
	_ = os.WriteFile(filepath.Join(tempDir, "calc.go"), []byte(code), 0644)

	engine := executor.NewEngine()
	s, _ := store.OpenStore(":memory:")
	defer s.Close()
	server := NewMCPServer(engine, s, tempDir)

	// 1. Valid impact by symbol with tree format
	respTree := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(20),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_get_impact_report",
			"arguments": map[string]interface{}{
				"symbol": "CalculateTotal",
				"format": "tree",
			},
		},
	}, noopNotify)

	resTree, ok := respTree.Result.(mcpToolCallResult)
	if !ok || resTree.IsError {
		t.Fatalf("impact tree call failed: %+v", resTree)
	}
	if len(resTree.Content) == 0 {
		t.Fatal("expected tree content")
	}

	// 2. Fractional budget rejected
	respFrac := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(21),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_get_impact_report",
			"arguments": map[string]interface{}{
				"symbol": "CalculateTotal",
				"budget": 200.5,
			},
		},
	}, noopNotify)

	resFrac := respFrac.Result.(mcpToolCallResult)
	if !resFrac.IsError || !strings.Contains(resFrac.Content[0].Text, "fractional budget") {
		t.Errorf("expected fractional budget error, got: %+v", resFrac)
	}

	// 3. Path outside workspace rejected
	respEscape := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(22),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_get_impact_report",
			"arguments": map[string]interface{}{
				"symbol": "CalculateTotal",
				"file":   "../../etc/passwd",
			},
		},
	}, noopNotify)

	resEscape := respEscape.Result.(mcpToolCallResult)
	if !resEscape.IsError || !strings.Contains(resEscape.Content[0].Text, "outside bound workspace") {
		t.Errorf("expected path escape error, got: %+v", resEscape)
	}

	// 4. Conflicting anchors (symbol and scope) rejected
	respConflict := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(23),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_get_impact_report",
			"arguments": map[string]interface{}{
				"symbol": "CalculateTotal",
				"scope":  "staged",
			},
		},
	}, noopNotify)

	resConflict := respConflict.Result.(mcpToolCallResult)
	if !resConflict.IsError || !strings.Contains(resConflict.Content[0].Text, "cannot specify both symbol and scope") {
		t.Errorf("expected conflicting anchor error, got: %+v", resConflict)
	}
}

func TestMCP_ProgressTokenContract(t *testing.T) {
	tempDir := t.TempDir()
	code := `package worker
func DoWork() {}
`
	_ = os.WriteFile(filepath.Join(tempDir, "work.go"), []byte(code), 0644)

	engine := executor.NewEngine()
	s, _ := store.OpenStore(":memory:")
	defer s.Close()
	server := NewMCPServer(engine, s, tempDir)

	// 1. Without progressToken -> no progress notification emitted
	var notifs1 []jsonRPCNotification
	collect1 := func(n interface{}) error {
		if notif, ok := n.(jsonRPCNotification); ok {
			notifs1 = append(notifs1, notif)
		}
		return nil
	}

	server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(30),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_get_context_pack",
			"arguments": map[string]interface{}{
				"symbol": "DoWork",
			},
		},
	}, collect1)

	if len(notifs1) != 0 {
		t.Errorf("expected 0 progress notifications without _meta.progressToken, got %d", len(notifs1))
	}

	// 2. With progressToken -> exactly matches supplied token
	var notifs2 []jsonRPCNotification
	collect2 := func(n interface{}) error {
		if notif, ok := n.(jsonRPCNotification); ok {
			notifs2 = append(notifs2, notif)
		}
		return nil
	}

	server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(31),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_get_context_pack",
			"arguments": map[string]interface{}{
				"symbol": "DoWork",
				"_meta": map[string]interface{}{
					"progressToken": "token-xyz-123",
				},
			},
		},
	}, collect2)

	if len(notifs2) == 0 {
		t.Fatal("expected progress notification with _meta.progressToken")
	}
	pMap := notifs2[0].Params.(map[string]interface{})
	if pMap["progressToken"] != "token-xyz-123" {
		t.Errorf("expected progressToken 'token-xyz-123', got %v", pMap["progressToken"])
	}
}
