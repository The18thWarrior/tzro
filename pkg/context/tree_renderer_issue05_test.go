package context

import (
	"strings"
	"testing"
	"time"
)

func TestTreeRenderer_SyntheticReport(t *testing.T) {
	symA := Symbol{Name: "FunctionA", Kind: "function", FilePath: "pkg/core.go", StartLine: 10}
	symB := Symbol{Name: "TypeB", Kind: "type", FilePath: "pkg/types.go", StartLine: 25}

	refA1 := RawReference{
		FilePath:     "pkg/consumer.go",
		StartLine:    42,
		EndLine:      45,
		Precision:    PrecisionSyntactic,
		Relationship: RelCaller,
		SymbolName:   "FunctionA",
		SourceSymbol: &symA,
	}
	refA2 := RawReference{
		FilePath:     "pkg/core_test.go",
		StartLine:    15,
		EndLine:      20,
		Precision:    PrecisionSyntactic,
		Relationship: RelTest,
		SymbolName:   "FunctionA",
		SourceSymbol: &symA,
	}
	// Shared call site: refB1 is at the exact same location as refA1!
	refB1 := RawReference{
		FilePath:     "pkg/consumer.go",
		StartLine:    42,
		EndLine:      45,
		Precision:    PrecisionSyntactic,
		Relationship: RelCaller,
		SymbolName:   "TypeB",
		SourceSymbol: &symB,
	}

	report := &ImpactReport{
		WorkspaceRoot:         "/test/workspace",
		ChangedSymbols:        []Symbol{symA, symB},
		ReferenceEdges:        []RawReference{refA1, refA2, refB1},
		UniqueReferencesCount: 2, // 2 unique sites: consumer.go:42 and core_test.go:15
		TotalEdgesCount:       3, // 3 edges total
		CandidateTestFiles:    []string{"pkg/core_test.go"},
		AffectedModules:       []string{"pkg"},
		Coverage: &CoverageReport{
			IncompleteDiscovery: false,
		},
		GeneratedAt: time.Now().UTC(),
	}

	renderer := NewTreeRenderer(80, true) // no color for exact text assertions
	output := renderer.Render(report)

	// Verify header counts
	if !strings.Contains(output, "Changed symbols:        2") {
		t.Errorf("missing changed symbols count in output:\n%s", output)
	}
	if !strings.Contains(output, "Potential blast radius: 2 unique reference site(s) (3 total edges)") {
		t.Errorf("missing unique reference sites and total edges count in output:\n%s", output)
	}
	if !strings.Contains(output, "Candidate test files:   1") {
		t.Errorf("missing candidate test files count in output:\n%s", output)
	}

	// Verify both roots are rendered
	if !strings.Contains(output, "FunctionA (pkg/core.go:10)") {
		t.Errorf("missing FunctionA root in output:\n%s", output)
	}
	if !strings.Contains(output, "TypeB (pkg/types.go:25)") {
		t.Errorf("missing TypeB root in output:\n%s", output)
	}

	// Verify shared call site appears under both symbols
	if !strings.Contains(output, "pkg/consumer.go:42") {
		t.Errorf("missing shared reference in output:\n%s", output)
	}

	// Output must not contain unsupported breaking-change claims
	if strings.Contains(strings.ToLower(output), "breaking") {
		t.Errorf("output contains unsupported breaking claim:\n%s", output)
	}
}

func TestTreeRenderer_NoColorAndTermDumb(t *testing.T) {
	sym := Symbol{Name: "Foo", Kind: "function", FilePath: "foo.go", StartLine: 1}
	ref := RawReference{
		FilePath:     "bar.go",
		StartLine:    5,
		Precision:    PrecisionSyntactic,
		Relationship: RelCaller,
		SymbolName:   "Foo",
		SourceSymbol: &sym,
	}

	report := &ImpactReport{
		ChangedSymbols:        []Symbol{sym},
		ReferenceEdges:        []RawReference{ref},
		UniqueReferencesCount: 1,
		TotalEdgesCount:       1,
	}

	// Render with NoColor = true
	rendererNoColor := NewTreeRenderer(80, true)
	outNoColor := rendererNoColor.Render(report)

	// ANSI escape sequence is \x1b[
	if strings.Contains(outNoColor, "\x1b[") {
		t.Errorf("NoColor output unexpectedly contains ANSI escape sequences")
	}

	// Render with NoColor = false
	rendererColor := NewTreeRenderer(80, false)
	outColor := rendererColor.Render(report)
	if !strings.Contains(outColor, "\x1b[") {
		t.Errorf("Color output should contain ANSI escape sequences")
	}
}

func TestTreeRenderer_ControlCharsAndSanitization(t *testing.T) {
	// Malicious / control character injection in paths or symbol names
	dirtySym := Symbol{
		Name:      "Injected\x1b[31mRed\x1b[0mName\r\n",
		Kind:      "func\x00tion",
		FilePath:  "src/malicious\x1b[2Jpath.go",
		StartLine: 1,
	}
	dirtyRef := RawReference{
		FilePath:     "test/control\x07bell.go",
		StartLine:    10,
		Relationship: RelTest,
		SymbolName:   dirtySym.Name,
		SourceSymbol: &dirtySym,
	}

	report := &ImpactReport{
		ChangedSymbols:        []Symbol{dirtySym},
		ReferenceEdges:        []RawReference{dirtyRef},
		UniqueReferencesCount: 1,
		TotalEdgesCount:       1,
	}

	renderer := NewTreeRenderer(80, true)
	output := renderer.Render(report)

	// Verify terminal escape codes \x1b and bells \x07 were stripped
	if strings.Contains(output, "\x1b") {
		t.Errorf("output failed to sanitize escape character \\x1b")
	}
	if strings.Contains(output, "\x07") {
		t.Errorf("output failed to sanitize bell character \\x07")
	}
	if strings.Contains(output, "\x00") {
		t.Errorf("output failed to sanitize null byte \\x00")
	}
}

func TestTreeRenderer_NarrowTerminalAndTruncation(t *testing.T) {
	sym := Symbol{
		Name:      "SuperLongFunctionNameThatExceedsTheNarrowTerminalWidthSignificantly",
		Kind:      "function",
		FilePath:  "path/to/some/very/deeply/nested/directory/structure/in/the/repo/core.go",
		StartLine: 100,
	}

	var edges []RawReference
	for i := 1; i <= 60; i++ {
		edges = append(edges, RawReference{
			FilePath:     "path/to/some/very/deeply/nested/directory/structure/in/the/repo/caller.go",
			StartLine:    i * 10,
			Relationship: RelCaller,
			SymbolName:   sym.Name,
			SourceSymbol: &sym,
		})
	}

	report := &ImpactReport{
		ChangedSymbols:        []Symbol{sym},
		ReferenceEdges:        edges,
		UniqueReferencesCount: len(edges),
		TotalEdgesCount:       len(edges),
		Coverage: &CoverageReport{
			IncompleteDiscovery: true,
			UnsupportedSyntax:   []string{"dynamic macro in macro.rs"},
		},
	}

	// Narrow terminal: 50 columns
	renderer := NewTreeRenderer(50, true)
	output := renderer.Render(report)

	// Check truncation notice appears
	if !strings.Contains(output, "and 10 more reference(s)") {
		t.Errorf("expected reference truncation notice in output:\n%s", output)
	}

	// Incomplete discovery notice remains visible
	if !strings.Contains(output, "dynamic macro in macro.rs") {
		t.Errorf("expected incomplete discovery notice even with truncation:\n%s", output)
	}
}

func TestTreeRenderer_EmptyStates(t *testing.T) {
	// No changes state
	noChangesReport := &ImpactReport{
		Coverage: &CoverageReport{NoChanges: true},
	}
	renderer := NewTreeRenderer(80, true)
	out1 := renderer.Render(noChangesReport)
	if !strings.Contains(out1, "No changes detected") {
		t.Errorf("expected 'No changes detected', got:\n%s", out1)
	}

	// No references state
	noRefsReport := &ImpactReport{
		ChangedSymbols: []Symbol{{Name: "Orphan", Kind: "function", FilePath: "orphan.go", StartLine: 1}},
		Coverage:       &CoverageReport{NoReferences: true},
	}
	out2 := renderer.Render(noRefsReport)
	if !strings.Contains(out2, "no downstream references discovered") {
		t.Errorf("expected 'no downstream references discovered', got:\n%s", out2)
	}
}
