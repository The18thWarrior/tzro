package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/internal/memory"
)

func setupFilesystemTestFixtures(t *testing.T) (string, *PathValidator) {
	t.Helper()
	root := testdataDir(t)

	// Create a file with many lines for line-range and cap tests
	manyLinesPath := filepath.Join(root, "many_lines.txt")
	var lines []string
	for i := 1; i <= 250; i++ {
		lines = append(lines, "line "+string(rune('0'+(i/100)%10))+string(rune('0'+(i/10)%10))+string(rune('0'+i%10)))
	}
	// Use a simpler approach — write numbered lines
	var content strings.Builder
	for i := 1; i <= 250; i++ {
		content.WriteString("line " + intToStr(i) + "\n")
	}
	if err := os.WriteFile(manyLinesPath, []byte(content.String()), 0644); err != nil {
		t.Fatalf("failed to create many_lines.txt: %v", err)
	}

	// Create a file with searchable content
	searchablePath := filepath.Join(root, "searchable.go")
	searchContent := `package main

import "fmt"

func hello() {
	fmt.Println("hello world")
}

func goodbye() {
	fmt.Println("goodbye world")
}
`
	if err := os.WriteFile(searchablePath, []byte(searchContent), 0644); err != nil {
		t.Fatalf("failed to create searchable.go: %v", err)
	}

	v := NewStaticPathValidator([]string{root})
	return root, v
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// ==========================================
// read_file tests
// ==========================================

func TestReadFile_ReturnsContents(t *testing.T) {
	root, v := setupFilesystemTestFixtures(t)
	tool := NewReadFileTool(v)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path": filepath.Join(root, "readme.txt"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data, ok := res.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", res.Data)
	}

	content, ok := data["content"].(string)
	if !ok || !strings.Contains(content, "line1") {
		t.Errorf("expected file content containing 'line1', got: %s", content)
	}
}

func TestReadFile_WithLineRange(t *testing.T) {
	root, v := setupFilesystemTestFixtures(t)
	tool := NewReadFileTool(v)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path":      filepath.Join(root, "many_lines.txt"),
		"startLine": float64(5),
		"endLine":   float64(10),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	content := data["content"].(string)

	// Should contain lines 5-10
	if !strings.Contains(content, "line 5") {
		t.Errorf("expected content to contain 'line 5', got: %s", content)
	}
	if !strings.Contains(content, "line 10") {
		t.Errorf("expected content to contain 'line 10', got: %s", content)
	}

	// Should NOT contain lines outside range
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) != 6 { // lines 5,6,7,8,9,10
		t.Errorf("expected 6 lines, got %d: %v", len(lines), lines)
	}
}

func TestReadFile_CapsAt200Lines(t *testing.T) {
	root, v := setupFilesystemTestFixtures(t)
	tool := NewReadFileTool(v)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path": filepath.Join(root, "many_lines.txt"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	lineCount := data["lineCount"].(float64)
	totalLines := data["totalLines"].(float64)

	if lineCount != 200 {
		t.Errorf("expected 200 lines returned, got %v", lineCount)
	}
	if totalLines != 250 {
		t.Errorf("expected 250 total lines, got %v", totalLines)
	}

	// Should have a hint about truncation
	if res.Hint == "" {
		t.Error("expected a hint about truncated content")
	}
}

func TestReadFile_RejectsInvalidPath(t *testing.T) {
	_, v := setupFilesystemTestFixtures(t)
	outside := outsideDir(t)
	tool := NewReadFileTool(v)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path": filepath.Join(outside, "secret.txt"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if res.Success {
		t.Fatal("expected failure for path outside allowed roots")
	}
}

func TestReadFile_PDFExtraction(t *testing.T) {
	root, v := setupFilesystemTestFixtures(t)
	tool := NewReadFileTool(v)

	// Write minimal PDF
	pdfPath := filepath.Join(root, "test.pdf")
	pdfBytes := buildPDFBytes()
	if err := os.WriteFile(pdfPath, pdfBytes, 0644); err != nil {
		t.Fatalf("failed to write test.pdf: %v", err)
	}
	defer os.Remove(pdfPath)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path": pdfPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	content := data["content"].(string)

	if !strings.Contains(content, "Hello World") {
		t.Errorf("expected PDF content containing 'Hello World', got: %s", content)
	}
}

// ==========================================
// list_dir tests
// ==========================================

func TestListDir_ReturnsEntries(t *testing.T) {
	root, v := setupFilesystemTestFixtures(t)
	tool := NewListDirTool(v)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path": root,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	entries, ok := data["entries"].([]interface{})
	if !ok {
		t.Fatalf("expected entries array, got %T", data["entries"])
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one directory entry")
	}

	// Check that entries have expected metadata
	entry := entries[0].(map[string]interface{})
	if _, ok := entry["name"]; !ok {
		t.Error("entry missing 'name' field")
	}
	if _, ok := entry["type"]; !ok {
		t.Error("entry missing 'type' field")
	}
}

func TestListDir_RejectsInvalidPath(t *testing.T) {
	_, v := setupFilesystemTestFixtures(t)
	outside := outsideDir(t)
	tool := NewListDirTool(v)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path": outside,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if res.Success {
		t.Fatal("expected failure for path outside allowed roots")
	}
}

// ==========================================
// search_files tests
// ==========================================

func TestSearchFiles_FindsMatchingLines(t *testing.T) {
	root, v := setupFilesystemTestFixtures(t)
	tool := NewSearchFilesTool(v)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path":    root,
		"pattern": "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	matches, ok := data["matches"].([]interface{})
	if !ok || len(matches) == 0 {
		t.Fatal("expected at least one match for 'hello'")
	}

	match := matches[0].(map[string]interface{})
	if _, ok := match["file"]; !ok {
		t.Error("match missing 'file' field")
	}
	if _, ok := match["line"]; !ok {
		t.Error("match missing 'line' field")
	}
	if _, ok := match["content"]; !ok {
		t.Error("match missing 'content' field")
	}
}

func TestSearchFiles_ReturnsEmptyForNoMatch(t *testing.T) {
	root, v := setupFilesystemTestFixtures(t)
	tool := NewSearchFilesTool(v)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path":    root,
		"pattern": "zzz_nonexistent_pattern_zzz",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	matches := data["matches"].([]interface{})
	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matches))
	}
}

// ==========================================
// Registration test
// ==========================================

func TestFilesystemTools_RegisteredInRegistry(t *testing.T) {
	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_fs_tools_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_fs_tools_test.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init DB: %v", err)
	}

	// Set TZRO_DIR so the tools know what root to use
	root := testdataDir(t)
	originalDir := os.Getenv("TZRO_DIR")
	os.Setenv("TZRO_DIR", root)
	defer os.Setenv("TZRO_DIR", originalDir)

	if err := Init(""); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// All three should be registered
	for _, name := range []string{"read_file", "list_dir", "search_files"} {
		tool := GetTool(name)
		if tool == nil {
			t.Errorf("expected tool '%s' to be registered", name)
		}
	}
}
