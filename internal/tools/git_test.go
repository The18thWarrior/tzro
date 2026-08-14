package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupGitTestRepo creates a temporary git repository with initial commits for testing.
func setupGitTestRepo(t *testing.T) (string, *PathValidator) {
	t.Helper()
	dir := t.TempDir()

	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test User",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test User",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	runGit("init")
	runGit("config", "user.name", "Test User")
	runGit("config", "user.email", "test@example.com")

	// Commit 1: Initial commit
	if err := os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("initial content\n"), 0644); err != nil {
		t.Fatalf("failed to write file1.txt: %v", err)
	}
	runGit("add", "file1.txt")
	runGit("commit", "-m", "Initial commit")

	// Commit 2: Add file2
	if err := os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("second file\n"), 0644); err != nil {
		t.Fatalf("failed to write file2.txt: %v", err)
	}
	runGit("add", "file2.txt")
	runGit("commit", "-m", "Add file2")

	// Commit 3: Update file1
	if err := os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("initial content\nupdated line\n"), 0644); err != nil {
		t.Fatalf("failed to update file1.txt: %v", err)
	}
	runGit("add", "file1.txt")
	runGit("commit", "-m", "Update file1")

	v := NewStaticPathValidator([]string{dir})
	return dir, v
}

// RED: TestGitLogTool_BasicHistory
func TestGitLogTool_BasicHistory(t *testing.T) {
	dir, v := setupGitTestRepo(t)
	tool := NewGitLogTool(v)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path": dir,
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

	data, ok := res.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", res.Data)
	}

	output, ok := data["output"].(string)
	if !ok || output == "" {
		t.Fatalf("expected non-empty output string, got %v", data["output"])
	}

	if !strings.Contains(output, "Initial commit") || !strings.Contains(output, "Add file2") || !strings.Contains(output, "Update file1") {
		t.Errorf("expected git log output to contain all commit messages, got:\n%s", output)
	}

	count, ok := data["commitCount"].(float64)
	if !ok || count != 3 {
		t.Errorf("expected commitCount=3, got %v", data["commitCount"])
	}
}

func TestGitLogTool_PathScoped(t *testing.T) {
	dir, v := setupGitTestRepo(t)
	tool := NewGitLogTool(v)

	// file2.txt was only touched in Commit 2 ("Add file2")
	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path": filepath.Join(dir, "file2.txt"),
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
	output := data["output"].(string)
	if !strings.Contains(output, "Add file2") {
		t.Errorf("expected output to contain 'Add file2', got:\n%s", output)
	}
	if strings.Contains(output, "Update file1") {
		t.Errorf("expected output to NOT contain 'Update file1' for file2.txt, got:\n%s", output)
	}
}

func TestGitLogTool_ZeroArgs(t *testing.T) {
	dir, v := setupGitTestRepo(t)
	tool := NewGitLogTool(v)

	// Set TZRO_DIR to the test git dir so zero-args resolves to this repo
	t.Setenv("TZRO_DIR", dir)

	result, err := tool.Call(context.Background(), map[string]interface{}{})
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
	output := data["output"].(string)
	if output == "" || !strings.Contains(output, "Initial commit") {
		t.Errorf("expected valid git log output with zero args, got:\n%s", output)
	}
}

func TestGitLogTool_MaxCountCap(t *testing.T) {
	dir, v := setupGitTestRepo(t)
	tool := NewGitLogTool(v)

	// Request 100 commits (exceeds cap of 50 -> should use --oneline format)
	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path":     dir,
		"maxCount": 100,
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
	output := data["output"].(string)
	// Oneline format does NOT have "commit " headers or "Author:" lines
	if strings.Contains(output, "Author:") {
		t.Errorf("expected --oneline format without 'Author:', got:\n%s", output)
	}
	if !strings.Contains(res.Hint, "capped at 50") {
		t.Errorf("expected hint mentioning capped at 50, got: %s", res.Hint)
	}
}

func TestGitDiffTool_WorkingTree(t *testing.T) {
	dir, v := setupGitTestRepo(t)
	tool := NewGitDiffTool(v)

	// Make uncommitted working tree modification
	if err := os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("initial content\nupdated line\nuncommitted edit\n"), 0644); err != nil {
		t.Fatalf("failed to modify file1.txt: %v", err)
	}

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path": dir,
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
	output := data["output"].(string)
	if !strings.Contains(output, "+uncommitted edit") {
		t.Errorf("expected diff to contain '+uncommitted edit', got:\n%s", output)
	}
}

func TestGitDiffTool_RefDiff(t *testing.T) {
	dir, v := setupGitTestRepo(t)
	tool := NewGitDiffTool(v)

	// Diff HEAD~2 against HEAD
	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path": dir,
		"ref":  "HEAD~2",
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
	output := data["output"].(string)
	if !strings.Contains(output, "file2.txt") {
		t.Errorf("expected diff against HEAD~2 to include file2.txt, got:\n%s", output)
	}
}

func TestGitDiffTool_StatFallback(t *testing.T) {
	dir, v := setupGitTestRepo(t)
	tool := NewGitDiffTool(v)

	// Create a large uncommitted change with 600 lines
	var bigContent strings.Builder
	for i := 1; i <= 600; i++ {
		bigContent.WriteString(fmt.Sprintf("large change line %d\n", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "bigfile.txt"), []byte(bigContent.String()), 0644); err != nil {
		t.Fatalf("failed to write bigfile.txt: %v", err)
	}

	// Add it to git index so git diff HEAD compares it
	cmd := exec.Command("git", "add", "bigfile.txt")
	cmd.Dir = dir
	_ = cmd.Run()

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"path": dir,
		"ref":  "HEAD",
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
	output := data["output"].(string)
	statOnly, _ := data["statOnly"].(bool)
	if !statOnly {
		t.Errorf("expected statOnly=true for >500 lines diff")
	}
	if !strings.Contains(output, "bigfile.txt") || !strings.Contains(output, "|") {
		t.Errorf("expected stat summary output with '|', got:\n%s", output)
	}
	if !strings.Contains(res.Hint, "Output was large") {
		t.Errorf("expected stat-first hint in result, got: %s", res.Hint)
	}
}

func TestGitShowTool_LatestCommit(t *testing.T) {
	dir, v := setupGitTestRepo(t)
	tool := NewGitShowTool(v)

	t.Setenv("TZRO_DIR", dir)

	// Zero-args should default to HEAD
	result, err := tool.Call(context.Background(), map[string]interface{}{})
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
	output := data["output"].(string)
	if !strings.Contains(output, "Update file1") {
		t.Errorf("expected latest commit 'Update file1', got:\n%s", output)
	}
}

func TestGitShowTool_InvalidRef(t *testing.T) {
	dir, v := setupGitTestRepo(t)
	tool := NewGitShowTool(v)

	t.Setenv("TZRO_DIR", dir)

	result, err := tool.Call(context.Background(), map[string]interface{}{
		"ref": "nonexistent_ref_12345",
	})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}

	var res ToolResult
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if res.Success {
		t.Fatalf("expected failure for invalid ref, got success")
	}
	if !strings.Contains(res.Error, "failed") {
		t.Errorf("expected error message for invalid ref, got: %s", res.Error)
	}
}

func TestGitShowTool_StatFallback(t *testing.T) {
	dir, v := setupGitTestRepo(t)
	tool := NewGitShowTool(v)

	t.Setenv("TZRO_DIR", dir)

	// Create a commit with 600 lines
	var bigContent strings.Builder
	for i := 1; i <= 600; i++ {
		bigContent.WriteString(fmt.Sprintf("commit line %d\n", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(bigContent.String()), 0644); err != nil {
		t.Fatalf("failed to write large.txt: %v", err)
	}
	cmd1 := exec.Command("git", "add", "large.txt")
	cmd1.Dir = dir
	_ = cmd1.Run()
	cmd2 := exec.Command("git", "commit", "-m", "Large commit")
	cmd2.Dir = dir
	cmd2.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@t.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@t.com")
	_ = cmd2.Run()

	result, err := tool.Call(context.Background(), map[string]interface{}{})
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
	statOnly, _ := data["statOnly"].(bool)
	if !statOnly {
		t.Errorf("expected statOnly=true for >500 lines git_show")
	}
	if !strings.Contains(res.Hint, "Output was large") {
		t.Errorf("expected stat-first hint in git_show, got: %s", res.Hint)
	}
}

func TestGitTools_NotARepo(t *testing.T) {
	nonGitDir := t.TempDir()
	v := NewStaticPathValidator([]string{nonGitDir})

	logTool := NewGitLogTool(v)
	result, err := logTool.Call(context.Background(), map[string]interface{}{
		"path": nonGitDir,
	})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}

	var res ToolResult
	_ = json.Unmarshal([]byte(result), &res)
	if res.Success {
		t.Errorf("expected error when calling git_log on non-git directory")
	}
	if !strings.Contains(res.Error, "Not a git repository") {
		t.Errorf("expected 'Not a git repository' error, got: %s", res.Error)
	}
}

func TestGitTools_PathValidation(t *testing.T) {
	dir, _ := setupGitTestRepo(t)
	outsideDir := t.TempDir()
	v := NewStaticPathValidator([]string{dir}) // only `dir` allowed

	logTool := NewGitLogTool(v)
	result, err := logTool.Call(context.Background(), map[string]interface{}{
		"path": outsideDir,
	})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}

	var res ToolResult
	_ = json.Unmarshal([]byte(result), &res)
	if res.Success {
		t.Errorf("expected error when accessing path outside allowed roots")
	}
	if !strings.Contains(res.Error, "path validation failed") && !strings.Contains(res.Error, "outside all allowed roots") {
		t.Errorf("expected path validation failure, got: %s", res.Error)
	}
}
