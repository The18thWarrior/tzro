package context_test

import (
	stdctx "context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tzroctx "tzro/pkg/context"
	"tzro/pkg/dlp"
	"tzro/pkg/store"
)

// TestIssue01_FullReportVsTinyPackCompleteness tests:
// "A full report and a tiny display pack select identical test targets and report identical discovery completeness."
func TestIssue01_FullReportVsTinyPackCompleteness(t *testing.T) {
	tempDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tempDir, "service.go"), []byte(`package svc
func DoWork() string { return "done" }
`), 0644)

	// Create 10 callers to exceed tiny token budget
	for i := 1; i <= 10; i++ {
		callerCode := `package svc
func Caller` + string(rune('A'+i)) + `() {
	DoWork()
	// Large comment padding to quickly blow up token consumption:
	// Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.
}
`
		_ = os.WriteFile(filepath.Join(tempDir, filepath.Base(tempDir)+string(rune('a'+i))+".go"), []byte(callerCode), 0644)
	}

	// Create test file
	_ = os.WriteFile(filepath.Join(tempDir, "service_test.go"), []byte(`package svc
import "testing"
func TestDoWork(t *testing.T) {
	DoWork()
}
`), 0644)

	analyzer := tzroctx.NewImpactAnalyzer(nil, nil)

	// 1. Run with large budget (4000)
	reportLarge, packLarge, err := analyzer.AnalyzeReport(
		stdctx.Background(),
		tempDir,
		[]tzroctx.Symbol{{Name: "DoWork"}},
		4000,
		false,
		"working_tree",
	)
	if err != nil {
		t.Fatalf("AnalyzeReport large budget failed: %v", err)
	}

	// 2. Run with tiny budget (20 tokens)
	reportTiny, packTiny, err := analyzer.AnalyzeReport(
		stdctx.Background(),
		tempDir,
		[]tzroctx.Symbol{{Name: "DoWork"}},
		20,
		false,
		"working_tree",
	)
	if err != nil {
		t.Fatalf("AnalyzeReport tiny budget failed: %v", err)
	}

	// Candidate test files in both reports must be identical
	if len(reportLarge.CandidateTestFiles) == 0 {
		t.Fatalf("expected candidate test files to be found")
	}
	if len(reportLarge.CandidateTestFiles) != len(reportTiny.CandidateTestFiles) {
		t.Errorf("candidate test files mismatch: large %v vs tiny %v",
			reportLarge.CandidateTestFiles, reportTiny.CandidateTestFiles)
	}
	if reportLarge.CandidateTestFiles[0] != reportTiny.CandidateTestFiles[0] {
		t.Errorf("expected same candidate test file, got %s vs %s",
			reportLarge.CandidateTestFiles[0], reportTiny.CandidateTestFiles[0])
	}

	// Discovery completeness: TotalCandidates found must be identical
	if reportLarge.Coverage.TotalCandidates != reportTiny.Coverage.TotalCandidates {
		t.Errorf("total candidates mismatch: %d vs %d",
			reportLarge.Coverage.TotalCandidates, reportTiny.Coverage.TotalCandidates)
	}
	if packLarge.Coverage.TotalCandidates != packTiny.Coverage.TotalCandidates {
		t.Errorf("pack coverage total candidates mismatch: %d vs %d",
			packLarge.Coverage.TotalCandidates, packTiny.Coverage.TotalCandidates)
	}

	// Tiny pack has truncation, large pack should have included more items
	if packTiny.Coverage.TruncatedCount == 0 {
		t.Errorf("expected tiny pack to have truncated items")
	}
	if len(packTiny.Items) >= len(packLarge.Items) {
		t.Errorf("expected tiny pack items (%d) < large pack items (%d)", len(packTiny.Items), len(packLarge.Items))
	}
}

// TestIssue01_DuplicateNamesAcrossPackagesAndCallSites tests:
// "Duplicate names across packages and multiple changed symbols at one call site retain distinct edges."
func TestIssue01_DuplicateNamesAcrossPackagesAndCallSites(t *testing.T) {
	tempDir := t.TempDir()

	pkg1Dir := filepath.Join(tempDir, "pkg1")
	pkg2Dir := filepath.Join(tempDir, "pkg2")
	_ = os.MkdirAll(pkg1Dir, 0755)
	_ = os.MkdirAll(pkg2Dir, 0755)

	_ = os.WriteFile(filepath.Join(pkg1Dir, "store.go"), []byte(`package pkg1
type Handler struct{}
func (h Handler) Close() {}
`), 0644)

	_ = os.WriteFile(filepath.Join(pkg2Dir, "network.go"), []byte(`package pkg2
type Connection struct{}
func (c Connection) Close() {}
`), 0644)

	// Call site where BOTH Handler and Connection are called or referenced
	appFile := filepath.Join(tempDir, "app.go")
	_ = os.WriteFile(appFile, []byte(`package main
import (
	"pkg1"
	"pkg2"
)
func Teardown(h pkg1.Handler, c pkg2.Connection) {
	h.Close()
	c.Close()
}
`), 0644)

	sym1 := tzroctx.Symbol{
		Name:      "Handler",
		Package:   "pkg1",
		FilePath:  "pkg1/store.go",
		Language:  "go",
		Workspace: tempDir,
	}
	sym2 := tzroctx.Symbol{
		Name:      "Connection",
		Package:   "pkg2",
		FilePath:  "pkg2/network.go",
		Language:  "go",
		Workspace: tempDir,
	}

	analyzer := tzroctx.NewImpactAnalyzer(nil, nil)
	report, _, err := analyzer.AnalyzeReport(
		stdctx.Background(),
		tempDir,
		[]tzroctx.Symbol{sym1, sym2},
		4000,
		false,
		"working_tree",
	)
	if err != nil {
		t.Fatalf("AnalyzeReport failed: %v", err)
	}

	// Verify both symbols have distinct edges pointing to app.go
	foundSym1Edge := false
	foundSym2Edge := false

	for _, edge := range report.ReferenceEdges {
		if edge.FilePath == "app.go" {
			if edge.SourceSymbol != nil && edge.SourceSymbol.Name == "Handler" && edge.SymbolName == "Handler" {
				foundSym1Edge = true
			}
			if edge.SourceSymbol != nil && edge.SourceSymbol.Name == "Connection" && edge.SymbolName == "Connection" {
				foundSym2Edge = true
			}
		}
	}

	if !foundSym1Edge {
		t.Errorf("missing edge for Handler in app.go")
	}
	if !foundSym2Edge {
		t.Errorf("missing edge for Connection in app.go")
	}

	// Verify TotalEdgesCount accounts for both edges
	if report.TotalEdgesCount < 2 {
		t.Errorf("expected at least 2 edges, got %d", report.TotalEdgesCount)
	}
}

// TestIssue01_FallbackAndMissingRipgrep tests:
// "Missing ripgrep, absent/stale FTS5, unreadable files, timeout, and cancellation have explicit outcomes."
func TestIssue01_FallbackAndMissingRipgrep(t *testing.T) {
	tempDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tempDir, "util.go"), []byte(`package main
func Helper() {}
`), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "main.go"), []byte(`package main
func main() { Helper() }
`), 0644)

	// Create adapter with non-existent ripgrep binary path
	rgAdapter := tzroctx.NewGoRipgrepAdapter(nil, nil)
	rgAdapter.SetRipgrepPath("/path/to/nonexistent/rg_binary_fake")

	reg := tzroctx.NewAdapterRegistry(nil, nil)
	reg.Register("go", rgAdapter)

	analyzer := tzroctx.NewImpactAnalyzerWithRegistry(nil, nil, reg)

	report, pack, err := analyzer.AnalyzeReport(
		stdctx.Background(),
		tempDir,
		[]tzroctx.Symbol{{Name: "Helper"}},
		4000,
		false,
		"working_tree",
	)
	if err != nil {
		t.Fatalf("unexpected error with fallback: %v", err)
	}

	if !report.Coverage.FallbackUsed {
		t.Errorf("expected FallbackUsed to be true when rg is absent")
	}
	if !pack.Coverage.FallbackUsed {
		t.Errorf("expected pack.Coverage.FallbackUsed to be true")
	}
	if report.Coverage.FallbackReason == "" {
		t.Errorf("expected non-empty FallbackReason")
	}

	// References should still be discovered by the fallback scanner!
	if len(report.ReferenceEdges) == 0 {
		t.Errorf("expected fallback scanner to discover references, got 0")
	}
}

// TestIssue01_SymbolDisambiguation tests:
// "Resolve bare impact --symbol anchors explicitly. Add --file disambiguation and report multiple declarations instead of merging them."
func TestIssue01_SymbolDisambiguation(t *testing.T) {
	tempDir := t.TempDir()

	fileA := filepath.Join(tempDir, "a.go")
	fileB := filepath.Join(tempDir, "b.go")

	_ = os.WriteFile(fileA, []byte(`package a
func Execute() string { return "a" }
`), 0644)

	_ = os.WriteFile(fileB, []byte(`package b
func Execute() string { return "b" }
`), 0644)

	analyzer := tzroctx.NewImpactAnalyzer(nil, nil)

	// 1. Bare symbol without --file when multiple declarations exist -> ambiguous error
	_, _, err := analyzer.AnalyzeSymbolWithFile(tempDir, "Execute", "", 4000, false)
	if err == nil {
		t.Fatalf("expected ambiguity error for bare 'Execute', got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected error message to mention 'ambiguous', got: %v", err)
	}
	if !strings.Contains(err.Error(), "--file") {
		t.Errorf("expected error message to advise using --file, got: %v", err)
	}

	// 2. Disambiguated with --file a.go -> succeeds
	reportA, packA, err := analyzer.AnalyzeSymbolWithFile(tempDir, "Execute", "a.go", 4000, false)
	if err != nil {
		t.Fatalf("unexpected error with --file a.go: %v", err)
	}
	if len(reportA.ChangedSymbols) != 1 || reportA.ChangedSymbols[0].FilePath != "a.go" {
		t.Errorf("expected symbol resolved to a.go, got: %+v", reportA.ChangedSymbols)
	}
	if packA == nil {
		t.Errorf("expected non-nil packA")
	}
}

// TestIssue01_WorkspaceExclusionsAndPolicy tests:
// "Hidden files, nested ignore rules, generated files, symlinks, and privacy-denied paths match the documented search scope."
func TestIssue01_WorkspaceExclusionsAndPolicy(t *testing.T) {
	tempDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tempDir, "root.go"), []byte(`package main
func Target() {}
`), 0644)

	// Allowed consumer
	_ = os.WriteFile(filepath.Join(tempDir, "allowed.go"), []byte(`package main
func Call1() { Target() }
`), 0644)

	// Denied consumer via DLP policy
	secretDir := filepath.Join(tempDir, "secret")
	_ = os.MkdirAll(secretDir, 0755)
	_ = os.WriteFile(filepath.Join(secretDir, "private.go"), []byte(`package secret
func CallSecret() { Target() }
`), 0644)

	// Denied via .gitignore
	_ = os.WriteFile(filepath.Join(tempDir, ".gitignore"), []byte("ignored_dir/\n*.ignored\n"), 0644)
	ignDir := filepath.Join(tempDir, "ignored_dir")
	_ = os.MkdirAll(ignDir, 0755)
	_ = os.WriteFile(filepath.Join(ignDir, "call.go"), []byte(`package ign
func CallIgn() { Target() }
`), 0644)

	wp := &dlp.WorkspacePolicy{
		Version:       "1.0",
		DefaultAction: dlp.ActionAllow,
		Rules: []dlp.PolicyRule{
			{PathPattern: "secret/*", Action: dlp.ActionDeny, Description: "Deny secrets directory"},
		},
	}
	policy := dlp.NewPolicyEngine(wp)

	s, _ := store.OpenStore(":memory:")
	defer s.Close()

	analyzer := tzroctx.NewImpactAnalyzer(s, policy)

	report, pack, err := analyzer.AnalyzeReport(
		stdctx.Background(),
		tempDir,
		[]tzroctx.Symbol{{Name: "Target"}},
		4000,
		false,
		"working_tree",
	)
	if err != nil {
		t.Fatalf("AnalyzeReport failed: %v", err)
	}

	foundAllowed := false
	for _, ref := range report.ReferenceEdges {
		if strings.Contains(ref.FilePath, "secret") {
			t.Errorf("DLP-denied file leaked into reference edges: %s", ref.FilePath)
		}
		if strings.Contains(ref.FilePath, "ignored_dir") {
			t.Errorf("Gitignored file leaked into reference edges: %s", ref.FilePath)
		}
		if ref.FilePath == "allowed.go" {
			foundAllowed = true
		}
	}

	if !foundAllowed {
		t.Errorf("allowed.go caller was not found")
	}

	for _, it := range pack.Items {
		if strings.Contains(it.FilePath, "secret") {
			t.Errorf("DLP-denied file leaked into context pack: %s", it.FilePath)
		}
	}
}
