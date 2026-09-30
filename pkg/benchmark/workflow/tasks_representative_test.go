package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRepresentativeTasksRejectIncompleteWork(t *testing.T) {
	for _, c := range representativeCases() {
		t.Run(c.task.ID, func(t *testing.T) {
			root := t.TempDir()
			workspace := filepath.Join(root, "workspace")
			writeFiles(t, workspace, c.task.Files)
			if err := os.MkdirAll(filepath.Join(root, "tools"), 0700); err != nil {
				t.Fatal(err)
			}
			goBin, err := exec.LookPath("go")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(goBin, filepath.Join(root, "tools/go")); err != nil {
				t.Fatal(err)
			}
			p := &prepared{result: Result{Home: filepath.Join(root, "home"), Workspace: workspace}, env: os.Environ()}
			cfg := Config{Timeout: 30 * time.Second}
			if passed, _ := grade(context.Background(), cfg, c.task, p); passed {
				t.Fatal("unfinished task passed grading")
			}
			writeFiles(t, workspace, c.solution)
			if passed, err := grade(context.Background(), cfg, c.task, p); !passed {
				t.Fatalf("intended solution failed: %v", err)
			}
			for name := range c.task.GradeFiles {
				if _, err := os.Stat(filepath.Join(workspace, name)); !os.IsNotExist(err) {
					t.Fatal("private check exposed in workspace")
				}
			}
		})
	}
}
