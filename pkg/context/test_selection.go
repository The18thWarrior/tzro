package context

import (
	stdctx "context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tzro/pkg/compactor"
	"tzro/pkg/dlp"
	"tzro/pkg/store"
)

// TestTarget represents an executable test target.
type TestTarget struct {
	ModuleRoot       string   `json:"module_root"`
	PackageOrFile    string   `json:"package_or_file"`
	Framework        string   `json:"framework"` // go | jest | vitest | pytest
	Executable       string   `json:"executable"`
	Args             []string `json:"args"`
	Cwd              string   `json:"cwd"`
	SelectionReasons []string `json:"selection_reasons"`
	TestNames        []string `json:"test_names,omitempty"`
}

// TestExecution records the outcome of executing a TestTarget.
type TestExecution struct {
	Target          TestTarget                   `json:"target"`
	ExitCode        int                          `json:"exit_code"`
	ExitSource      string                       `json:"exit_source"`
	Evidence        *compactor.CompactedEvidence `json:"evidence,omitempty"`
	ArtifactRef     string                       `json:"artifact_ref,omitempty"`
	DurationSeconds float64                      `json:"duration_seconds"`
}

// TestSelectionReport is the structured report returned by test selection.
type TestSelectionReport struct {
	Scope                       GitScope        `json:"scope"`
	SnapshotProvenance          string          `json:"snapshot_provenance"`
	UnstagedDifferencesDetected bool            `json:"unstaged_differences_detected"`
	SnapshotWarning             string          `json:"snapshot_warning,omitempty"`
	Targets                     []TestTarget    `json:"targets"`
	Broadened                   bool            `json:"broadened"`
	FallbackReasons             []string        `json:"fallback_reasons,omitempty"`
	OmittedTargets              []string        `json:"omitted_targets,omitempty"`
	Coverage                    *CoverageReport `json:"coverage,omitempty"`
	Executions                  []TestExecution `json:"executions,omitempty"`
	ExitCode                    int             `json:"exit_code"`
	GeneratedAt                 time.Time       `json:"generated_at"`
}

// FormatSummary formats the selection report as human-readable text.
func (r *TestSelectionReport) FormatSummary() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("tzro test selection [scope: %s | exit: %d]\n", r.Scope, r.ExitCode))
	if r.SnapshotWarning != "" {
		sb.WriteString(fmt.Sprintf("Warning: %s\n", r.SnapshotWarning))
	}
	if len(r.FallbackReasons) > 0 {
		sb.WriteString(fmt.Sprintf("Broadened suite fallback: %s\n", strings.Join(r.FallbackReasons, "; ")))
	}
	if len(r.Targets) == 0 {
		sb.WriteString("No test targets selected (no changes or no applicable tests in scope).\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Selected %d test target(s):\n", len(r.Targets)))
	for i, t := range r.Targets {
		sb.WriteString(fmt.Sprintf("  %d. [%s] %s (in %s)\n", i+1, t.Framework, t.PackageOrFile, t.Cwd))
		sb.WriteString(fmt.Sprintf("     Command: %s %s\n", t.Executable, strings.Join(t.Args, " ")))
		if len(t.SelectionReasons) > 0 {
			sb.WriteString(fmt.Sprintf("     Reasons: %s\n", strings.Join(t.SelectionReasons, ", ")))
		}
	}

	if len(r.Executions) > 0 {
		sb.WriteString("\nExecution Results:\n")
		for i, ex := range r.Executions {
			status := "PASS"
			if ex.ExitCode != 0 {
				status = fmt.Sprintf("FAIL (exit %d)", ex.ExitCode)
			}
			sb.WriteString(fmt.Sprintf("  %d. %s: %s (%.2fs)\n", i+1, ex.Target.PackageOrFile, status, ex.DurationSeconds))
		}
	}

	return sb.String()
}

// TestSelector orchestrates predictive test selection and execution.
type TestSelector struct {
	store    *store.Store
	policy   *dlp.PolicyEngine
	analyzer *ImpactAnalyzer
}

// NewTestSelector creates a new TestSelector.
func NewTestSelector(s *store.Store, policy *dlp.PolicyEngine, analyzer *ImpactAnalyzer) *TestSelector {
	return &TestSelector{
		store:    s,
		policy:   policy,
		analyzer: analyzer,
	}
}

// SelectAndRun evaluates changes in scope and either returns the selection (dry run) or executes targets.
func (ts *TestSelector) SelectAndRun(
	ctx stdctx.Context,
	workspaceRoot string,
	scope GitScope,
	dryRun bool,
) (*TestSelectionReport, error) {
	report := &TestSelectionReport{
		Scope:              scope,
		SnapshotProvenance: string(scope),
		GeneratedAt:        time.Now().UTC(),
	}

	// 1. Acquire diff for requested scope
	diffResult, err := AcquireGitDiff(ctx, workspaceRoot, scope)
	if err != nil {
		return nil, fmt.Errorf("git diff acquisition failed: %w", err)
	}

	if len(diffResult.Files) == 0 {
		// Explicit no-op. Never substitute another scope.
		report.Coverage = &CoverageReport{NoChanges: true}
		return report, nil
	}

	// 2. When scope is staged, detect unstaged differences
	if scope == GitScopeStaged {
		unstagedDiff, uErr := AcquireGitDiff(ctx, workspaceRoot, GitScopeUnstaged)
		if uErr == nil && len(unstagedDiff.Files) > 0 {
			report.UnstagedDifferencesDetected = true
			report.SnapshotWarning = "Staged selection executes current worktree files; detected unstaged differences. Does not verify exact staged snapshot without worktree."
		}
	}

	// 3. Extract AST-validated symbols and diff scope
	symbols, symCov, err := ExtractSymbolsFromSnapshotDiff(ctx, workspaceRoot, diffResult)
	if err != nil {
		return nil, fmt.Errorf("diff symbol extraction failed: %w", err)
	}

	// Also discover directly changed test files (which may have no production-symbol root)
	directlyChangedTests := make(map[string]bool)
	directlyChangedConfigs := make(map[string]bool)

	for _, f := range diffResult.Files {
		p := f.NewPath
		if p == "" {
			p = f.OldPath
		}
		if isAnyTestFile(p) {
			directlyChangedTests[p] = true
		}
		if isNonSymbolConfigFile(p) {
			directlyChangedConfigs[p] = true
		}
	}

	// 4. Run impact discovery across adapters
	// Pass budget 1000, but we use the unbudgeted ImpactReport, never ContextPack.Items!
	impactReport, _, err := ts.analyzer.AnalyzeReport(ctx, workspaceRoot, symbols, 1000, false, string(scope))
	if err != nil {
		return nil, fmt.Errorf("impact analysis failed: %w", err)
	}

	report.Coverage = impactReport.Coverage
	if symCov != nil && symCov.IncompleteDiscovery {
		report.Coverage.IncompleteDiscovery = true
		report.Coverage.UnsupportedSyntax = append(report.Coverage.UnsupportedSyntax, symCov.UnsupportedSyntax...)
	}

	// 5. Check broadening triggers
	broadenToSuite := false

	if len(directlyChangedConfigs) > 0 {
		broadenToSuite = true
		for cfg := range directlyChangedConfigs {
			report.FallbackReasons = append(report.FallbackReasons, fmt.Sprintf("configuration changed: %s", cfg))
		}
	}

	if report.Coverage != nil && report.Coverage.IncompleteDiscovery {
		broadenToSuite = true
		report.FallbackReasons = append(report.FallbackReasons, "incomplete discovery in coverage report")
	}

	// Follow reverse dependencies transitively with visited set
	candidateTests := make(map[string][]string) // testFile -> []reasons

	// Add candidates from impactReport
	for _, tf := range impactReport.CandidateTestFiles {
		candidateTests[tf] = append(candidateTests[tf], "impact discovery reference")
	}

	// Add directly changed test files
	for tf := range directlyChangedTests {
		candidateTests[tf] = append(candidateTests[tf], "directly modified test file")
	}

	// Check if conftest.py or test fixtures changed
	for tf := range candidateTests {
		if filepath.Base(tf) == "conftest.py" {
			broadenToSuite = true
			report.FallbackReasons = append(report.FallbackReasons, fmt.Sprintf("shared fixture file changed: %s", tf))
		}
	}

	// Transitive caller search: follow references across edges
	transitiveCallers := make(map[string]bool)
	visitedEdges := make(map[string]bool)
	var edgeQueue []string

	for _, edge := range impactReport.ReferenceEdges {
		edgeKey := fmt.Sprintf("%s:%s", edge.FilePath, edge.SymbolName)
		if !visitedEdges[edgeKey] {
			visitedEdges[edgeKey] = true
			edgeQueue = append(edgeQueue, edge.SymbolName)
			if edge.Relationship == RelTest || isAnyTestFile(edge.FilePath) {
				candidateTests[edge.FilePath] = append(candidateTests[edge.FilePath], fmt.Sprintf("references %s", edge.SymbolName))
			}
		}
	}

	for len(edgeQueue) > 0 {
		currSym := edgeQueue[0]
		edgeQueue = edgeQueue[1:]

		for _, edge := range impactReport.ReferenceEdges {
			if edge.SourceSymbol != nil && edge.SourceSymbol.Name == currSym {
				edgeKey := fmt.Sprintf("%s:%s", edge.FilePath, edge.SymbolName)
				if !visitedEdges[edgeKey] {
					visitedEdges[edgeKey] = true
					edgeQueue = append(edgeQueue, edge.SymbolName)
					transitiveCallers[edge.FilePath] = true
					if edge.Relationship == RelTest || isAnyTestFile(edge.FilePath) {
						candidateTests[edge.FilePath] = append(candidateTests[edge.FilePath], fmt.Sprintf("transitive caller via %s", currSym))
					}
				}
			}
		}
	}

	// If broadened or if changes exist but candidateTests is empty, fallback to detected full suite
	if broadenToSuite || len(candidateTests) == 0 {
		report.Broadened = true
		if len(candidateTests) == 0 {
			report.FallbackReasons = append(report.FallbackReasons, "changes detected with no reliable targeted test references")
		}

		fullTargets, fErr := detectFullModuleSuites(workspaceRoot, diffResult.Files)
		if fErr != nil {
			return nil, fErr
		}
		if len(fullTargets) == 0 {
			return nil, fmt.Errorf("changes detected, but no supported test runner found in workspace (checked: go test, jest, vitest, pytest)")
		}
		for i := range fullTargets {
			fullTargets[i].SelectionReasons = append(fullTargets[i].SelectionReasons, report.FallbackReasons...)
		}
		report.Targets = fullTargets
	} else {
		// Group candidate tests into TestTargets
		targets, tErr := buildTestTargets(workspaceRoot, candidateTests)
		if tErr != nil {
			return nil, tErr
		}
		if len(targets) == 0 {
			// Fallback to full module suites
			report.Broadened = true
			report.FallbackReasons = append(report.FallbackReasons, "unable to target individual files with installed runners; falling back to suite")
			fullTargets, fErr := detectFullModuleSuites(workspaceRoot, diffResult.Files)
			if fErr != nil {
				return nil, fErr
			}
			report.Targets = fullTargets
		} else {
			report.Targets = targets
		}
	}

	// 6. If dry run, return report immediately
	if dryRun {
		return report, nil
	}

	// 7. Execute targets using RunArgsAndCompact
	overallExit := 0
	for _, target := range report.Targets {
		start := time.Now()
		evidence, runErr := compactor.RunArgsAndCompact(ctx, target.Executable, target.Args, target.Cwd, ts.store, ts.policy)
		duration := time.Since(start).Seconds()

		exitCode := 0
		exitSource := compactor.ExitSourceObserved
		var artRef string

		if evidence != nil {
			exitCode = evidence.ExitCode
			exitSource = evidence.ExitSource
			artRef = evidence.ArtifactRef
		} else if runErr != nil {
			exitCode = 1
			exitSource = compactor.ExitSourceUnknown
		}

		if exitCode != 0 && overallExit == 0 {
			overallExit = exitCode
		}

		report.Executions = append(report.Executions, TestExecution{
			Target:          target,
			ExitCode:        exitCode,
			ExitSource:      exitSource,
			Evidence:        evidence,
			ArtifactRef:     artRef,
			DurationSeconds: duration,
		})
	}

	report.ExitCode = overallExit
	return report, nil
}

func isAnyTestFile(p string) bool {
	lower := strings.ToLower(p)
	base := strings.ToLower(filepath.Base(p))
	if strings.HasSuffix(lower, "_test.go") {
		return true
	}
	if strings.Contains(lower, "__tests__/") || strings.Contains(lower, ".test.") || strings.Contains(lower, ".spec.") {
		return true
	}
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		return true
	}
	if strings.HasSuffix(base, "_test.py") || base == "conftest.py" {
		return true
	}
	if strings.Contains(lower, "tests/") && (strings.HasSuffix(lower, ".py") || strings.HasSuffix(lower, ".rs") || strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".js")) {
		return true
	}
	return false
}

// buildTestTargets groups test files by framework and module into discrete TestTargets.
func buildTestTargets(workspaceRoot string, candidateTests map[string][]string) ([]TestTarget, error) {
	var targets []TestTarget

	// Group by language/framework
	goFiles := make(map[string][]string)   // pkgDir -> []testFiles
	nodeFiles := make(map[string][]string) // dir -> []testFiles
	pyFiles := make(map[string][]string)   // dir -> []testFiles

	for tf, reasons := range candidateTests {
		ext := strings.ToLower(filepath.Ext(tf))
		switch ext {
		case ".go":
			pkgDir := filepath.Dir(tf)
			goFiles[pkgDir] = append(goFiles[pkgDir], tf)
		case ".ts", ".tsx", ".js", ".jsx", ".mts", ".mjs":
			pkgDir := filepath.Dir(tf)
			nodeFiles[pkgDir] = append(nodeFiles[pkgDir], tf)
		case ".py":
			pkgDir := filepath.Dir(tf)
			pyFiles[pkgDir] = append(pyFiles[pkgDir], tf)
		}
		_ = reasons
	}

	// 1. Go targets
	if len(goFiles) > 0 {
		goBin, err := exec.LookPath("go")
		if err != nil {
			return nil, fmt.Errorf("go test runner not found on PATH: %w", err)
		}

		var pkgDirs []string
		for p := range goFiles {
			pkgDirs = append(pkgDirs, p)
		}
		sort.Strings(pkgDirs)

		for _, pDir := range pkgDirs {
			relPkg := "./" + filepath.ToSlash(pDir)
			if pDir == "." {
				relPkg = "."
			}
			reasons := make(map[string]bool)
			for _, tf := range goFiles[pDir] {
				for _, r := range candidateTests[tf] {
					reasons[r] = true
				}
			}
			var reasonList []string
			for r := range reasons {
				reasonList = append(reasonList, r)
			}
			sort.Strings(reasonList)

			targets = append(targets, TestTarget{
				ModuleRoot:       workspaceRoot,
				PackageOrFile:    relPkg,
				Framework:        "go",
				Executable:       goBin,
				Args:             []string{"test", "-v", relPkg},
				Cwd:              workspaceRoot,
				SelectionReasons: reasonList,
			})
		}
	}

	// 2. Node (Vitest / Jest) targets
	if len(nodeFiles) > 0 {
		runnerPath, framework := findNodeTestRunner(workspaceRoot)
		if runnerPath != "" {
			var files []string
			reasons := make(map[string]bool)
			for _, fs := range nodeFiles {
				for _, f := range fs {
					files = append(files, f)
					for _, r := range candidateTests[f] {
						reasons[r] = true
					}
				}
			}
			sort.Strings(files)
			var reasonList []string
			for r := range reasons {
				reasonList = append(reasonList, r)
			}
			sort.Strings(reasonList)

			args := []string{"run"}
			if framework == "jest" {
				args = append([]string{"--bail"}, files...)
			} else {
				args = append(args, files...)
			}

			targets = append(targets, TestTarget{
				ModuleRoot:       workspaceRoot,
				PackageOrFile:    strings.Join(files, ", "),
				Framework:        framework,
				Executable:       runnerPath,
				Args:             args,
				Cwd:              workspaceRoot,
				SelectionReasons: reasonList,
			})
		}
	}

	// 3. Python (pytest) targets
	if len(pyFiles) > 0 {
		pytestBin := findPytestRunner(workspaceRoot)
		if pytestBin != "" {
			var files []string
			reasons := make(map[string]bool)
			for _, fs := range pyFiles {
				for _, f := range fs {
					files = append(files, f)
					for _, r := range candidateTests[f] {
						reasons[r] = true
					}
				}
			}
			sort.Strings(files)
			var reasonList []string
			for r := range reasons {
				reasonList = append(reasonList, r)
			}
			sort.Strings(reasonList)

			args := append([]string{"-v"}, files...)
			targets = append(targets, TestTarget{
				ModuleRoot:       workspaceRoot,
				PackageOrFile:    strings.Join(files, ", "),
				Framework:        "pytest",
				Executable:       pytestBin,
				Args:             args,
				Cwd:              workspaceRoot,
				SelectionReasons: reasonList,
			})
		}
	}

	return targets, nil
}

// detectFullModuleSuites discovers the full test suite targets when broadening is triggered.
func detectFullModuleSuites(workspaceRoot string, diffFiles []GitDiffFile) ([]TestTarget, error) {
	var targets []TestTarget

	// Check if Go workspace
	if _, err := os.Stat(filepath.Join(workspaceRoot, "go.mod")); err == nil {
		goBin, gErr := exec.LookPath("go")
		if gErr == nil {
			targets = append(targets, TestTarget{
				ModuleRoot:       workspaceRoot,
				PackageOrFile:    "./...",
				Framework:        "go",
				Executable:       goBin,
				Args:             []string{"test", "./..."},
				Cwd:              workspaceRoot,
				SelectionReasons: []string{"full module test suite (broadened)"},
			})
		}
	}

	// Check if Node workspace
	if _, err := os.Stat(filepath.Join(workspaceRoot, "package.json")); err == nil {
		runnerPath, framework := findNodeTestRunner(workspaceRoot)
		if runnerPath != "" {
			targets = append(targets, TestTarget{
				ModuleRoot:       workspaceRoot,
				PackageOrFile:    "all",
				Framework:        framework,
				Executable:       runnerPath,
				Args:             []string{"run"},
				Cwd:              workspaceRoot,
				SelectionReasons: []string{"full module test suite (broadened)"},
			})
		}
	}

	// Check if Python workspace
	if _, err := os.Stat(filepath.Join(workspaceRoot, "pyproject.toml")); err == nil {
		pytestBin := findPytestRunner(workspaceRoot)
		if pytestBin != "" {
			targets = append(targets, TestTarget{
				ModuleRoot:       workspaceRoot,
				PackageOrFile:    "tests",
				Framework:        "pytest",
				Executable:       pytestBin,
				Args:             []string{"-v"},
				Cwd:              workspaceRoot,
				SelectionReasons: []string{"full module test suite (broadened)"},
			})
		}
	}

	return targets, nil
}

// findNodeTestRunner detects vitest or jest locally without using npx.
func findNodeTestRunner(workspaceRoot string) (string, string) {
	// 1. Check local node_modules/.bin
	vitestLocal := filepath.Join(workspaceRoot, "node_modules", ".bin", "vitest")
	if _, err := os.Stat(vitestLocal); err == nil {
		return vitestLocal, "vitest"
	}
	jestLocal := filepath.Join(workspaceRoot, "node_modules", ".bin", "jest")
	if _, err := os.Stat(jestLocal); err == nil {
		return jestLocal, "jest"
	}

	// 2. Check PATH
	if p, err := exec.LookPath("vitest"); err == nil {
		return p, "vitest"
	}
	if p, err := exec.LookPath("jest"); err == nil {
		return p, "jest"
	}

	return "", ""
}

// findPytestRunner detects pytest in virtual environments or PATH.
func findPytestRunner(workspaceRoot string) string {
	venvPytest := filepath.Join(workspaceRoot, ".venv", "bin", "pytest")
	if _, err := os.Stat(venvPytest); err == nil {
		return venvPytest
	}
	envPytest := filepath.Join(workspaceRoot, "venv", "bin", "pytest")
	if _, err := os.Stat(envPytest); err == nil {
		return envPytest
	}
	if p, err := exec.LookPath("pytest"); err == nil {
		return p
	}
	return ""
}
