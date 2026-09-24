//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tzro/pkg/executor"
	"tzro/pkg/store"
)

// TestMiniBenchmark_LargeFileExploration verifies that tzro skeleton + probe
// achieves >= 80% token reduction compared to raw file reading for a large source file.
func TestMiniBenchmark_LargeFileExploration(t *testing.T) {
	workspace := t.TempDir()

	// Generate a 2000-line Go file with many handler functions
	var source strings.Builder
	source.WriteString("package main\n\nimport \"fmt\"\n\n")
	for i := 0; i < 100; i++ {
		source.WriteString(fmt.Sprintf("// HandleRequest%d processes type %d requests.\n", i, i))
		source.WriteString(fmt.Sprintf("// It validates input, applies business rules, and returns a response.\n"))
		source.WriteString(fmt.Sprintf("// Additional documentation line for padding.\n"))
		source.WriteString(fmt.Sprintf("func HandleRequest%d(w ResponseWriter, r *Request) {\n", i))
		for j := 0; j < 20; j++ {
			source.WriteString(fmt.Sprintf("\tfmt.Println(\"processing step %d for handler %d with extra context and details\")\n", j, i))
		}
		source.WriteString("}\n\n")
	}

	largeFile := filepath.Join(workspace, "handlers.go")
	if err := os.WriteFile(largeFile, []byte(source.String()), 0o644); err != nil {
		t.Fatalf("writing large file: %v", err)
	}

	rawTokens := estimateTokens(source.String())

	// Execute via tzro graph: skeleton + probe
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	defer s.Close()

	disp := executor.NewBuiltinDispatcher(workspace, s)

	g := &executor.Graph{
		Version: "3.0",
		TaskID:  "large-file-explore",
		Nodes: []executor.Node{
			{
				ID:   "skeleton",
				Type: executor.NodeTypeTool,
				Tool: "skeleton",
				Args: map[string]interface{}{"file": largeFile},
			},
			{
				ID:   "probe",
				Type: executor.NodeTypeTool,
				Tool: "probe",
				Args: map[string]interface{}{
					"query":       "HandleRequest42",
					"max_results": float64(5),
				},
			},
		},
	}

	engine := executor.NewEngine(
		executor.WithMaxConcurrency(2),
		executor.WithToolDispatcher(disp),
	)

	start := time.Now()
	result, err := engine.Execute(context.Background(), g)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed, got %q", result.Status)
	}

	// Calculate token reduction from skeleton
	skeletonOutput := result.Outputs["skeleton"]
	skeletonData := skeletonOutput.Data
	skeletonCode, _ := skeletonData["skeleton"].(string)
	skeletonTokens := estimateTokens(skeletonCode)

	// Probe output is even smaller
	probeOutput := result.Outputs["probe"]
	probeData := probeOutput.Data
	probeMarkdown, _ := probeData["markdown"].(string)
	probeTokens := estimateTokens(probeMarkdown)

	// Combined output tokens (skeleton + probe) vs raw file
	combinedTokens := skeletonTokens + probeTokens
	reduction := 1.0 - float64(combinedTokens)/float64(rawTokens)

	t.Logf("Raw file: %d tokens", rawTokens)
	t.Logf("Skeleton: %d tokens", skeletonTokens)
	t.Logf("Probe: %d tokens", probeTokens)
	t.Logf("Combined: %d tokens", combinedTokens)
	t.Logf("Token reduction: %.1f%%", reduction*100)
	t.Logf("Execution time: %v", elapsed)

	if reduction < 0.80 {
		t.Errorf("expected >= 80%% token reduction, got %.1f%%", reduction*100)
	}
}

// TestMiniBenchmark_TabularDataAnalysis verifies that auto-ingest SQL query
// achieves >= 95% token reduction compared to raw CSV dump.
func TestMiniBenchmark_TabularDataAnalysis(t *testing.T) {
	workspace := t.TempDir()

	// Generate a 10,000-row CSV
	var csv strings.Builder
	csv.WriteString("id,name,value,category,region,timestamp\n")
	categories := []string{"cpu", "memory", "disk", "network", "latency"}
	regions := []string{"us-east-1", "us-west-2", "eu-west-1", "ap-south-1"}
	for i := 0; i < 10000; i++ {
		csv.WriteString(fmt.Sprintf("%d,metric_%d,%d,%s,%s,2024-01-%02dT%02d:%02d:00Z\n",
			i, i, i*17%1000, categories[i%5], regions[i%4],
			(i%28)+1, (i % 24), (i % 60)))
	}

	csvFile := filepath.Join(workspace, "large_metrics.csv")
	if err := os.WriteFile(csvFile, []byte(csv.String()), 0o644); err != nil {
		t.Fatalf("writing CSV: %v", err)
	}

	rawTokens := estimateTokens(csv.String())

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	defer s.Close()

	disp := executor.NewBuiltinDispatcher(workspace, s)

	g := &executor.Graph{
		Version: "3.0",
		TaskID:  "tabular-analysis",
		Nodes: []executor.Node{
			{
				ID:   "query_stats",
				Type: executor.NodeTypeTool,
				Tool: "query",
				Args: map[string]interface{}{
					"file": csvFile,
					"sql":  "SELECT category, COUNT(*) as cnt, AVG(CAST(value AS REAL)) as avg_val FROM large_metrics GROUP BY category ORDER BY cnt DESC",
				},
			},
		},
	}

	engine := executor.NewEngine(
		executor.WithMaxConcurrency(1),
		executor.WithToolDispatcher(disp),
	)

	start := time.Now()
	result, err := engine.Execute(context.Background(), g)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed, got %q", result.Status)
	}

	// Calculate query result token count
	queryOutput := result.Outputs["query_stats"]
	queryJSON, _ := json.Marshal(queryOutput.Data)
	queryTokens := estimateTokens(string(queryJSON))

	reduction := 1.0 - float64(queryTokens)/float64(rawTokens)

	t.Logf("Raw CSV: %d tokens (%d bytes)", rawTokens, len(csv.String()))
	t.Logf("Query result: %d tokens", queryTokens)
	t.Logf("Token reduction: %.1f%%", reduction*100)
	t.Logf("Execution time: %v", elapsed)

	if reduction < 0.95 {
		t.Errorf("expected >= 95%% token reduction, got %.1f%%", reduction*100)
	}

	// Verify results are reasonable
	rows, ok := queryOutput.Data["rows"].([]map[string]string)
	if !ok {
		t.Fatalf("expected rows in query result, got %T", queryOutput.Data["rows"])
	}
	if len(rows) != 5 {
		t.Errorf("expected 5 category groups, got %d", len(rows))
	}
}

// TestMiniBenchmark_MultiStepDiagnosis verifies a multi-step diagnostic graph
// executes in < 2.0s with flat memory usage.
func TestMiniBenchmark_MultiStepDiagnosis(t *testing.T) {
	workspace := t.TempDir()

	// Create a Go file for probing
	goFile := filepath.Join(workspace, "handler.go")
	if err := os.WriteFile(goFile, []byte(`package main

func HandleAuth(token string) (bool, error) {
	if token == "" {
		return false, fmt.Errorf("empty token")
	}
	return true, nil
}
`), 0o644); err != nil {
		t.Fatalf("writing go file: %v", err)
	}

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	defer s.Close()

	disp := executor.NewBuiltinDispatcher(workspace, s)

	// Multi-step graph: bash (test) → probe (symbol) → skeleton (file)
	g := &executor.Graph{
		Version: "3.0",
		TaskID:  "multi-step-diagnosis",
		Nodes: []executor.Node{
			{
				ID:   "run_test",
				Type: executor.NodeTypeTool,
				Tool: "bash",
				Args: map[string]interface{}{
					"command":             "echo '--- FAIL: TestHandleAuth (0.01s)\n    handler_test.go:15: expected true, got false'",
					"accepted_exit_codes": []interface{}{float64(0), float64(1)},
				},
			},
			{
				ID:        "probe_symbol",
				Type:      executor.NodeTypeTool,
				Tool:      "probe",
				DependsOn: []string{"run_test"},
				Args: map[string]interface{}{
					"query":       "HandleAuth",
					"max_results": float64(5),
				},
			},
			{
				ID:        "skeleton_file",
				Type:      executor.NodeTypeTool,
				Tool:      "skeleton",
				DependsOn: []string{"probe_symbol"},
				Args: map[string]interface{}{
					"file": goFile,
				},
			},
		},
	}

	engine := executor.NewEngine(
		executor.WithMaxConcurrency(2),
		executor.WithToolDispatcher(disp),
	)

	// Measure memory before
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	start := time.Now()
	result, err := engine.Execute(context.Background(), g)
	elapsed := time.Since(start)

	// Measure memory after
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed, got %q", result.Status)
	}

	// Verify all nodes completed
	for _, id := range []string{"run_test", "probe_symbol", "skeleton_file"} {
		out, ok := result.Outputs[id]
		if !ok {
			t.Errorf("missing output for %q", id)
			continue
		}
		if out.Status != "completed" {
			t.Errorf("node %q: status = %q, want 'completed'", id, out.Status)
		}
	}

	// Memory delta (heap allocated during the test)
	memDeltaMB := float64(memAfter.TotalAlloc-memBefore.TotalAlloc) / 1024 / 1024

	t.Logf("Total DAG execution time: %v", elapsed)
	t.Logf("Memory delta (heap alloc): %.1f MB", memDeltaMB)
	t.Logf("Resident memory (sys): %.1f MB", float64(memAfter.Sys)/1024/1024)

	if elapsed > 2*time.Second {
		t.Errorf("expected total execution < 2s, got %v", elapsed)
	}

	// Memory check: the Go process itself should use < 870 MB
	// (we check that the test delta is reasonable, not total system memory
	//  since we're not running the full daemon stack)
	if memDeltaMB > 100 {
		t.Errorf("expected heap alloc delta < 100 MB for graph execution, got %.1f MB", memDeltaMB)
	}
}

func estimateTokens(text string) int {
	n := int(float64(len(text)) / 3.8)
	if n == 0 && len(text) > 0 {
		n = 1
	}
	return n
}
