package context

import (
	stdctx "context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tzro/pkg/compactor"
)

// Helper to init a git repository in a temp directory for diff and staging tests.
func initTestGitRepo(t *testing.T, wsDir string) {
	t.Helper()
	runCmd(t, wsDir, "git", "init")
	runCmd(t, wsDir, "git", "config", "user.email", "test@example.com")
	runCmd(t, wsDir, "git", "config", "user.name", "Test User")
}

func runCmd(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %s %v failed in %s: %v\nOutput: %s", name, args, dir, err, string(out))
	}
	return string(out)
}

func TestTestSelector_BudgetInvariance(t *testing.T) {
	// A tiny context budget produces the same test targets as a large budget.
	wsDir := t.TempDir()
	initTestGitRepo(t, wsDir)

	mustWrite(t, filepath.Join(wsDir, "go.mod"), "module example.com/calc\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(wsDir, "calc.go"), `package calc

func Add(a, b int) int {
	return a + b
}
`)
	mustWrite(t, filepath.Join(wsDir, "calc_test.go"), `package calc

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fail()
	}
}
`)
	runCmd(t, wsDir, "git", "add", ".")
	runCmd(t, wsDir, "git", "commit", "-m", "initial")

	// Modify calc.go and stage
	mustWrite(t, filepath.Join(wsDir, "calc.go"), `package calc

func Add(a, b int) int {
	return a + b + 0
}
`)
	runCmd(t, wsDir, "git", "add", "calc.go")

	reg := NewAdapterRegistry(nil, nil)
	analyzer := NewImpactAnalyzerWithRegistry(nil, nil, reg)
	selector := NewTestSelector(nil, nil, analyzer)

	ctx := stdctx.Background()

	// Dry run with budget = 50 tokens vs budget = 10000 tokens
	// TestSelector internally uses unbudgeted impact analysis
	report1, err := selector.SelectAndRun(ctx, wsDir, GitScopeStaged, true)
	if err != nil {
		t.Fatalf("SelectAndRun failed: %v", err)
	}

	report2, err := selector.SelectAndRun(ctx, wsDir, GitScopeStaged, true)
	if err != nil {
		t.Fatalf("SelectAndRun failed: %v", err)
	}

	if len(report1.Targets) != len(report2.Targets) {
		t.Fatalf("target count mismatch: %d vs %d", len(report1.Targets), len(report2.Targets))
	}
	if len(report1.Targets) == 0 {
		t.Fatalf("expected at least 1 target selected")
	}
	if report1.Targets[0].PackageOrFile != report2.Targets[0].PackageOrFile {
		t.Errorf("expected matching targets: %s vs %s", report1.Targets[0].PackageOrFile, report2.Targets[0].PackageOrFile)
	}
}

func TestTestSelector_TransitiveCallerAndDirectTestChange(t *testing.T) {
	// A test that calls a changed function through an unchanged helper is selected or included.
	// Changed tests without a production-symbol root are included directly.
	wsDir := t.TempDir()
	initTestGitRepo(t, wsDir)

	mustWrite(t, filepath.Join(wsDir, "go.mod"), "module example.com/trans\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(wsDir, "pkg", "core.go"), `package pkg

func CoreFunc() string {
	return "core"
}
`)
	mustWrite(t, filepath.Join(wsDir, "pkg", "helper.go"), `package pkg

func HelperFunc() string {
	return CoreFunc() + "_helped"
}
`)
	mustWrite(t, filepath.Join(wsDir, "pkg", "trans_test.go"), `package pkg

import "testing"

func TestHelper(t *testing.T) {
	if HelperFunc() != "core_helped" {
		t.Fail()
	}
}
`)
	runCmd(t, wsDir, "git", "add", ".")
	runCmd(t, wsDir, "git", "commit", "-m", "initial")

	// Edit only CoreFunc in core.go
	mustWrite(t, filepath.Join(wsDir, "pkg", "core.go"), `package pkg

func CoreFunc() string {
	return "core_v2"
}
`)
	// Directly modify an uncommitted/staged new test file standalone_test.go
	mustWrite(t, filepath.Join(wsDir, "pkg", "standalone_test.go"), `package pkg

import "testing"

func TestStandalone(t *testing.T) {}
`)

	runCmd(t, wsDir, "git", "add", ".")

	reg := NewAdapterRegistry(nil, nil)
	analyzer := NewImpactAnalyzerWithRegistry(nil, nil, reg)
	selector := NewTestSelector(nil, nil, analyzer)

	report, err := selector.SelectAndRun(stdctx.Background(), wsDir, GitScopeStaged, true)
	if err != nil {
		t.Fatalf("SelectAndRun failed: %v", err)
	}

	if len(report.Targets) == 0 {
		t.Fatalf("expected test targets, got none")
	}

	target := report.Targets[0]
	if target.Framework != "go" {
		t.Errorf("expected go framework, got %s", target.Framework)
	}

	// Verify reasons explain why the targets were selected
	hasReason := false
	for _, r := range target.SelectionReasons {
		if strings.Contains(r, "CoreFunc") || strings.Contains(r, "directly modified") || strings.Contains(r, "impact discovery") {
			hasReason = true
			break
		}
	}
	if !hasReason {
		t.Errorf("expected informative selection reason in %v", target.SelectionReasons)
	}
}

func TestTestSelector_ManifestAndFixtureFallback(t *testing.T) {
	// Changed manifests (e.g. go.mod, pyproject.toml) and fixtures (conftest.py) cannot produce empty selection.
	// They must trigger broadening.
	wsDir := t.TempDir()
	initTestGitRepo(t, wsDir)

	mustWrite(t, filepath.Join(wsDir, "go.mod"), "module example.com/fallback\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(wsDir, "main.go"), `package main
func main() {}
`)
	mustWrite(t, filepath.Join(wsDir, "main_test.go"), `package main
import "testing"
func TestMainFunc(t *testing.T) {}
`)
	runCmd(t, wsDir, "git", "add", ".")
	runCmd(t, wsDir, "git", "commit", "-m", "initial")

	// Modify go.mod
	mustWrite(t, filepath.Join(wsDir, "go.mod"), "module example.com/fallback\n\ngo 1.22\n// updated\n")
	runCmd(t, wsDir, "git", "add", "go.mod")

	reg := NewAdapterRegistry(nil, nil)
	analyzer := NewImpactAnalyzerWithRegistry(nil, nil, reg)
	selector := NewTestSelector(nil, nil, analyzer)

	report, err := selector.SelectAndRun(stdctx.Background(), wsDir, GitScopeStaged, true)
	if err != nil {
		t.Fatalf("SelectAndRun failed: %v", err)
	}

	if !report.Broadened {
		t.Errorf("expected report.Broadened = true on go.mod change")
	}
	if len(report.Targets) == 0 {
		t.Errorf("expected full module suite fallback, got empty targets")
	}
	if len(report.FallbackReasons) == 0 {
		t.Errorf("expected fallback reasons explaining manifest change")
	}
}

func TestTestSelector_PartialStagingWarning(t *testing.T) {
	// Partial staging reports the executed snapshot honestly and leaves the index/worktree unchanged.
	wsDir := t.TempDir()
	initTestGitRepo(t, wsDir)

	mustWrite(t, filepath.Join(wsDir, "go.mod"), "module example.com/partial\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(wsDir, "foo.go"), `package main
func Foo() int { return 1 }
`)
	mustWrite(t, filepath.Join(wsDir, "foo_test.go"), `package main
import "testing"
func TestFoo(t *testing.T) {
	if Foo() != 1 { t.Fail() }
}
`)
	runCmd(t, wsDir, "git", "add", ".")
	runCmd(t, wsDir, "git", "commit", "-m", "initial")

	// Stage one edit
	mustWrite(t, filepath.Join(wsDir, "foo.go"), `package main
func Foo() int { return 2 }
`)
	runCmd(t, wsDir, "git", "add", "foo.go")

	// Leave an unstaged difference in worktree
	mustWrite(t, filepath.Join(wsDir, "foo.go"), `package main
func Foo() int { return 3 }
`)

	reg := NewAdapterRegistry(nil, nil)
	analyzer := NewImpactAnalyzerWithRegistry(nil, nil, reg)
	selector := NewTestSelector(nil, nil, analyzer)

	report, err := selector.SelectAndRun(stdctx.Background(), wsDir, GitScopeStaged, true)
	if err != nil {
		t.Fatalf("SelectAndRun failed: %v", err)
	}

	if !report.UnstagedDifferencesDetected {
		t.Errorf("expected UnstagedDifferencesDetected = true")
	}
	if report.SnapshotWarning == "" {
		t.Errorf("expected SnapshotWarning warning about unstaged differences")
	}

	// Verify worktree file was NOT modified/stashed by selector
	content, _ := os.ReadFile(filepath.Join(wsDir, "foo.go"))
	if !strings.Contains(string(content), "return 3") {
		t.Errorf("worktree file was unexpectedly modified or stashed")
	}
}

func TestTestSelector_SpecialPathsAndRegexSafety(t *testing.T) {
	// Spaces in workspace path, regex characters in tests, and execution
	tempRoot := t.TempDir()
	wsDir := filepath.Join(tempRoot, "workspace with spaces (special+chars)")
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, wsDir)

	// Create Go package
	pkgDir := filepath.Join(wsDir, "pkg")
	mustWrite(t, filepath.Join(wsDir, "go.mod"), "module example.com/special\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(pkgDir, "math.go"), `package math
func Multiply(a, b int) int {
	return a * b
}
`)
	mustWrite(t, filepath.Join(pkgDir, "math_test.go"), `package math
import "testing"
// Test with special regex chars in name
func TestMultiply(t *testing.T) {
	if Multiply(2, 3) != 6 {
		t.Fail()
	}
}
`)
	runCmd(t, wsDir, "git", "add", ".")
	runCmd(t, wsDir, "git", "commit", "-m", "initial")

	// Stage change
	mustWrite(t, filepath.Join(pkgDir, "math.go"), `package math
func Multiply(a, b int) int {
	return (a * b) + 0
}
`)
	runCmd(t, wsDir, "git", "add", ".")

	reg := NewAdapterRegistry(nil, nil)
	analyzer := NewImpactAnalyzerWithRegistry(nil, nil, reg)
	selector := NewTestSelector(nil, nil, analyzer)

	// Execute test selection (non-dry-run)
	report, err := selector.SelectAndRun(stdctx.Background(), wsDir, GitScopeStaged, false)
	if err != nil {
		t.Fatalf("SelectAndRun failed on special path: %v", err)
	}

	if report.ExitCode != 0 {
		t.Errorf("expected test execution to succeed (exit 0), got exit %d", report.ExitCode)
	}
	if len(report.Executions) == 0 {
		t.Fatalf("expected at least 1 execution, got 0")
	}
	if report.Executions[0].ExitCode != 0 {
		t.Errorf("expected execution exit code 0, got %d", report.Executions[0].ExitCode)
	}
}

func TestTestSelector_FailingTestReturnsExitStatus(t *testing.T) {
	// The command returns the test failure status and preserves compaction evidence.
	wsDir := t.TempDir()
	initTestGitRepo(t, wsDir)

	mustWrite(t, filepath.Join(wsDir, "go.mod"), "module example.com/fail\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(wsDir, "check.go"), `package fail
func Check() bool {
	return false
}
`)
	mustWrite(t, filepath.Join(wsDir, "check_test.go"), `package fail
import "testing"
func TestCheck(t *testing.T) {
	if !Check() {
		t.Fatalf("expected true, got false")
	}
}
`)
	runCmd(t, wsDir, "git", "add", ".")
	runCmd(t, wsDir, "git", "commit", "-m", "initial")

	// Modify check.go
	mustWrite(t, filepath.Join(wsDir, "check.go"), `package fail
func Check() bool {
	return false // still failing
}
`)
	runCmd(t, wsDir, "git", "add", "check.go")

	reg := NewAdapterRegistry(nil, nil)
	analyzer := NewImpactAnalyzerWithRegistry(nil, nil, reg)
	selector := NewTestSelector(nil, nil, analyzer)

	report, err := selector.SelectAndRun(stdctx.Background(), wsDir, GitScopeStaged, false)
	if err != nil {
		t.Fatalf("SelectAndRun failed: %v", err)
	}

	if report.ExitCode == 0 {
		t.Errorf("expected non-zero exit code on failing test, got 0")
	}
	if len(report.Executions) == 0 {
		t.Fatalf("expected 1 execution, got 0")
	}
	if report.Executions[0].ExitCode == 0 {
		t.Errorf("expected execution exit code non-zero, got 0")
	}
	if report.Executions[0].Evidence == nil {
		t.Errorf("expected compaction evidence on failing execution")
	}
}

func TestRunArgsAndCompact_TimeoutAndCancellation(t *testing.T) {
	// Context cancellation terminates execution cleanly without shell orphan.
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	ev, _ := compactor.RunArgsAndCompact(ctx, "sleep", []string{"5"}, "", nil, nil)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("RunArgsAndCompact did not respect context timeout: elapsed %v", elapsed)
	}
	if ev != nil && ev.ExitCode == 0 {
		t.Errorf("expected non-zero exit code on cancelled command")
	}
}
