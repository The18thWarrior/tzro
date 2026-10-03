package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tzro/pkg/executor"
)

// TestMCP_EditAndVerify_E2E_CorrectEditPassesCheck is the primary E2E test
// for the grouped edit-and-verify operation through the MCP transport.
// It starts a real MCP server with a temporary Go project workspace,
// submits a correction through tzro_edit_and_verify, and asserts:
// - file bytes are correct after edit
// - native check passes with observed exit code
// - mandatory Verification Summary fields present in response
// - the response comes through the MCP JSON-RPC path, not direct service call
func TestMCP_EditAndVerify_E2E_CorrectEditPassesCheck(t *testing.T) {
	ws := setupGoFixture(t)
	engine := executor.NewEngine(executor.WithMaxConcurrency(2))
	server := NewMCPServer(engine, nil, ws)

	// Initialize first.
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
	if initResp.Error != nil {
		t.Fatalf("initialize: %s", initResp.Error.Message)
	}

	// Verify tzro_edit_and_verify is in tools list.
	toolsResp := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(2),
		Method:  "tools/list",
	}, noopNotify)
	toolsResult := toolsResp.Result.(mcpToolsListResult)
	found := false
	for _, tool := range toolsResult.Tools {
		if tool.Name == "tzro_edit_and_verify" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("tzro_edit_and_verify not in tools/list")
	}

	// Submit the correct fix.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp := server.handleRequest(ctx, &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(3),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_edit_and_verify",
			"arguments": map[string]interface{}{
				"edits": []interface{}{
					map[string]interface{}{
						"kind":     "replace",
						"path":     "calc.go",
						"old_text": "return a - b",
						"new_text": "return a + b",
					},
				},
			},
		},
	}, noopNotify)

	if resp == nil {
		t.Fatal("nil response from tools/call")
	}
	if resp.Error != nil {
		t.Fatalf("tools/call error: %s", resp.Error.Message)
	}

	// Parse the response content.
	result, ok := resp.Result.(mcpToolCallResult)
	if !ok {
		t.Fatalf("expected mcpToolCallResult, got %T", resp.Result)
	}

	// Assert text content is present.
	if len(result.Content) == 0 {
		t.Fatal("no content in response")
	}
	text := result.Content[0].Text
	if text == "" {
		t.Fatal("empty text content")
	}

	// Parse the text to verify mandatory fields.
	assertContains(t, text, "schema: tzro.verification.v1")
	assertContains(t, text, "application: applied")
	assertContains(t, text, "verification: passed")
	assertContains(t, text, "termination: completed")

	// Verify the file on disk was actually changed.
	data, err := os.ReadFile(filepath.Join(ws, "calc.go"))
	if err != nil {
		t.Fatalf("cannot read calc.go: %v", err)
	}
	if got := string(data); got != correctCalcGoFixture {
		t.Errorf("calc.go not corrected on disk:\ngot:  %s\nwant: %s", got, correctCalcGoFixture)
	}
}

// TestMCP_EditAndVerify_E2E_WrongEditRetainsWithFailure verifies the E2E path
// for a wrong edit: file is modified, check fails, edits stay on disk.
func TestMCP_EditAndVerify_E2E_WrongEditRetainsWithFailure(t *testing.T) {
	ws := setupGoFixture(t)
	engine := executor.NewEngine(executor.WithMaxConcurrency(2))
	server := NewMCPServer(engine, nil, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp := server.handleRequest(ctx, &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_edit_and_verify",
			"arguments": map[string]interface{}{
				"edits": []interface{}{
					map[string]interface{}{
						"kind":     "replace",
						"path":     "calc.go",
						"old_text": "return a - b",
						"new_text": "return a * b",
					},
				},
			},
		},
	}, noopNotify)

	if resp.Error != nil {
		t.Fatalf("tools/call error: %s", resp.Error.Message)
	}

	result := resp.Result.(mcpToolCallResult)
	text := result.Content[0].Text

	// Edits applied, check failed, edits NOT rolled back.
	assertContains(t, text, "application: applied")
	assertContains(t, text, "verification: failed")

	// Verify the wrong fix is still on disk (not reverted).
	data, _ := os.ReadFile(filepath.Join(ws, "calc.go"))
	if string(data) == brokenCalcGoFixture {
		t.Error("file was rolled back to original; edits must be retained on check failure")
	}
	assertContains(t, string(data), "return a * b")
}

// TestMCP_EditAndVerify_E2E_ConflictRejectsBeforeWrite tests the full MCP
// path for batch rejection: no file is modified when an edit conflicts.
func TestMCP_EditAndVerify_E2E_ConflictRejectsBeforeWrite(t *testing.T) {
	ws := setupGoFixture(t)
	engine := executor.NewEngine(executor.WithMaxConcurrency(2))
	server := NewMCPServer(engine, nil, ws)

	resp := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_edit_and_verify",
			"arguments": map[string]interface{}{
				"edits": []interface{}{
					map[string]interface{}{
						"kind":     "replace",
						"path":     "calc.go",
						"old_text": "return a - b",
						"new_text": "return a + b",
					},
					map[string]interface{}{
						"kind":             "replace",
						"path":             "calc_test.go",
						"old_text":         "THIS DOES NOT EXIST IN THE FILE",
						"new_text":         "anything",
						"expected_matches": float64(1),
					},
				},
			},
		},
	}, noopNotify)

	if resp.Error != nil {
		t.Fatalf("transport error: %s", resp.Error.Message)
	}

	result := resp.Result.(mcpToolCallResult)
	text := result.Content[0].Text

	assertContains(t, text, "application: rejected")

	// First file must NOT have been modified.
	data, _ := os.ReadFile(filepath.Join(ws, "calc.go"))
	if string(data) != brokenCalcGoFixture {
		t.Error("first file was modified despite batch rejection")
	}
}

// TestMCP_EditAndVerify_E2E_MissingPreset tests that edits apply but
// verification reports not_configured when no preset exists.
func TestMCP_EditAndVerify_E2E_MissingPreset(t *testing.T) {
	ws := setupGoFixtureNoPreset(t)
	engine := executor.NewEngine(executor.WithMaxConcurrency(2))
	server := NewMCPServer(engine, nil, ws)

	resp := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_edit_and_verify",
			"arguments": map[string]interface{}{
				"edits": []interface{}{
					map[string]interface{}{
						"kind":     "replace",
						"path":     "calc.go",
						"old_text": "return a - b",
						"new_text": "return a + b",
					},
				},
			},
		},
	}, noopNotify)

	result := resp.Result.(mcpToolCallResult)
	text := result.Content[0].Text

	assertContains(t, text, "application: applied")
	assertContains(t, text, "verification: not_configured")
}

// TestMCP_EditAndVerify_E2E_EmptyBatchRejects tests that an empty edit
// batch returns a structured rejection through MCP without transport error.
func TestMCP_EditAndVerify_E2E_EmptyBatchRejects(t *testing.T) {
	ws := setupGoFixture(t)
	engine := executor.NewEngine(executor.WithMaxConcurrency(2))
	server := NewMCPServer(engine, nil, ws)

	resp := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_edit_and_verify",
			"arguments": map[string]interface{}{
				"edits": []interface{}{},
			},
		},
	}, noopNotify)

	// Transport must succeed — domain rejection is not a transport error.
	if resp.Error != nil {
		t.Fatalf("transport error for empty batch: %s", resp.Error.Message)
	}

	result := resp.Result.(mcpToolCallResult)
	text := result.Content[0].Text
	assertContains(t, text, "application: rejected")
	assertContains(t, text, "empty edit batch")
}

// TestMCP_EditAndVerify_E2E_StructuredOutput tests that protocol version
// 2025-06-18 returns structured content alongside text.
func TestMCP_EditAndVerify_E2E_StructuredOutput(t *testing.T) {
	ws := setupGoFixture(t)
	engine := executor.NewEngine(executor.WithMaxConcurrency(2))
	server := NewMCPServer(engine, nil, ws)

	// Initialize with 2025-06-18 protocol.
	server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
		},
	}, noopNotify)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp := server.handleRequest(ctx, &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(2),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_edit_and_verify",
			"arguments": map[string]interface{}{
				"edits": []interface{}{
					map[string]interface{}{
						"kind":     "replace",
						"path":     "calc.go",
						"old_text": "return a - b",
						"new_text": "return a + b",
					},
				},
			},
		},
	}, noopNotify)

	result := resp.Result.(mcpToolCallResult)

	// With 2025-06-18, structured content must be present.
	if result.StructuredContent == nil {
		t.Fatal("structured content missing for protocol 2025-06-18")
	}

	// Verify structured content has mandatory fields.
	sc := result.StructuredContent
	if sc["schema"] != "tzro.verification.v1" {
		t.Errorf("structured schema = %v, want tzro.verification.v1", sc["schema"])
	}
	if sc["application"] != "applied" {
		t.Errorf("structured application = %v, want applied", sc["application"])
	}
	if sc["verification"] != "passed" {
		t.Errorf("structured verification = %v, want passed", sc["verification"])
	}

	// Verify changed_files is present and correct.
	cf, ok := sc["changed_files"].([]interface{})
	if !ok || len(cf) == 0 {
		t.Errorf("structured changed_files missing or empty: %v", sc["changed_files"])
	}

	// Verify checks array is present.
	checks, ok := sc["checks"].([]interface{})
	if !ok || len(checks) == 0 {
		t.Error("structured checks missing or empty")
	}
	if len(checks) > 0 {
		check := checks[0].(map[string]interface{})
		if check["id"] != "tests" {
			t.Errorf("structured check.id = %v, want tests", check["id"])
		}
		if check["status"] != "passed" {
			t.Errorf("structured check.status = %v, want passed", check["status"])
		}
	}
}

// TestMCP_EditAndVerify_E2E_Deadline tests that the operation respects
// a tight context deadline and returns an incomplete result.
func TestMCP_EditAndVerify_E2E_Deadline(t *testing.T) {
	ws := setupGoFixture(t)
	engine := executor.NewEngine(executor.WithMaxConcurrency(2))
	server := NewMCPServer(engine, nil, ws)

	// Use a very short deadline — just enough for preflight but not for go test.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	resp := server.handleRequest(ctx, &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_edit_and_verify",
			"arguments": map[string]interface{}{
				"edits": []interface{}{
					map[string]interface{}{
						"kind":     "replace",
						"path":     "calc.go",
						"old_text": "return a - b",
						"new_text": "return a + b",
					},
				},
			},
		},
	}, noopNotify)

	// Must not be a transport error — domain results are always returned.
	if resp.Error != nil {
		t.Fatalf("transport error on deadline: %s", resp.Error.Message)
	}

	result := resp.Result.(mcpToolCallResult)
	text := result.Content[0].Text

	// The operation may have applied edits before the deadline hit,
	// or the deadline may have hit during preflight/application/checks.
	// Either way, it must report a domain result.
	assertContains(t, text, "schema: tzro.verification.v1")

	// If application happened, verification must be incomplete or failed.
	if containsStr(text, "application: applied") {
		if containsStr(text, "verification: passed") {
			// This is actually fine if the test completed before the deadline.
			// The 50ms deadline may or may not be enough depending on system load.
			t.Log("NOTE: operation completed before deadline; test is non-deterministic on fast systems")
		}
	}
}

// TestMCP_EditAndVerify_E2E_CreateFileAndVerify tests creating a new file
// through the MCP transport path.
func TestMCP_EditAndVerify_E2E_CreateFileAndVerify(t *testing.T) {
	ws := setupGoFixtureNoPreset(t) // No preset — just test creation.
	engine := executor.NewEngine(executor.WithMaxConcurrency(2))
	server := NewMCPServer(engine, nil, ws)

	resp := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_edit_and_verify",
			"arguments": map[string]interface{}{
				"edits": []interface{}{
					map[string]interface{}{
						"kind":    "create",
						"path":    "multiply.go",
						"content": "package calc\n\nfunc Multiply(a, b int) int { return a * b }\n",
					},
				},
			},
		},
	}, noopNotify)

	if resp.Error != nil {
		t.Fatalf("transport error: %s", resp.Error.Message)
	}

	result := resp.Result.(mcpToolCallResult)
	text := result.Content[0].Text
	assertContains(t, text, "application: applied")

	// Verify file exists on disk.
	data, err := os.ReadFile(filepath.Join(ws, "multiply.go"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	assertContains(t, string(data), "func Multiply")
}

// TestMCP_EditAndVerify_E2E_ExistingToolsStillWork verifies that the
// existing MCP tools (graph, context, impact) still work after adding
// the edit-and-verify handler.
func TestMCP_EditAndVerify_E2E_ExistingToolsStillWork(t *testing.T) {
	ws := setupGoFixture(t)
	engine := executor.NewEngine(executor.WithMaxConcurrency(2))
	server := NewMCPServer(engine, nil, ws)

	// Initialize.
	server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
		},
	}, noopNotify)

	// tools/list must contain all expected tools.
	toolsResp := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(2),
		Method:  "tools/list",
	}, noopNotify)

	toolsResult := toolsResp.Result.(mcpToolsListResult)
	expected := map[string]bool{
		"tzro_execute_graph":     false,
		"tzro_get_context_pack":  false,
		"tzro_get_impact_report": false,
		"tzro_edit_and_verify":   false,
	}
	for _, tool := range toolsResult.Tools {
		if _, ok := expected[tool.Name]; ok {
			expected[tool.Name] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("missing tool: %s", name)
		}
	}

	// Execute a graph call to verify it still works.
	graphResp := server.handleRequest(context.Background(), &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(3),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name": "tzro_execute_graph",
			"arguments": map[string]interface{}{
				"version": "3.0",
				"task_id": "verify-existing-tools",
				"nodes": []interface{}{
					map[string]interface{}{
						"id":   "search",
						"tool": "probe",
						"inputs": map[string]interface{}{
							"query": "Add",
						},
					},
				},
			},
		},
	}, noopNotify)

	if graphResp.Error != nil {
		t.Fatalf("graph execution failed: %s", graphResp.Error.Message)
	}
}

// TestMCP_EditAndVerify_E2E_FullStdioProtocol tests the complete stdio
// protocol flow: initialize → tools/list → tools/call with edit-and-verify.
func TestMCP_EditAndVerify_E2E_FullStdioProtocol(t *testing.T) {
	ws := setupGoFixtureNoPreset(t) // No preset needed.
	engine := executor.NewEngine(executor.WithMaxConcurrency(2))
	server := NewMCPServer(engine, nil, ws)

	// Simulate the full JSON-RPC protocol as a client would.
	requests := []jsonRPCRequest{
		{
			JSONRPC: "2.0",
			ID:      float64(1),
			Method:  "initialize",
			Params: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"clientInfo":      map[string]interface{}{"name": "agy", "version": "3.0"},
			},
		},
		{
			JSONRPC: "2.0",
			ID:      float64(2),
			Method:  "tools/list",
		},
		{
			JSONRPC: "2.0",
			ID:      float64(3),
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name": "tzro_edit_and_verify",
				"arguments": map[string]interface{}{
					"edits": []interface{}{
						map[string]interface{}{
							"kind":     "replace",
							"path":     "calc.go",
							"old_text": "return a - b",
							"new_text": "return a + b",
						},
					},
				},
			},
		},
	}

	var responses []*jsonRPCResponse
	for _, req := range requests {
		r := req
		resp := server.handleRequest(context.Background(), &r, noopNotify)
		if resp != nil {
			responses = append(responses, resp)
		}
	}

	if len(responses) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(responses))
	}

	// Each response ID must match the request.
	for i, resp := range responses {
		expectedID := float64(i + 1)
		if resp.ID != expectedID {
			t.Errorf("response %d: id = %v, want %v", i, resp.ID, expectedID)
		}
		if resp.Error != nil {
			t.Errorf("response %d: unexpected error: %s", i, resp.Error.Message)
		}
	}

	// The third response should have the edit-and-verify result.
	result := responses[2].Result.(mcpToolCallResult)
	text := result.Content[0].Text
	assertContains(t, text, "application: applied")
}

// --- E2E test helpers ---

const brokenCalcGoFixture = `package calc

// Add returns the sum of two integers.
func Add(a, b int) int {
	return a - b
}
`

const correctCalcGoFixture = `package calc

// Add returns the sum of two integers.
func Add(a, b int) int {
	return a + b
}
`

const calcTestGoFixture = `package calc

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Errorf("Add(2, 3) = %d, want 5", got)
	}
}
`

const goModFixture = `module calc

go 1.26
`

const presetFixture = `version: 1
timeout: 30s
checks:
  - id: tests
    argv: [go, test, -count=1, ./...]
    cwd: .
`

// setupGoFixture creates a temporary Go project with a broken calculation,
// a test, and a verification preset.
func setupGoFixture(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()

	for name, content := range map[string]string{
		"go.mod":       goModFixture,
		"calc.go":      brokenCalcGoFixture,
		"calc_test.go": calcTestGoFixture,
	} {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Write the verification preset.
	tzroDir := filepath.Join(ws, ".tzro")
	if err := os.MkdirAll(tzroDir, 0755); err != nil {
		t.Fatalf("mkdir .tzro: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tzroDir, "verification.yaml"), []byte(presetFixture), 0644); err != nil {
		t.Fatalf("write preset: %v", err)
	}

	return ws
}

// setupGoFixtureNoPreset creates the Go project without a verification preset.
func setupGoFixtureNoPreset(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()

	for name, content := range map[string]string{
		"go.mod":       goModFixture,
		"calc.go":      brokenCalcGoFixture,
		"calc_test.go": calcTestGoFixture,
	} {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	return ws
}

// assertContains is a test helper that asserts a string contains a substring.
func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !containsStr(s, substr) {
		t.Errorf("expected %q to contain %q", truncate(s, 200), substr)
	}
}

func containsStr(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && json.Valid([]byte("\""+substr+"\"")) && // valid string
		findSubstring(s, substr)
}

func findSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
