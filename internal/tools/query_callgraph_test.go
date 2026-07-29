package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Slice 9: query_callgraph tool ---

func TestQueryCallgraphTool_Registration(t *testing.T) {
	tool := NewQueryCallgraphTool()
	Register(tool)
	defer Unregister("query_callgraph")

	got := GetTool("query_callgraph")
	if got == nil {
		t.Fatal("query_callgraph tool not registered")
	}

	schema, err := got.GetSchema()
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}

	// Schema should mention directory parameter
	if !strings.Contains(schema, "directory") {
		t.Error("schema missing 'directory' parameter")
	}
}

func TestQueryCallgraphTool_Call(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a small Go project
	os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(`package example

func Process(input string) string {
	return validate(input)
}

func validate(input string) string {
	return input
}
`), 0644)

	tool := NewQueryCallgraphTool()

	result, err := tool.Call(context.TODO(), map[string]interface{}{
		"directory":      tmpDir,
		"include_bodies": false,
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	if result == "" {
		t.Fatal("expected non-empty result")
	}

	if !strings.Contains(result, "Process") {
		t.Error("result should contain Process")
	}
	if !strings.Contains(result, "validate") {
		t.Error("result should contain validate")
	}
}
