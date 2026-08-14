package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/internal/inference"
	"tzro/internal/memory"
)

func setupFilesystemTestFixtures(t *testing.T) (string, *PathValidator) {
	t.Helper()
	root := testdataDir(t)

	// Create a file with many lines for line-range and cap tests
	manyLinesPath := filepath.Join(root, "many_lines.txt")
	var lines []string
	for i := 1; i <= 600; i++ {
		lines = append(lines, "line "+string(rune('0'+(i/100)%10))+string(rune('0'+(i/10)%10))+string(rune('0'+i%10)))
	}
	// Use a simpler approach — write numbered lines
	var content strings.Builder
	for i := 1; i <= 600; i++ {
		fmt.Fprintf(&content, "line %s\n", intToStr(i))
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

func TestReadFile_CapsAt500Lines(t *testing.T) {
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

	if lineCount != 500 {
		t.Errorf("expected 500 lines returned, got %v", lineCount)
	}
	if totalLines != 600 {
		t.Errorf("expected 600 total lines, got %v", totalLines)
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

func TestSearchFiles_FileGlob(t *testing.T) {
	root, v := setupFilesystemTestFixtures(t)
	tool := NewSearchFilesTool(v)

	// In setupFilesystemTestFixtures, searchable.go has "world" and readme.txt has "line1"
	// Create another file with "world" but with .txt extension
	if err := os.WriteFile(filepath.Join(root, "world.txt"), []byte("hello world in txt\n"), 0644); err != nil {
		t.Fatalf("failed to create world.txt: %v", err)
	}

	// Search for "world" scoped only to *.go
	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path":     root,
		"pattern":  "world",
		"fileGlob": "*.go",
	})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
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
	if len(matches) == 0 {
		t.Fatalf("expected matches in .go files")
	}

	for _, m := range matches {
		matchMap := m.(map[string]interface{})
		fileName := matchMap["file"].(string)
		if !strings.HasSuffix(fileName, ".go") {
			t.Errorf("expected only .go files with fileGlob='*.go', got: %s", fileName)
		}
	}
}

func TestSearchFiles_GoFallback(t *testing.T) {
	root, v := setupFilesystemTestFixtures(t)
	tool := NewSearchFilesTool(v)

	// Force disable ripgrep by overriding hook / testing Go fallback explicitly
	oldRg := rgOverridePath
	rgOverridePath = "nonexistent_rg_binary"
	defer func() { rgOverridePath = oldRg }()

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path":     root,
		"pattern":  "hello",
		"fileGlob": "*.go",
	})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
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
	if len(matches) == 0 {
		t.Fatalf("expected matches in Go fallback")
	}
	for _, m := range matches {
		matchMap := m.(map[string]interface{})
		fileName := matchMap["file"].(string)
		if !strings.HasSuffix(fileName, ".go") {
			t.Errorf("expected only .go matches in fallback, got: %s", fileName)
		}
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

	// All four should be registered
	for _, name := range []string{"read_file", "list_dir", "search_files", "peek_file"} {
		tool := GetTool(name)
		if tool == nil {
			t.Errorf("expected tool '%s' to be registered", name)
		}
	}
}

// ==========================================
// Anti-anchoring defense tests (L0-L3)
// ==========================================

// resolvedTempDir returns a temp directory with symlinks resolved.
// On macOS, t.TempDir() returns /var/folders/... which is a symlink
// to /private/var/folders/... — the PathValidator resolves symlinks
// for security, so the root must match the resolved path.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("failed to resolve temp dir symlinks: %v", err)
	}
	return resolved
}

func TestListDir_FiltersNoisyEntries(t *testing.T) {
	// Create a temp directory with noisy + real entries
	root := resolvedTempDir(t)
	v := NewStaticPathValidator([]string{root})

	// Create noisy entries
	os.Mkdir(filepath.Join(root, "node_modules"), 0755)
	os.Mkdir(filepath.Join(root, ".git"), 0755)
	os.Mkdir(filepath.Join(root, "__pycache__"), 0755)
	os.WriteFile(filepath.Join(root, ".DS_Store"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(root, "Thumbs.db"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(root, "~$document.docx"), []byte("x"), 0644)

	// Create real entries
	os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test"), 0644)
	os.Mkdir(filepath.Join(root, "internal"), 0755)

	tool := NewListDirTool(v)
	result, err := tool.Call(context.Background(), map[string]interface{}{"path": root})
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
	entries := data["entries"].([]interface{})

	// Should only see real entries
	if len(entries) != 3 {
		t.Errorf("expected 3 visible entries, got %d", len(entries))
	}

	// Verify hidden count
	hiddenCount := int(data["hiddenCount"].(float64))
	if hiddenCount != 6 {
		t.Errorf("expected 6 hidden entries, got %d", hiddenCount)
	}

	// Verify none of the noisy entries are present
	for _, e := range entries {
		entry := e.(map[string]interface{})
		name := entry["name"].(string)
		if isNoisyEntry(name, name, nil) {
			t.Errorf("noisy entry '%s' should have been filtered", name)
		}
	}

	// Verify hint mentions hidden entries
	if res.Hint == "" {
		t.Error("expected hint about hidden entries")
	}
}

func TestListDir_IncludesProfile(t *testing.T) {
	root := resolvedTempDir(t)
	v := NewStaticPathValidator([]string{root})

	// Create files with various extensions
	for i := 0; i < 5; i++ {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("file%d.go", i)), []byte("pkg"), 0644)
	}
	for i := 0; i < 3; i++ {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("file%d.rs", i)), []byte("fn"), 0644)
	}
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test"), 0644)
	os.Mkdir(filepath.Join(root, "cmd"), 0755)

	tool := NewListDirTool(v)
	result, err := tool.Call(context.Background(), map[string]interface{}{"path": root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	json.Unmarshal([]byte(result), &res)

	data := res.Data.(map[string]interface{})
	profile, ok := data["profile"].(string)
	if !ok || profile == "" {
		t.Fatal("expected non-empty profile string")
	}

	// Profile should mention .go (most common), .rs, and directories
	if !strings.Contains(profile, ".go") {
		t.Errorf("profile should mention .go: got %q", profile)
	}
	if !strings.Contains(profile, ".rs") {
		t.Errorf("profile should mention .rs: got %q", profile)
	}
	if !strings.Contains(profile, "directories") {
		t.Errorf("profile should mention directories: got %q", profile)
	}
}

func TestListDir_TruncatesLargeDirectories(t *testing.T) {
	root := resolvedTempDir(t)
	v := NewStaticPathValidator([]string{root})

	// Create 150 files (above the 100 entry limit)
	for i := 0; i < 150; i++ {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("file_%03d.txt", i)), []byte("x"), 0644)
	}

	tool := NewListDirTool(v)
	result, err := tool.Call(context.Background(), map[string]interface{}{"path": root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	json.Unmarshal([]byte(result), &res)

	data := res.Data.(map[string]interface{})
	entryCount := int(data["entryCount"].(float64))
	totalCount := int(data["totalCount"].(float64))

	if entryCount != 100 {
		t.Errorf("expected 100 entries after truncation, got %d", entryCount)
	}
	if totalCount != 150 {
		t.Errorf("expected totalCount=150, got %d", totalCount)
	}

	// Profile should still reflect ALL 150 files
	profile := data["profile"].(string)
	if !strings.Contains(profile, "150 .txt") {
		t.Errorf("profile should show all 150 .txt files even after truncation: got %q", profile)
	}

	// Hint should mention truncation
	if !strings.Contains(res.Hint, "Showing first 100") {
		t.Errorf("expected truncation hint, got %q", res.Hint)
	}
}

func TestPeekFile_ReturnsFirst20Lines(t *testing.T) {
	root := resolvedTempDir(t)
	v := NewStaticPathValidator([]string{root})

	// Create a 50-line file
	var content strings.Builder
	for i := 1; i <= 50; i++ {
		content.WriteString(fmt.Sprintf("line %d\n", i))
	}
	filePath := filepath.Join(root, "big.go")
	os.WriteFile(filePath, []byte(content.String()), 0644)

	tool := NewPeekFileTool(v)
	result, err := tool.Call(context.Background(), map[string]interface{}{"path": filePath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	json.Unmarshal([]byte(result), &res)
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	lineCount := int(data["lineCount"].(float64))
	if lineCount != 20 {
		t.Errorf("expected 20 lines, got %d", lineCount)
	}

	// Should have truncation hint
	if res.Hint == "" {
		t.Error("expected hint about file continuing")
	}

	// Content should contain line 1 but not line 21
	fileContent := data["content"].(string)
	if !strings.Contains(fileContent, "line 1\n") {
		t.Error("expected content to contain 'line 1'")
	}
	if strings.Contains(fileContent, "line 21") {
		t.Error("content should NOT contain 'line 21'")
	}
}

func TestPeekFile_ShortFile(t *testing.T) {
	root := resolvedTempDir(t)
	v := NewStaticPathValidator([]string{root})

	// Create a 5-line file
	filePath := filepath.Join(root, "short.txt")
	os.WriteFile(filePath, []byte("a\nb\nc\nd\ne\n"), 0644)

	tool := NewPeekFileTool(v)
	result, err := tool.Call(context.Background(), map[string]interface{}{"path": filePath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	json.Unmarshal([]byte(result), &res)
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	lineCount := int(data["lineCount"].(float64))
	if lineCount != 5 {
		t.Errorf("expected 5 lines, got %d", lineCount)
	}

	// Should NOT have truncation hint for short files
	if res.Hint != "" {
		t.Errorf("expected no hint for short file, got %q", res.Hint)
	}
}

func TestSearchFiles_SkipsNoisyDirs(t *testing.T) {
	root := resolvedTempDir(t)
	v := NewStaticPathValidator([]string{root})

	// Create a matching file inside node_modules
	nmDir := filepath.Join(root, "node_modules", "some_pkg")
	os.MkdirAll(nmDir, 0755)
	os.WriteFile(filepath.Join(nmDir, "index.js"), []byte("export function UNIQUE_MARKER() {}"), 0644)

	// Create a matching file in a real directory
	srcDir := filepath.Join(root, "src")
	os.MkdirAll(srcDir, 0755)
	os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("func UNIQUE_MARKER() {}"), 0644)

	tool := NewSearchFilesTool(v)
	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path":    root,
		"pattern": "UNIQUE_MARKER",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	json.Unmarshal([]byte(result), &res)
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	matches := data["matches"].([]interface{})

	// Should find the match in src/ but NOT in node_modules/
	if len(matches) != 1 {
		t.Errorf("expected exactly 1 match (from src/), got %d", len(matches))
	}

	if len(matches) > 0 {
		match := matches[0].(map[string]interface{})
		file := match["file"].(string)
		if strings.Contains(file, "node_modules") {
			t.Errorf("match should not be from node_modules: %s", file)
		}
	}
}

// ==========================================
// write_file tests
// ==========================================

func TestWriteFile_CreatesNewFile(t *testing.T) {
	tmpDir := t.TempDir()
	v := NewStaticPathValidator([]string{tmpDir})
	tool := NewWriteFileTool(v)

	targetPath := filepath.Join(tmpDir, "hello.go")
	content := "package hello\n\nfunc Hello() string {\n\treturn \"hello\"\n}\n"

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path":    targetPath,
		"content": content,
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
	if data["action"] != "created" {
		t.Errorf("expected action 'created', got %v", data["action"])
	}

	// Verify file on disk
	diskContent, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(diskContent) != content {
		t.Errorf("content mismatch: got %q, want %q", string(diskContent), content)
	}
}

func TestWriteFile_CreatesParentDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	v := NewStaticPathValidator([]string{tmpDir})
	tool := NewWriteFileTool(v)

	targetPath := filepath.Join(tmpDir, "deep", "nested", "dir", "file.txt")
	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path":    targetPath,
		"content": "hello\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	json.Unmarshal([]byte(result), &res)
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	if data["action"] != "created" {
		t.Errorf("expected action 'created', got %v", data["action"])
	}

	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		t.Fatalf("file not created with nested directories")
	}
}

func TestWriteFile_OverwriteReportsUpdated(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir) // macOS: /var -> /private/var
	os.Setenv("TZRO_DIR", tmpDir)
	defer os.Unsetenv("TZRO_DIR")

	v := NewStaticPathValidator([]string{tmpDir})
	tool := NewWriteFileTool(v)

	targetPath := filepath.Join(tmpDir, "existing.go")
	originalContent := "package original\n"
	os.WriteFile(targetPath, []byte(originalContent), 0644)

	newContent := "package updated\n"
	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path":    targetPath,
		"content": newContent,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	json.Unmarshal([]byte(result), &res)
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	if data["action"] != "updated" {
		t.Errorf("expected action 'updated', got %v", data["action"])
	}

	// Verify backup was created
	backupDir := filepath.Join(tmpDir, ".tzro", "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("backup directory not created: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("no backup files created")
	}

	// Verify backup contains original content
	backupContent, err := os.ReadFile(filepath.Join(backupDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("failed to read backup: %v", err)
	}
	if string(backupContent) != originalContent {
		t.Errorf("backup content mismatch: got %q, want %q", string(backupContent), originalContent)
	}
}

func TestWriteFile_RejectsPathOutsideWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	v := NewStaticPathValidator([]string{tmpDir})
	tool := NewWriteFileTool(v)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path":    "/etc/passwd",
		"content": "hacked\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	json.Unmarshal([]byte(result), &res)
	if res.Success {
		t.Errorf("expected success=false for path outside workspace")
	}
	if !strings.Contains(res.Error, "path validation failed") {
		t.Errorf("expected path validation error, got: %s", res.Error)
	}
}

func TestWriteFile_RejectsBinaryContent(t *testing.T) {
	tmpDir := t.TempDir()
	v := NewStaticPathValidator([]string{tmpDir})
	tool := NewWriteFileTool(v)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path":    filepath.Join(tmpDir, "binary.bin"),
		"content": "hello\x00world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	json.Unmarshal([]byte(result), &res)
	if res.Success {
		t.Errorf("expected success=false for binary content")
	}
	if !strings.Contains(res.Error, "binary content not allowed") {
		t.Errorf("expected binary content error, got: %s", res.Error)
	}
}

func TestWriteFile_BackupLRUEviction(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("TZRO_DIR", tmpDir)
	defer os.Unsetenv("TZRO_DIR")

	// Pre-create 50 backup files
	backupDir := filepath.Join(tmpDir, ".tzro", "backups")
	os.MkdirAll(backupDir, 0755)
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("backup_%02d.bak", i)
		os.WriteFile(filepath.Join(backupDir, name), []byte("old"), 0644)
	}

	v := NewStaticPathValidator([]string{tmpDir})
	tool := NewWriteFileTool(v)

	// Create and overwrite a file to trigger backup + eviction
	targetPath := filepath.Join(tmpDir, "eviction_test.go")
	os.WriteFile(targetPath, []byte("original\n"), 0644)

	_, err := tool.Call(context.Background(), map[string]interface{}{
		"path":    targetPath,
		"content": "updated\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Count backups — should be <= 50 after eviction
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("failed to read backup dir: %v", err)
	}
	bakCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".bak") {
			bakCount++
		}
	}
	if bakCount > 50 {
		t.Errorf("expected <= 50 backups after LRU eviction, got %d", bakCount)
	}
}

func TestWriteFile_CountsLines(t *testing.T) {
	tmpDir := t.TempDir()
	v := NewStaticPathValidator([]string{tmpDir})
	tool := NewWriteFileTool(v)

	// 5 lines with trailing newline
	content := "line1\nline2\nline3\nline4\nline5\n"
	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path":    filepath.Join(tmpDir, "counted.txt"),
		"content": content,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	json.Unmarshal([]byte(result), &res)
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	linesWritten := data["linesWritten"].(float64)
	if linesWritten != 5 {
		t.Errorf("expected 5 lines, got %v", linesWritten)
	}
}

// ── Phase 3: read_file tabular routing integration tests ─────────────────

func TestReadFile_CSVRoute_ReturnsProfile(t *testing.T) {
	// Setup DB for cache.StoreFileRef
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_readfile_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_readfile_test.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
		_ = memory.DB.Init()
	}()
	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init test db: %v", err)
	}

	tmpDir := t.TempDir()
	// Resolve symlinks to match PathValidator's internal resolution (macOS /var → /private/var)
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	csvPath := filepath.Join(tmpDir, "leads.csv")
	csvContent := "name,email,status,score\nAlice,alice@test.com,active,95\nBob,bob@test.com,pending,87\nCharlie,charlie@test.com,active,100\n"
	if err := os.WriteFile(csvPath, []byte(csvContent), 0644); err != nil {
		t.Fatal(err)
	}

	v := NewPathValidator([]string{tmpDir})
	tool := NewReadFileTool(v)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path": csvPath,
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

	// Should have dataProfile (not raw content)
	if _, hasProfile := data["dataProfile"]; !hasProfile {
		t.Error("expected 'dataProfile' key in response for CSV file")
	}

	// Should NOT have raw content key
	if _, hasContent := data["content"]; hasContent {
		t.Error("CSV file should NOT return raw 'content', should return dataProfile")
	}

	// Should have cacheId
	if _, hasCacheID := data["cacheId"]; !hasCacheID {
		t.Error("expected 'cacheId' key in response for CSV file")
	}

	// Should have hint
	if res.Hint == "" {
		t.Error("expected hint about tabular data tools")
	}
}

func TestReadFile_RegularFile_StillReturnsRawContent(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	txtPath := filepath.Join(tmpDir, "readme.txt")
	if err := os.WriteFile(txtPath, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	v := NewPathValidator([]string{tmpDir})
	tool := NewReadFileTool(v)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path": txtPath,
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

	// Should have raw content for regular text files
	content, ok := data["content"].(string)
	if !ok || !strings.Contains(content, "hello world") {
		t.Errorf("expected raw content for txt file, got: %v", data)
	}

	// Should NOT have dataProfile
	if _, hasProfile := data["dataProfile"]; hasProfile {
		t.Error("regular text file should NOT return dataProfile")
	}
}

// ==========================================
// Goal-directed file compaction tests
// ==========================================

// TestReadFile_GoalCompaction_SmallFile_ReturnsRaw verifies that files
// at or below the compaction threshold are returned raw even when a
// probe goal is present in context.
func TestReadFile_GoalCompaction_SmallFile_ReturnsRaw(t *testing.T) {
	root, v := setupFilesystemTestFixtures(t)
	tool := NewReadFileTool(v)

	// Create a file with exactly 50 lines (under threshold of 100)
	smallPath := filepath.Join(root, "small_code.go")
	var content strings.Builder
	for i := 1; i <= 50; i++ {
		content.WriteString(fmt.Sprintf("func handler%d() { return }\n", i))
	}
	if err := os.WriteFile(smallPath, []byte(content.String()), 0644); err != nil {
		t.Fatalf("failed to create small_code.go: %v", err)
	}

	// Call with goal in context
	ctx := context.WithValue(context.Background(), FileReadGoalKey, "Explore the architecture")
	result, err := tool.Call(ctx, map[string]interface{}{
		"path": smallPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	json.Unmarshal([]byte(result), &res)
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	fileContent := data["content"].(string)

	// Should contain raw function definitions, not compressed output
	if !strings.Contains(fileContent, "func handler1()") {
		t.Error("small file should return raw content including 'func handler1()'")
	}
	if !strings.Contains(fileContent, "func handler50()") {
		t.Error("small file should return raw content including 'func handler50()'")
	}
	// Should NOT have the goal-compressed header
	if strings.Contains(fileContent, "goal-compressed") {
		t.Error("small file should NOT be goal-compressed")
	}
	// Hint should NOT mention goal-compression
	if strings.Contains(res.Hint, "Goal-compressed") {
		t.Error("hint should NOT mention goal-compression for small files")
	}
}

// TestReadFile_GoalCompaction_LargeFile_ReturnsCompressed verifies that files
// exceeding the compaction threshold are goal-compressed when a probe goal is
// present. This is an integration test that requires the router sidecar.
func TestReadFile_GoalCompaction_LargeFile_ReturnsCompressed(t *testing.T) {
	// Skip if router sidecar is not available
	_, err := inference.CallRouter(context.Background(), []inference.InferenceMessage{
		{Role: "user", Content: "hello"},
	}, "")
	if err != nil {
		t.Skip("Skipping: router sidecar not available")
	}

	root, v := setupFilesystemTestFixtures(t)
	tool := NewReadFileTool(v)

	// Create a file with 200 lines (above threshold of 100)
	largePath := filepath.Join(root, "large_code.go")
	var content strings.Builder
	content.WriteString("package main\n\n")
	for i := 1; i <= 198; i++ {
		content.WriteString(fmt.Sprintf("func Process%d(input string) string { return input + \"%d\" }\n", i, i))
	}
	if err := os.WriteFile(largePath, []byte(content.String()), 0644); err != nil {
		t.Fatalf("failed to create large_code.go: %v", err)
	}

	// Call with goal in context
	ctx := context.WithValue(context.Background(), FileReadGoalKey, "Find exported function signatures and their return types")
	result, err := tool.Call(ctx, map[string]interface{}{
		"path": largePath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	json.Unmarshal([]byte(result), &res)
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	fileContent := data["content"].(string)

	// Should be compressed — content should be significantly shorter than raw
	rawLen := len(content.String())
	if len(fileContent) >= rawLen {
		t.Errorf("compressed content (%d chars) should be shorter than raw (%d chars)", len(fileContent), rawLen)
	}

	// Hint should mention goal-compression
	if !strings.Contains(res.Hint, "Goal-compressed") {
		t.Errorf("hint should mention goal-compression, got: %s", res.Hint)
	}

	t.Logf("Compression: %d raw → %d compressed (%.1f%% reduction)",
		rawLen, len(fileContent), 100*(1-float64(len(fileContent))/float64(rawLen)))
}

// TestReadFile_NoGoal_LargeFile_ReturnsRaw verifies backward compatibility:
// large files without a probe goal in context are returned raw.
func TestReadFile_NoGoal_LargeFile_ReturnsRaw(t *testing.T) {
	root, v := setupFilesystemTestFixtures(t)
	tool := NewReadFileTool(v)

	// Create a file with 200 lines (above threshold)
	largePath := filepath.Join(root, "large_raw.go")
	var content strings.Builder
	content.WriteString("package main\n\n")
	for i := 1; i <= 198; i++ {
		content.WriteString(fmt.Sprintf("func Handler%d() { }\n", i))
	}
	if err := os.WriteFile(largePath, []byte(content.String()), 0644); err != nil {
		t.Fatalf("failed to create large_raw.go: %v", err)
	}

	// Call WITHOUT goal in context (plain background context)
	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path": largePath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var res ToolResult
	json.Unmarshal([]byte(result), &res)
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	data := res.Data.(map[string]interface{})
	fileContent := data["content"].(string)

	// Should contain raw function definitions
	if !strings.Contains(fileContent, "func Handler1()") {
		t.Error("large file without goal should return raw content with 'func Handler1()'")
	}
	if !strings.Contains(fileContent, "func Handler198()") {
		t.Error("large file without goal should return raw content with 'func Handler198()'")
	}
	// Should NOT have any compaction hint
	if strings.Contains(res.Hint, "Goal-compressed") {
		t.Error("hint should NOT mention goal-compression when no goal is present")
	}
}

// TestDeterministicTruncate_FallbackFormat verifies the deterministic
// truncation fallback produces the correct format with first/last 20 lines.
func TestDeterministicTruncate_FallbackFormat(t *testing.T) {
	// Build 100 lines of input
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}

	result := deterministicTruncate(lines)

	// Should contain first 20 lines
	if !strings.Contains(result, "line 1\n") {
		t.Error("should contain 'line 1'")
	}
	if !strings.Contains(result, "line 20\n") {
		t.Error("should contain 'line 20'")
	}

	// Should contain omission marker
	if !strings.Contains(result, "[... 60 lines omitted ...]") {
		t.Error("should contain omission marker for 60 lines (100 - 20 - 20)")
	}

	// Should contain last 20 lines
	if !strings.Contains(result, "line 81") {
		t.Error("should contain 'line 81' (first of last 20)")
	}
	if !strings.Contains(result, "line 100") {
		t.Error("should contain 'line 100'")
	}

	// Should NOT contain middle lines
	if strings.Contains(result, "line 21\n") {
		t.Error("should NOT contain 'line 21' (omitted)")
	}
	if strings.Contains(result, "line 80\n") {
		t.Error("should NOT contain 'line 80' (omitted)")
	}

	// Edge case: short input (under 40 lines) should return all lines unchanged
	shortLines := []string{"a", "b", "c"}
	shortResult := deterministicTruncate(shortLines)
	if shortResult != "a\nb\nc" {
		t.Errorf("short input should return all lines unchanged, got: %q", shortResult)
	}
}
