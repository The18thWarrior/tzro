package turnreduction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// GradeFixture runs the grading tests for a fixture in the given workspace.
// It copies the grading tests into a temp directory with the workspace code,
// runs the grade command, and returns whether it passed.
func GradeFixture(ctx context.Context, fixture Fixture, workspaceDir string) (bool, string, error) {
	if err := ctx.Err(); err != nil {
		return false, "", err
	}
	gradingCopy, err := os.MkdirTemp("", "tzro-private-grade-*")
	if err != nil {
		return false, "", err
	}
	defer os.RemoveAll(gradingCopy)
	if err := copyDirContext(ctx, workspaceDir, gradingCopy); err != nil {
		return false, "", fmt.Errorf("prepare private grading copy: %w", err)
	}
	workspaceDir = gradingCopy
	var passed bool
	var log string
	switch fixture.Language {
	case "go":
		passed, log, err = gradeGo(ctx, fixture, workspaceDir)
	case "python":
		passed, log, err = gradePython(ctx, fixture, workspaceDir)
	case "typescript":
		passed, log, err = gradeTypeScript(ctx, fixture, workspaceDir)
	default:
		return false, "", fmt.Errorf("unsupported language: %s", fixture.Language)
	}
	if passed {
		tests := passingPrivateTests(fixture, log)
		if len(tests) == 0 {
			return false, log, fmt.Errorf("no passing private tests: %s", fixture.Language)
		}
		for _, name := range fixture.RequiredGradeTests {
			if !tests[name] {
				return false, log, fmt.Errorf("required private test did not pass: %s", name)
			}
		}
	} else if err == nil && zeroTestsRan(fixture, log) {
		// Command exited non-zero with no tests discovered (e.g. Python 3.12+ exits 5
		// when zero tests are found). Surface the same explicit guard so callers always
		// see an error explaining *why* the grade failed, rather than a bare passed=false.
		return false, log, fmt.Errorf("no passing private tests: %s", fixture.Language)
	}
	return passed, log, err
}

func passingPrivateTests(fixture Fixture, log string) map[string]bool {
	tests := map[string]bool{}
	switch fixture.Language {
	case "go":
		for _, line := range strings.Split(log, "\n") {
			var event struct{ Action, Test string }
			if json.Unmarshal([]byte(line), &event) == nil && event.Action == "pass" && event.Test != "" {
				tests[event.Test] = true
			}
		}
	case "python":
		for _, match := range regexp.MustCompile(`(?m)^(test_\S+) .+ \.\.\. ok$`).FindAllStringSubmatch(log, -1) {
			tests[match[1]] = true
		}
	case "typescript":
		for _, match := range regexp.MustCompile(`(?m)^ok [0-9]+ - (.+)$`).FindAllStringSubmatch(log, -1) {
			name := match[1]
			// Node reports an empty test file itself as a passing test.
			if !strings.HasSuffix(name, ".test.js") && !strings.Contains(name, "# SKIP") && !strings.Contains(name, "# TODO") {
				tests[name] = true
			}
		}
	}
	return tests
}

// zeroTestsRan returns true when the test runner log indicates that no tests
// were discovered or executed. This covers Python 3.12+ which exits non-zero
// with "Ran 0 tests" when zero tests match the discovery pattern, and Go's
// "no test files" or "no tests to run" messages.
func zeroTestsRan(fixture Fixture, log string) bool {
	switch fixture.Language {
	case "python":
		return strings.Contains(log, "Ran 0 tests") || strings.Contains(log, "NO TESTS RAN")
	case "go":
		return strings.Contains(log, "no test files") || strings.Contains(log, "no tests to run")
	case "typescript":
		// Node test runner with zero tests reports "# tests 0".
		return strings.Contains(log, "# tests 0")
	}
	return false
}

func gradeGo(ctx context.Context, fixture Fixture, workspaceDir string) (bool, string, error) {
	// Copy grading tests into workspace.
	if err := copyGradingFiles(fixture.GradingDir, workspaceDir); err != nil {
		return false, "", fmt.Errorf("cannot copy grading files: %w", err)
	}
	defer cleanGradingFiles(fixture.GradingDir, workspaceDir)

	cmd := exec.CommandContext(ctx, "go", "test", "-json", "-count=1", "-run", "Grading", "./...")
	cmd.Dir = workspaceDir
	return runGradeCommand(ctx, cmd)
}

func gradePython(ctx context.Context, fixture Fixture, workspaceDir string) (bool, string, error) {
	if err := copyGradingFiles(fixture.GradingDir, workspaceDir); err != nil {
		return false, "", fmt.Errorf("cannot copy grading files: %w", err)
	}
	defer cleanGradingFiles(fixture.GradingDir, workspaceDir)

	cmd := exec.CommandContext(ctx, "python3", "-m", "unittest", "discover", "-v", "-s", ".", "-p", "test_grading*.py")
	cmd.Dir = workspaceDir
	return runGradeCommand(ctx, cmd)
}

func gradeTypeScript(ctx context.Context, fixture Fixture, workspaceDir string) (bool, string, error) {
	if err := copyGradingFiles(fixture.GradingDir, workspaceDir); err != nil {
		return false, "", fmt.Errorf("cannot copy grading files: %w", err)
	}
	defer cleanGradingFiles(fixture.GradingDir, workspaceDir)

	// Compile first.
	tsc := exec.CommandContext(ctx, "tsc")
	tsc.Dir = workspaceDir
	if passed, log, err := runGradeCommand(ctx, tsc); !passed {
		return false, log, err
	}

	// Run grading tests.
	entries, err := os.ReadDir(fixture.GradingDir)
	if err != nil {
		return false, "", err
	}
	args := []string{"--test"}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".test.ts") {
			args = append(args, filepath.Join("dist", strings.TrimSuffix(entry.Name(), ".ts")+".js"))
		}
	}
	if len(args) == 1 {
		return false, "", fmt.Errorf("no private TypeScript test files")
	}
	cmd := exec.CommandContext(ctx, "node", args...)
	cmd.Dir = workspaceDir
	return runGradeCommand(ctx, cmd)
}

func copyGradingFiles(gradingDir, workspaceDir string) error {
	entries, err := os.ReadDir(gradingDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(gradingDir, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(workspaceDir, e.Name()), data, 0644); err != nil {
			return err
		}
	}
	return nil
}

func cleanGradingFiles(gradingDir, workspaceDir string) {
	entries, _ := os.ReadDir(gradingDir)
	for _, e := range entries {
		os.Remove(filepath.Join(workspaceDir, e.Name()))
	}
}

// A failing assertion is a grade; a missing executable or expired deadline is infrastructure failure.
func runGradeCommand(ctx context.Context, cmd *exec.Cmd) (bool, string, error) {
	cleanup := ownProcessGroup(cmd)
	defer cleanup()
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return false, string(out), ctx.Err()
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return false, string(out), err
	}
	return err == nil, string(out), nil
}
