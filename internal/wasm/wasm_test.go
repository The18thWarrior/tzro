package wasm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	testWasmPath   string
	testSchemaPath string
)

func TestMain(m *testing.M) {
	// Compile a unified Go binary to WASM/WASI that routes multiple behaviors
	tmpDir, err := os.MkdirTemp("", "tzro-wasm-test-*")
	if err != nil {
		fmt.Printf("Failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	goCode := `
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Input struct {
	Action string ` + "`json:\"action\"`" + `
	Name   string ` + "`json:\"name\"`" + `
	Path   string ` + "`json:\"path\"`" + `
}

type Output struct {
	Greeting string ` + "`json:\"greeting\"`" + `
	EnvValue string ` + "`json:\"env_value\"`" + `
	FileData string ` + "`json:\"file_data\"`" + `
}

func main() {
	dec := json.NewDecoder(os.Stdin)
	var in Input
	if err := dec.Decode(&in); err != nil {
		fmt.Fprintf(os.Stderr, "failed to decode input: %v\n", err)
		os.Exit(1)
	}
	
	switch in.Action {
	case "echo":
		out := Output{Greeting: fmt.Sprintf("Hello, %s!", in.Name)}
		json.NewEncoder(os.Stdout).Encode(&out)
	case "loop":
		for {
			time.Sleep(1 * time.Millisecond)
		}
	case "exit_error":
		fmt.Fprintln(os.Stderr, "intentional failure in WASM")
		os.Exit(42)
	case "read_env":
		out := Output{EnvValue: os.Getenv("TZRO_TEST_VAR")}
		json.NewEncoder(os.Stdout).Encode(&out)
	case "read_file":
		data, err := os.ReadFile(in.Path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read file failed: %v\n", err)
			os.Exit(100)
		}
		out := Output{FileData: string(data)}
		json.NewEncoder(os.Stdout).Encode(&out)
	default:
		fmt.Fprintf(os.Stderr, "unknown action: %s\n", in.Action)
		os.Exit(99)
	}
}
`
	srcPath := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(srcPath, []byte(goCode), 0644); err != nil {
		fmt.Printf("Failed to write main.go: %v\n", err)
		os.Exit(1)
	}

	testWasmPath = filepath.Join(tmpDir, "test_echo.wasm")
	cmd := exec.Command("go", "build", "-o", testWasmPath, "main.go")
	cmd.Dir = tmpDir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Printf("Failed to compile WASM: %v, output: %s\n", err, string(output))
		os.Exit(1)
	}

	// Write mock schema
	testSchemaPath = filepath.Join(tmpDir, "test_echo.json")
	schemaJSON := `{
		"type": "object",
		"properties": {
			"action": { "type": "string" },
			"name": { "type": "string" },
			"path": { "type": "string" }
		},
		"required": ["action"]
	}`
	if err := os.WriteFile(testSchemaPath, []byte(schemaJSON), 0644); err != nil {
		fmt.Printf("Failed to write schema: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func TestWasmToolAdapter_Success(t *testing.T) {
	adapter := NewWasmToolAdapter("test_echo", testWasmPath, testSchemaPath)

	if adapter.Name() != "test_echo" {
		t.Errorf("Expected tool name 'test_echo', got '%s'", adapter.Name())
	}

	schema, err := adapter.GetSchema()
	if err != nil {
		t.Fatalf("Expected GetSchema to succeed, got error: %v", err)
	}
	if !strings.Contains(schema, `"properties"`) {
		t.Errorf("Expected schema to contain 'properties', got: %s", schema)
	}

	ctx := context.Background()
	args := map[string]interface{}{
		"action": "echo",
		"name":   "Tzro",
	}
	res, err := adapter.Call(ctx, args)
	if err != nil {
		t.Fatalf("Expected Call to succeed, got error: %v", err)
	}

	var output struct {
		Greeting string `json:"greeting"`
	}
	if err := json.Unmarshal([]byte(res), &output); err != nil {
		t.Fatalf("Failed to decode call output: %v, raw: %s", err, res)
	}

	expected := "Hello, Tzro!"
	if output.Greeting != expected {
		t.Errorf("Expected greeting '%s', got '%s'", expected, output.Greeting)
	}
}

func TestWasmToolAdapter_Timeout(t *testing.T) {
	// Use a very short timeout of 100ms to verify context timeout enforcement
	adapter := NewWasmToolAdapter("test_echo", testWasmPath, testSchemaPath, WithTimeout(100*time.Millisecond))

	ctx := context.Background()
	args := map[string]interface{}{
		"action": "loop",
	}

	start := time.Now()
	_, err := adapter.Call(ctx, args)
	duration := time.Since(start)

	if err == nil {
		t.Fatal("Expected Call to fail due to timeout, but it succeeded")
	}

	if !strings.Contains(err.Error(), "context deadline exceeded") && !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("Expected timeout/deadline exceeded error, got: %v", err)
	}

	if duration > 15*time.Second {
		t.Errorf("Expected execution to be terminated quickly, but took %v", duration)
	}
}

func TestWasmToolAdapter_ErrorHandling(t *testing.T) {
	adapter := NewWasmToolAdapter("test_echo", testWasmPath, testSchemaPath)

	ctx := context.Background()
	args := map[string]interface{}{
		"action": "exit_error",
	}

	_, err := adapter.Call(ctx, args)
	if err == nil {
		t.Fatal("Expected Call to fail due to non-zero exit code, but it succeeded")
	}

	if !strings.Contains(err.Error(), "intentional failure in WASM") {
		t.Errorf("Expected error message to contain stderr 'intentional failure in WASM', got: %v", err)
	}
}

func TestWasmToolAdapter_SecuritySandbox(t *testing.T) {
	adapter := NewWasmToolAdapter("test_echo", testWasmPath, testSchemaPath)

	// 1. Verify environment variables are isolated
	_ = os.Setenv("TZRO_TEST_VAR", "host-secret")
	defer os.Unsetenv("TZRO_TEST_VAR")

	ctx := context.Background()
	argsEnv := map[string]interface{}{
		"action": "read_env",
	}

	resEnv, err := adapter.Call(ctx, argsEnv)
	if err != nil {
		t.Fatalf("Expected env read call to succeed, got: %v", err)
	}

	var outEnv struct {
		EnvValue string `json:"env_value"`
	}
	if err := json.Unmarshal([]byte(resEnv), &outEnv); err != nil {
		t.Fatalf("Failed to decode env output: %v", err)
	}

	if outEnv.EnvValue != "" {
		t.Errorf("Security Breach: Sandbox inherited environment variable: '%s'", outEnv.EnvValue)
	}

	// 2. Verify filesystem is isolated
	tmpFile, err := os.CreateTemp("", "tzro-host-file-*")
	if err != nil {
		t.Fatalf("Failed to create host temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	_, _ = tmpFile.WriteString("host-secret-data")
	_ = tmpFile.Close()

	argsFile := map[string]interface{}{
		"action": "read_file",
		"path":   tmpFile.Name(),
	}

	_, err = adapter.Call(ctx, argsFile)
	if err == nil {
		t.Fatal("Security Breach: Sandbox read host file, expected failure")
	}

	if !strings.Contains(err.Error(), "read file failed") {
		t.Errorf("Expected read file failed error, got: %v", err)
	}
}
