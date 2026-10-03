package turnreduction

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestGoFixtures_BrokenSubjectsFail validates that all three Go fixtures
// fail their visible tests when in their broken state.
func TestGoFixtures_BrokenSubjectsFail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go fixture tests require Unix")
	}

	fixtures := []string{
		"go-pricing-bugfix",
		"go-pricing-rename",
		"go-duration-diagnosis",
	}

	fixtureRoot := fixtureDir(t)

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			subjectDir := filepath.Join(fixtureRoot, name, "subject")
			if _, err := os.Stat(subjectDir); os.IsNotExist(err) {
				t.Skipf("fixture %s not found", name)
			}

			cmd := exec.Command("go", "test", "-count=1", "./...")
			cmd.Dir = subjectDir
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Errorf("fixture %s should fail but passed:\n%s", name, string(out))
			}
			t.Logf("fixture %s correctly fails:\n%s", name, truncateOutput(out, 200))
		})
	}
}

// TestGoFixtures_GradingFailsOnBroken validates that grading tests fail
// against the broken subject code.
func TestGoFixtures_GradingFailsOnBroken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go fixture tests require Unix")
	}

	fixtures := []struct {
		name string
		// Whether the grading test can even compile against broken code.
		// The rename fixture won't compile.
		compiles bool
	}{
		{"go-pricing-bugfix", true},
		{"go-pricing-rename", false},
		{"go-duration-diagnosis", true},
	}

	fixtureRoot := fixtureDir(t)

	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			subjectDir := filepath.Join(fixtureRoot, f.name, "subject")
			gradingDir := filepath.Join(fixtureRoot, f.name, "grading")

			if _, err := os.Stat(subjectDir); os.IsNotExist(err) {
				t.Skipf("fixture %s not found", f.name)
			}

			// Copy grading tests into a temp dir with subject code.
			tmpDir := t.TempDir()
			copyDir(t, subjectDir, tmpDir)
			copyDir(t, gradingDir, tmpDir)

			cmd := exec.Command("go", "test", "-count=1", "-run", "Grading", "./...")
			cmd.Dir = tmpDir
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Errorf("grading should fail on broken code but passed:\n%s", string(out))
			}
			t.Logf("grading correctly fails on broken %s:\n%s", f.name, truncateOutput(out, 200))
		})
	}
}

// fixtureDir returns the absolute path to the fixtures directory.
func fixtureDir(t *testing.T) string {
	t.Helper()
	// Relative to the test file location.
	dir, err := filepath.Abs("fixtures")
	if err != nil {
		t.Fatalf("cannot resolve fixtures dir: %v", err)
	}
	return dir
}

// copyDir copies all files from src to dst (non-recursive, files only).
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("cannot read %s: %v", src, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("cannot read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0644); err != nil {
			t.Fatalf("cannot write %s: %v", e.Name(), err)
		}
	}
}

func truncateOutput(out []byte, maxLen int) string {
	s := string(out)
	if len(s) > maxLen {
		return s[:maxLen] + "...(truncated)"
	}
	return s
}
