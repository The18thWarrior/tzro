package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"tzro/internal/cache"
	"tzro/internal/config"
	"tzro/internal/mcp"
	"tzro/internal/memory"
)

// TestHelperProcess is the subprocess helper that acts as our mock MCP server
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		var req struct {
			Method string `json:"method"`
			ID     string `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}

		if req.Method == "tools/list" {
			resp := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"tools": []map[string]interface{}{
						{
							"name":        "mock_mcp_tool",
							"description": "Mock MCP Tool",
							"inputSchema": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"val": map[string]interface{}{"type": "string"},
								},
								"required": []string{"val"},
							},
						},
					},
				},
			}
			b, _ := json.Marshal(resp)
			fmt.Println(string(b))
		} else if req.Method == "tools/call" {
			resp := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"content": []map[string]interface{}{
						{
							"text": "mock_call_success",
						},
					},
				},
			}
			b, _ := json.Marshal(resp)
			fmt.Println(string(b))
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "scanner error: %v\n", err)
	}
}

func TestInitStaticConfig(t *testing.T) {
	// Create a temporary tool schemas file
	tempDir, err := os.MkdirTemp("", "tzro-tools-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "tool_schemas.json")
	schemasContent := `{
		"salesforce_query": {
			"schema": {
				"type": "object",
				"properties": {
					"soql": { "type": "string" }
				},
				"required": ["soql"]
			},
			"daemon": "salesforce-core"
		},
		"test_placeholder": {
			"schema": {
				"type": "object",
				"properties": {
					"foo": { "type": "string" }
				},
				"required": ["foo"]
			}
		}
	}`

	if err := os.WriteFile(configPath, []byte(schemasContent), 0644); err != nil {
		t.Fatalf("failed to write temp tool schemas: %v", err)
	}

	// Initialize registry with temp file
	if err := Init(configPath); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Verify static schemas are registered
	schemaSQ, err := GetSchema("salesforce_query")
	if err != nil {
		t.Fatalf("failed to get schema: %v", err)
	}
	if !strings.Contains(schemaSQ, "soql") || !strings.Contains(schemaSQ, "salesforce_query") {
		// MCPToolAdapter wraps GetSchema with GetGBNFSchema which adds tool_arguments wrapper
		if !strings.Contains(schemaSQ, "tool_arguments") {
			t.Errorf("expected GetSchema output to be wrapped in tool_arguments, got: %s", schemaSQ)
		}
	}

	schemaPlace, err := GetSchema("test_placeholder")
	if err != nil {
		t.Fatalf("failed to get schema: %v", err)
	}
	if !strings.Contains(schemaPlace, "foo") {
		t.Errorf("expected schema for test_placeholder to contain foo, got: %s", schemaPlace)
	}

	// Call placeholder tool, should return error since it's a placeholder without execution function
	_, err = Call(context.Background(), "test_placeholder", map[string]interface{}{"foo": "bar"})
	if err == nil || !strings.Contains(err.Error(), "has no execution function") {
		t.Errorf("expected error for calling placeholder tool, got: %v", err)
	}
}

func TestFunctionTools(t *testing.T) {
	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_tools_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_tools_test.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init DB: %v", err)
	}

	// Initialize registry
	if err := Init(""); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ctx := context.Background()
	rawPayload := `{"records": [{"id": 10, "name": "Tools Test"}]}`
	_, cacheID, err := cache.DefaultStore.Store(ctx, rawPayload)
	if err != nil {
		t.Fatalf("failed to store cache: %v", err)
	}
	defer func() {
		os.Remove(filepath.Join(".tzro", "cache", cacheID+".json"))
		os.RemoveAll(".tzro/cache")
	}()

	// 1. Test introspect_cache
	introResult, err := Call(ctx, "introspect_cache", map[string]interface{}{"cacheId": cacheID})
	if err != nil {
		t.Fatalf("failed to call introspect_cache: %v", err)
	}
	if !strings.Contains(introResult, "dataType") || !strings.Contains(introResult, cacheID) {
		t.Errorf("unexpected introspect result: %s", introResult)
	}

	// 2. Test sql_cached_data
	sqlResult, err := Call(ctx, "sql_cached_data", map[string]interface{}{
		"cacheId": cacheID,
		"sql":     fmt.Sprintf("SELECT * FROM %s", cacheID),
	})
	if err != nil {
		t.Fatalf("failed to call sql_cached_data: %v", err)
	}
	if !strings.Contains(sqlResult, "Tools Test") {
		t.Errorf("unexpected sql result: %s", sqlResult)
	}
}

func TestDynamicMCPDiscovery(t *testing.T) {
	// Create a temporary MCP config file
	tempDir, err := os.MkdirTemp("", "tzro-mcp-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "mcp_config.json")
	mcpContent := fmt.Sprintf(`{
		"mcpServers": {
			"mock-daemon": {
				"command": %q,
				"args": ["-test.run=TestHelperProcess"],
				"env": {
					"GO_WANT_HELPER_PROCESS": "1"
				}
			}
		}
	}`, os.Args[0])

	if err := os.WriteFile(configPath, []byte(mcpContent), 0644); err != nil {
		t.Fatalf("failed to write temp MCP config: %v", err)
	}

	// Load config into global MCP registry
	if err := mcp.GlobalRegistry.LoadConfig(configPath); err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	defer func() {
		// Stop any active daemons
		daemon, ok := mcp.GlobalRegistry.GetDaemon("mock-daemon")
		if ok {
			_ = daemon.Stop()
		}
	}()

	// Initialize tools registry
	if err := Init(""); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ctx := context.Background()

	// 1. Get GBNF schema dynamically, which should trigger discovery and register the tool
	schemaStr, err := GetSchema("mock_mcp_tool")
	if err != nil {
		t.Fatalf("failed to get dynamic schema: %v", err)
	}
	if !strings.Contains(schemaStr, "tool_arguments") || !strings.Contains(schemaStr, "val") {
		t.Errorf("unexpected dynamic GBNF schema: %s", schemaStr)
	}

	// 2. Execute the dynamically registered tool
	callResult, err := Call(ctx, "mock_mcp_tool", map[string]interface{}{"val": "hello-mcp"})
	if err != nil {
		t.Fatalf("failed to call dynamic tool: %v", err)
	}
	if callResult != "mock_call_success" {
		t.Errorf("unexpected call result: %s", callResult)
	}
}

func TestWasmDynamicRegistration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tzro-wasm-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("TZRO_DIR", tmpDir)

	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting(filepath.Join(tmpDir, "tzro_tools_wasm_test.db"))
	defer func() {
		memory.DB.Close()
		ClearLocalConnectionPool()
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init DB: %v", err)
	}

	// Create wasm directory as resolved by config
	wasmDir := config.ResolvePath("wasm")
	if err := os.MkdirAll(wasmDir, 0755); err != nil {
		t.Fatalf("failed to create wasm dir: %v", err)
	}
	defer os.RemoveAll(wasmDir)

	// Write simple schema
	schemaPath := filepath.Join(wasmDir, "test_dyn_skill.json")
	schemaJSON := `{
		"type": "object",
		"properties": {
			"name": { "type": "string" }
		},
		"required": ["name"]
	}`
	if err := os.WriteFile(schemaPath, []byte(schemaJSON), 0644); err != nil {
		t.Fatalf("failed to write schema: %v", err)
	}

	// Compile a tiny WASM binary
	buildDir, err := os.MkdirTemp("", "tzro-dyn-wasm-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(buildDir)

	goCode := `
package main

import (
	"encoding/json"
	"os"
)

type Input struct {
	Name string ` + "`json:\"name\"`" + `
}

type Output struct {
	Greeting string ` + "`json:\"greeting\"`" + `
}

func main() {
	var in Input
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		os.Exit(1)
	}
	out := Output{Greeting: "Hello, " + in.Name + "!"}
	json.NewEncoder(os.Stdout).Encode(&out)
}
`
	srcPath := filepath.Join(buildDir, "main.go")
	if err := os.WriteFile(srcPath, []byte(goCode), 0644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	wasmPath := filepath.Join(wasmDir, "test_dyn_skill.wasm")
	cmd := exec.Command("go", "build", "-o", wasmPath, "main.go")
	cmd.Dir = buildDir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to compile WASM: %v, output: %s", err, string(output))
	}

	// Clear registry and initialize
	if err := Init(""); err != nil {
		t.Fatalf("failed to Init: %v", err)
	}
	defer Unregister("test_dyn_skill")

	// Verify it was dynamically registered
	schema, err := GetSchema("test_dyn_skill")
	if err != nil {
		t.Fatalf("failed to GetSchema: %v", err)
	}
	if !strings.Contains(schema, "properties") {
		t.Errorf("unexpected schema: %s", schema)
	}

	// Execute it via tools.Call
	ctx := context.Background()
	res, err := Call(ctx, "test_dyn_skill", map[string]interface{}{"name": "Executor"})
	if err != nil {
		t.Fatalf("failed to execute dynamically registered WASM tool: %v", err)
	}

	var output struct {
		Greeting string `json:"greeting"`
	}
	if err := json.Unmarshal([]byte(res), &output); err != nil {
		t.Fatalf("failed to decode response: %v, raw: %s", err, res)
	}

	if output.Greeting != "Hello, Executor!" {
		t.Errorf("expected 'Hello, Executor!', got '%s'", output.Greeting)
	}
}
