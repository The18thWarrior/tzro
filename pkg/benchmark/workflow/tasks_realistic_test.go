package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRealisticTasks_Validation(t *testing.T) {
	tasks := RealisticTasks()
	if len(tasks) != 4 {
		t.Fatalf("expected 4 realistic tasks, got %d", len(tasks))
	}

	task5 := tasks[0]
	if task5.ID != "macro_5_large_file_skeleton" {
		t.Fatalf("expected task 5 to be macro_5_large_file_skeleton, got %s", task5.ID)
	}
	serviceGo := task5.Files["service.go"]
	lineCount := len(strings.Split(serviceGo, "\n"))
	if lineCount < 500 {
		t.Errorf("expected service.go to have at least 500 lines, got %d", lineCount)
	}

	all := DefaultTasks()
	if len(all) != 8 {
		t.Errorf("expected 8 total tasks in DefaultTasks, got %d", len(all))
	}
}

func TestRealisticTasks_InitialFailureAndResolution(t *testing.T) {
	// For each task, verify it fails initially, then passes when the intended fix is applied.
	cases := []struct {
		taskID string
		fixFn  func(map[string]string)
	}{
		{
			taskID: "macro_5_large_file_skeleton",
			fixFn: func(files map[string]string) {
				files["service.go"] = strings.Replace(
					files["service.go"],
					"case \"enterprise\":\n\t\t// BUG: returns 0 instead of 20% discount\n\t\treturn 0",
					"case \"enterprise\":\n\t\treturn amountCents * 20 / 100",
					1,
				)
			},
		},
		{
			taskID: "macro_6_verbose_log_compact",
			fixFn: func(files map[string]string) {
				files["pipeline.go"] = strings.Replace(
					files["pipeline.go"],
					"panic(\"empty item encountered in pipeline stage 10: index out of bounds\")",
					"return \"EMPTY\"",
					1,
				)
			},
		},
		{
			taskID: "macro_7_tabular_analysis",
			fixFn: func(files map[string]string) {
				files["analytics.go"] = strings.Replace(
					files["analytics.go"],
					"return 0.0",
					"return 3450.00",
					1,
				)
			},
		},
		{
			taskID: "macro_8_multi_pkg_discovery",
			fixFn: func(files map[string]string) {
				files["pkg/auth/authenticator.go"] = strings.Replace(
					files["pkg/auth/authenticator.go"],
					"actualHash := \"\"",
					"actualHash := a.hasher.HashToken(rawToken)",
					1,
				)
			},
		},
	}

	allTasks := make(map[string]Task)
	for _, tsk := range RealisticTasks() {
		allTasks[tsk.ID] = tsk
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.taskID, func(t *testing.T) {
			task, ok := allTasks[tc.taskID]
			if !ok {
				t.Fatalf("task %s not found", tc.taskID)
			}

			// 1. Initial workspace should fail tests
			dir := t.TempDir()
			writeFiles(t, dir, task.Files)
			if runGoTest(t, dir) {
				t.Fatalf("expected initial tests for %s to fail, but passed", tc.taskID)
			}

			// 2. Fixed workspace should pass tests
			fixedFiles := make(map[string]string)
			for k, v := range task.Files {
				fixedFiles[k] = v
			}
			tc.fixFn(fixedFiles)

			fixedDir := t.TempDir()
			writeFiles(t, fixedDir, fixedFiles)
			if !runGoTest(t, fixedDir) {
				t.Fatalf("expected fixed tests for %s to pass, but failed", tc.taskID)
			}
		})
	}
}

func writeFiles(t *testing.T, base string, files map[string]string) {
	for relPath, content := range files {
		fullPath := filepath.Join(base, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func runGoTest(t *testing.T, dir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("go test in %s failed as expected: %s", dir, strings.TrimSpace(string(out)))
		return false
	}
	return true
}
