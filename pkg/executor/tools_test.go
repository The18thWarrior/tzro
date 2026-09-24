package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/pkg/store"
)

func TestBuiltinDispatcher_BashExecution(t *testing.T) {
	disp := NewBuiltinDispatcher("", nil)

	result, err := disp.Dispatch(context.Background(), "bash", map[string]interface{}{
		"command": "echo dispatcher_test",
	})
	if err != nil {
		t.Fatalf("bash dispatch failed: %v", err)
	}

	stdout, ok := result["stdout"].(string)
	if !ok {
		t.Fatal("expected stdout string in result")
	}
	if !strings.Contains(stdout, "dispatcher_test") {
		t.Errorf("expected stdout to contain 'dispatcher_test', got %q", stdout)
	}

	exitCode, ok := result["exit_code"].(int)
	if !ok {
		t.Fatal("expected exit_code int in result")
	}
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
}

func TestBuiltinDispatcher_Probe(t *testing.T) {
	// Create a temporary workspace with a Go file containing known symbols
	workspace := t.TempDir()
	goFile := filepath.Join(workspace, "handler.go")
	err := os.WriteFile(goFile, []byte(`package main

func HandleRequest(w http.ResponseWriter, r *http.Request) {
	// handle the request
}

func ProcessData(input string) string {
	return input
}
`), 0o644)
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer s.Close()

	disp := NewBuiltinDispatcher(workspace, s)

	result, err := disp.Dispatch(context.Background(), "probe", map[string]interface{}{
		"query":       "HandleRequest",
		"max_results": float64(5),
	})
	if err != nil {
		t.Fatalf("probe dispatch failed: %v", err)
	}

	matches, ok := result["matches"]
	if !ok {
		t.Fatal("expected 'matches' in probe result")
	}

	matchList, ok := matches.([]interface{})
	if !ok {
		t.Fatalf("expected matches to be a slice, got %T", matches)
	}

	if len(matchList) == 0 {
		t.Error("expected at least one match for 'HandleRequest'")
	}
}

func TestBuiltinDispatcher_Skeleton(t *testing.T) {
	workspace := t.TempDir()
	goFile := filepath.Join(workspace, "example.go")
	err := os.WriteFile(goFile, []byte(`package main

import "fmt"

func Hello() {
	fmt.Println("Hello, World!")
}

func Goodbye() {
	fmt.Println("Goodbye!")
}
`), 0o644)
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer s.Close()

	disp := NewBuiltinDispatcher(workspace, s)

	result, err := disp.Dispatch(context.Background(), "skeleton", map[string]interface{}{
		"file": goFile,
	})
	if err != nil {
		t.Fatalf("skeleton dispatch failed: %v", err)
	}

	skeleton, ok := result["skeleton"].(string)
	if !ok {
		t.Fatal("expected 'skeleton' string in result")
	}

	// Skeleton should contain the function signatures but with elided bodies
	if !strings.Contains(skeleton, "func Hello()") {
		t.Error("skeleton should contain Hello function signature")
	}
	if !strings.Contains(skeleton, "body elided") {
		t.Error("skeleton should contain body elision markers")
	}

	// Check compaction metrics
	if _, ok := result["savings_ratio"]; !ok {
		t.Error("expected 'savings_ratio' in skeleton result")
	}
}

func TestBuiltinDispatcher_QueryAutoIngest(t *testing.T) {
	workspace := t.TempDir()

	// Create a CSV test file
	csvFile := filepath.Join(workspace, "metrics.csv")
	csvContent := "name,value,category\n"
	csvContent += "cpu_usage,85,system\n"
	csvContent += "mem_usage,72,system\n"
	csvContent += "disk_io,45,storage\n"
	csvContent += "network_rx,1200,network\n"
	csvContent += "network_tx,800,network\n"
	err := os.WriteFile(csvFile, []byte(csvContent), 0o644)
	if err != nil {
		t.Fatalf("failed to write CSV: %v", err)
	}

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer s.Close()

	disp := NewBuiltinDispatcher(workspace, s)

	// Auto-ingest and query in one step
	result, err := disp.Dispatch(context.Background(), "query", map[string]interface{}{
		"file": csvFile,
		"sql":  "SELECT COUNT(*) as cnt FROM metrics",
	})
	if err != nil {
		t.Fatalf("query dispatch failed: %v", err)
	}

	// Verify we got query results
	rows, ok := result["rows"]
	if !ok {
		t.Fatal("expected 'rows' in query result")
	}

	rowList, ok := rows.([]map[string]string)
	if !ok {
		t.Fatalf("expected rows to be []map[string]string, got %T", rows)
	}

	if len(rowList) == 0 {
		t.Fatal("expected at least one result row")
	}

	// COUNT(*) should be 5 (5 data rows)
	cnt, ok := rowList[0]["cnt"]
	if !ok {
		t.Fatal("expected 'cnt' column in result")
	}
	if cnt != "5" {
		t.Errorf("expected count 5, got %q", cnt)
	}
}

func TestBuiltinDispatcher_QueryCategoryGroupBy(t *testing.T) {
	workspace := t.TempDir()

	csvFile := filepath.Join(workspace, "metrics.csv")
	csvContent := "name,value,category\n"
	csvContent += "cpu_usage,85,system\n"
	csvContent += "mem_usage,72,system\n"
	csvContent += "disk_io,45,storage\n"
	csvContent += "network_rx,1200,network\n"
	csvContent += "network_tx,800,network\n"
	err := os.WriteFile(csvFile, []byte(csvContent), 0o644)
	if err != nil {
		t.Fatalf("failed to write CSV: %v", err)
	}

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer s.Close()

	disp := NewBuiltinDispatcher(workspace, s)

	result, err := disp.Dispatch(context.Background(), "query", map[string]interface{}{
		"file": csvFile,
		"sql":  "SELECT category, COUNT(*) as cnt FROM metrics GROUP BY category ORDER BY cnt DESC",
	})
	if err != nil {
		t.Fatalf("query dispatch failed: %v", err)
	}

	rows := result["rows"].([]map[string]string)
	if len(rows) != 3 {
		t.Errorf("expected 3 category groups, got %d", len(rows))
	}

	// system and network should each have 2 entries
	found := map[string]string{}
	for _, r := range rows {
		found[r["category"]] = r["cnt"]
	}
	if found["system"] != "2" {
		t.Errorf("expected system count 2, got %q", found["system"])
	}
	if found["network"] != "2" {
		t.Errorf("expected network count 2, got %q", found["network"])
	}
	if found["storage"] != "1" {
		t.Errorf("expected storage count 1, got %q", found["storage"])
	}
}

func TestBuiltinDispatcher_UnknownTool(t *testing.T) {
	disp := NewBuiltinDispatcher("", nil)
	_, err := disp.Dispatch(context.Background(), "nonexistent_tool", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}
