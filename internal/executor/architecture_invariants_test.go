package executor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestArchitectureInvariants enforces the Cognitive Boundary Matrix (ADR-0093)
// using static AST analysis. This test fails CI if forbidden patterns are
// introduced into the codebase.
//
// Invariants enforced:
// 1. No hardcoded bespoke task-ID comparisons in comparison/conditions.go
// 2. No probe strategy registration in wireStrategies
// 3. No legacy probe*.go source files in internal/executor
func TestArchitectureInvariants(t *testing.T) {
	t.Run("NoBespokeTaskPreCompilation", testNoBespokeTaskPreCompilation)
	t.Run("NoProbeStrategyRegistration", testNoProbeRegistration)
	t.Run("NoProbeSourceFiles", testNoProbeSourceFiles)
}

// forbiddenTaskIDs are the bespoke pre-compilation hack task IDs that were
// purged. If any of these re-appear in conditions.go as string literal
// comparisons, the architecture invariant is violated.
var forbiddenTaskIDs = []string{
	"adr_summary",
	"internal_architecture",
	"comprehensive_readme",
}

// testNoBespokeTaskPreCompilation verifies that comparison/conditions.go
// contains no hardcoded comparisons against known bespoke task IDs.
// These hacks bypass the general execution pipeline.
func testNoBespokeTaskPreCompilation(t *testing.T) {
	conditionsPath := filepath.Join("..", "comparison", "conditions.go")

	data, err := os.ReadFile(conditionsPath)
	if err != nil {
		t.Fatalf("failed to read conditions.go: %v", err)
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, conditionsPath, data, parser.AllErrors)
	if err != nil {
		t.Fatalf("failed to parse conditions.go: %v", err)
	}

	forbidden := make(map[string]bool, len(forbiddenTaskIDs))
	for _, id := range forbiddenTaskIDs {
		forbidden["\""+id+"\""] = true
	}

	var violations []string
	ast.Inspect(f, func(n ast.Node) bool {
		binExpr, ok := n.(*ast.BinaryExpr)
		if !ok {
			return true
		}

		// Check RHS for a forbidden task ID string literal
		if lit, ok := binExpr.Y.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if forbidden[lit.Value] {
				pos := fset.Position(n.Pos())
				violations = append(violations, pos.String()+": bespoke task ID pre-compilation hack: "+lit.Value)
			}
		}
		// Also check LHS (in case of reversed comparison)
		if lit, ok := binExpr.X.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if forbidden[lit.Value] {
				pos := fset.Position(n.Pos())
				violations = append(violations, pos.String()+": bespoke task ID pre-compilation hack: "+lit.Value)
			}
		}
		return true
	})

	for _, v := range violations {
		t.Errorf("FORBIDDEN: %s", v)
	}
}

// testNoProbeRegistration verifies that the wireStrategies function in
// executor.go does not register any "probe" strategy.
func testNoProbeRegistration(t *testing.T) {
	executorPath := "executor.go"

	data, err := os.ReadFile(executorPath)
	if err != nil {
		t.Fatalf("failed to read executor.go: %v", err)
	}

	content := string(data)

	// Check for probe strategy registration patterns
	forbiddenPatterns := []string{
		"NewProbeStrategy",
		`findBaseStrategy(reg, "probe")`,
	}

	for _, pattern := range forbiddenPatterns {
		if strings.Contains(content, pattern) {
			t.Errorf("FORBIDDEN: executor.go contains probe strategy registration: %q", pattern)
		}
	}
}

// testNoProbeSourceFiles verifies that no legacy probe*.go files remain in internal/executor.
func testNoProbeSourceFiles(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("failed to read internal/executor directory: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "probe") && strings.HasSuffix(name, ".go") {
			t.Errorf("FORBIDDEN: legacy probe source file still exists: %s", name)
		}
	}
}

