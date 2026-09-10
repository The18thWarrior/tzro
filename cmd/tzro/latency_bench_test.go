package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tzro/pkg/compactor"
	tzroctx "tzro/pkg/context"
	"tzro/pkg/inspector"
	"tzro/pkg/search"
	"tzro/pkg/store"
)

// Benchmark 1: Compaction Evidence: <100ms for 100K lines
func TestLatency_Compaction_100kLines(t *testing.T) {
	st, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Build 100k lines of compiler/test diagnostics
	var b strings.Builder
	for i := 0; i < 100000; i++ {
		if i%10 == 0 {
			b.WriteString(fmt.Sprintf("pkg/foo/bar.go:%d:10: error: undefined variable foo%d\n", i, i))
		} else {
			b.WriteString(fmt.Sprintf("some verbose informational runtime build output log line %d\n", i))
		}
	}
	data := b.String()

	start := time.Now()
	exitCode := 1
	res, err := compactor.CompactEvidence(data, "/workspace", st, nil, &exitCode)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Compaction 100K lines: elapsed = %v (target: <100ms), diagnostics = %d", elapsed, len(res.Diagnostics))
	if elapsed > 100*time.Millisecond {
		t.Errorf("Compaction exceeded target 100ms: took %v", elapsed)
	}
}

// Benchmark 2: Context Inspector: <100ms trace write, <200ms replay
func TestLatency_ContextInspector_WriteAndReplay(t *testing.T) {
	st, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	engine := inspector.NewEngine(st, nil)

	trace := &inspector.Trace{
		ID:        "trace-bench-1",
		Workspace: "/workspace",
		CreatedAt: time.Now().UTC(),
		Query:     "bench query",
		Config: inspector.TraceConfig{
			Budget: 1000,
		},
		Discovery: []inspector.CandidateTrace{
			{Path: "pkg/foo/foo.go", Score: 0.95, Tokens: 150, SourceKind: "code"},
			{Path: "pkg/bar/bar.go", Score: 0.85, Tokens: 200, SourceKind: "code"},
		},
		Ranking: []inspector.RankedCandidate{
			{Path: "pkg/foo/foo.go", Score: 0.95, Rank: 1},
			{Path: "pkg/bar/bar.go", Score: 0.85, Rank: 2},
		},
		Packing: inspector.PackingStage{
			TotalBudget:     1000,
			BudgetRemaining: 650,
			IncludedItems: []inspector.PackedItem{
				{Path: "pkg/foo/foo.go", TokensUsed: 150, Tier: inspector.TierMeasured},
				{Path: "pkg/bar/bar.go", TokensUsed: 200, Tier: inspector.TierMeasured},
			},
		},
	}

	// Measure write latency
	startWrite := time.Now()
	err = engine.RecordTrace(trace)
	writeElapsed := time.Since(startWrite)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Inspector trace write: elapsed = %v (target: <100ms)", writeElapsed)
	if writeElapsed > 100*time.Millisecond {
		t.Errorf("Trace write exceeded target 100ms: took %v", writeElapsed)
	}

	// Measure replay latency
	startReplay := time.Now()
	replayRes, err := engine.Replay("trace-bench-1", "/workspace", 500)
	replayElapsed := time.Since(startReplay)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Inspector trace replay: elapsed = %v (target: <200ms), packed = %d", replayElapsed, len(replayRes.IncludedItems))
	if replayElapsed > 200*time.Millisecond {
		t.Errorf("Trace replay exceeded target 200ms: took %v", replayElapsed)
	}
}

// Benchmark 3: Evidence Search: <500ms / 5k files
func TestLatency_EvidenceSearch_5kFiles(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := store.OpenStore(filepath.Join(tmpDir, "tzro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Generate 5k files
	repoDir := filepath.Join(tmpDir, "repo")
	_ = os.MkdirAll(repoDir, 0755)

	for i := 0; i < 5000; i++ {
		subDir := filepath.Join(repoDir, fmt.Sprintf("dir_%d", i%50))
		_ = os.MkdirAll(subDir, 0755)
		filePath := filepath.Join(subDir, fmt.Sprintf("file_%d.go", i))
		content := fmt.Sprintf("package main\n\n// comment %d\nfunc Symbol_%d() {}\n", i, i)
		if i == 2500 {
			content += "// TargetSearchTermUnique\nfunc TargetQuery() {}\n"
		}
		_ = os.WriteFile(filePath, []byte(content), 0644)
	}

	engine := search.NewEngine(st, nil)

	start := time.Now()
	res, err := engine.Search(repoDir, "TargetSearchTermUnique", 2000)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	allocMB := float64(m.Alloc) / (1024 * 1024)

	t.Logf("Evidence search 5k files: elapsed = %v (target: <500ms), found = %d, alloc = %.2fMB (target: <50MB)", elapsed, len(res.Items), allocMB)
	if elapsed > 500*time.Millisecond {
		t.Errorf("Evidence search exceeded target 500ms: took %v", elapsed)
	}
	if allocMB > 50.0 {
		t.Errorf("Evidence search exceeded target 50MB RSS/Alloc: alloc was %.2fMB", allocMB)
	}
}

// Benchmark 4: Impact Context: <2s on 5k-file workspace
func TestLatency_ImpactContext_5kFiles(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := store.OpenStore(filepath.Join(tmpDir, "tzro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repoDir := filepath.Join(tmpDir, "repo")
	_ = os.MkdirAll(repoDir, 0755)

	for i := 0; i < 5000; i++ {
		subDir := filepath.Join(repoDir, fmt.Sprintf("dir_%d", i%50))
		_ = os.MkdirAll(subDir, 0755)
		filePath := filepath.Join(subDir, fmt.Sprintf("file_%d.go", i))
		content := fmt.Sprintf("package dir_%d\n\nfunc Helper_%d() {}\n", i%50, i)
		if i%100 == 0 {
			content += "func TargetFunc() { TargetSymbol() }\n"
		}
		_ = os.WriteFile(filePath, []byte(content), 0644)
	}

	analyzer := tzroctx.NewImpactAnalyzer(st, nil)

	start := time.Now()
	pack, err := analyzer.AnalyzeSymbol(repoDir, "TargetSymbol", 2000, false)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Impact analysis 5k files: elapsed = %v (target: <2s), items = %d", elapsed, len(pack.Items))
	if elapsed > 2*time.Second {
		t.Errorf("Impact analysis exceeded target 2s: took %v", elapsed)
	}
}
