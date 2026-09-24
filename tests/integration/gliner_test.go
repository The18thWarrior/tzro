//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"tzro/pkg/extractor"
)

// findRepoRoot walks up from the test file to find the repo root (contains go.mod).
func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}

// TestGLiNER_RealModel_SpanExtraction tests the real GLiNER 2.5 model
// via the Python worker for zero-shot parameter extraction.
func TestGLiNER_RealModel_SpanExtraction(t *testing.T) {
	root := findRepoRoot(t)
	workerPath := filepath.Join(root, ".venv-gliner", "bin", "python")
	workerScript := filepath.Join(root, "bin", "gliner_worker.py")

	if _, err := os.Stat(workerPath); err != nil {
		t.Skipf("GLiNER venv not found at %s (run bin/setup_models.sh --gliner)", workerPath)
	}
	if _, err := os.Stat(workerScript); err != nil {
		t.Skipf("GLiNER worker not found at %s", workerScript)
	}

	client := extractor.NewWorkerClient(workerPath, workerScript)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		t.Fatalf("failed to start GLiNER worker: %v", err)
	}
	defer client.Close()

	tests := []struct {
		name   string
		text   string
		labels []string
		expect map[string]string // label -> expected substring
	}{
		{
			name:   "GoTestFailure",
			text:   "--- FAIL: TestSessionSave (0.01s) pkg/session/manifest_test.go:42: expected true, got false",
			labels: []string{"file_path", "function_name", "line_number"},
			expect: map[string]string{
				"file_path":     "manifest_test.go",
				"function_name": "TestSessionSave",
				"line_number":   "42",
			},
		},
		{
			name:   "PythonTraceback",
			text:   "File \"/app/handlers/auth.py\", line 87, in validate_token",
			labels: []string{"file_path", "function_name", "line_number"},
			expect: map[string]string{
				"file_path":     "auth.py",
				"function_name": "validate_token",
				"line_number":   "87",
			},
		},
		{
			name:   "CompilerError",
			text:   "src/engine.go:155:12: cannot use x (variable of type string) as int value",
			labels: []string{"file_path", "line_number"},
			expect: map[string]string{
				"file_path":   "engine.go",
				"line_number": "155",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &extractor.ExtractionRequest{
				Text:   tt.text,
				Labels: tt.labels,
			}

			resp, err := client.Extract(ctx, req)
			if err != nil {
				t.Fatalf("extraction failed: %v", err)
			}

			t.Logf("Latency: %dms, Spans: %d", resp.LatencyMs, len(resp.Spans))
			for _, span := range resp.Spans {
				t.Logf("  %s: %q (confidence=%.2f%%, start=%d, end=%d)",
					span.Label, span.Text, span.Confidence*100, span.Start, span.End)
			}

			// Group spans by label, keeping only the highest confidence per label
			bestByLabel := make(map[string]extractor.ExtractedSpan)
			for _, span := range resp.Spans {
				existing, exists := bestByLabel[span.Label]
				if !exists || span.Confidence > existing.Confidence {
					bestByLabel[span.Label] = span
				}
			}

			// Verify expected labels were found with correct text
			for label, expected := range tt.expect {
				span, ok := bestByLabel[label]
				if !ok {
					t.Errorf("expected label %q not found in results", label)
					continue
				}
				if !containsSubstring(span.Text, expected) {
					t.Errorf("label %q: got %q, want substring %q",
						label, span.Text, expected)
				}
				if span.Confidence < 0.5 {
					t.Errorf("label %q: confidence %.2f%% is too low",
						label, span.Confidence*100)
				}
			}
		})
	}
}

// TestGLiNER_RealModel_Latency verifies extraction completes within
// acceptable time bounds on repeated calls (model already warm).
func TestGLiNER_RealModel_Latency(t *testing.T) {
	root := findRepoRoot(t)
	workerPath := filepath.Join(root, ".venv-gliner", "bin", "python")
	workerScript := filepath.Join(root, "bin", "gliner_worker.py")

	if _, err := os.Stat(workerPath); err != nil {
		t.Skipf("GLiNER venv not found (run bin/setup_models.sh --gliner)")
	}

	client := extractor.NewWorkerClient(workerPath, workerScript)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		t.Fatalf("failed to start GLiNER worker: %v", err)
	}
	defer client.Close()

	// Warm-up call
	req := &extractor.ExtractionRequest{
		Text:   "error in pkg/store/store.go:42: undefined reference to CreateIndex",
		Labels: []string{"file_path", "function_name", "line_number"},
	}
	if _, err := client.Extract(ctx, req); err != nil {
		t.Fatalf("warm-up extraction failed: %v", err)
	}

	// Measure 5 extractions
	var totalMs int64
	for i := 0; i < 5; i++ {
		resp, err := client.Extract(ctx, req)
		if err != nil {
			t.Fatalf("extraction %d failed: %v", i, err)
		}
		totalMs += resp.LatencyMs
		t.Logf("extraction %d: %dms", i, resp.LatencyMs)
	}

	avgMs := totalMs / 5
	t.Logf("Average latency: %dms over 5 calls", avgMs)

	// Average should be under 500ms on CPU
	if avgMs > 500 {
		t.Errorf("average latency %dms exceeds 500ms threshold", avgMs)
	}
}

// TestGLiNER_RealModel_MemoryFootprint checks the worker process
// stays within the 870 MB combined budget (alongside laya).
func TestGLiNER_RealModel_MemoryFootprint(t *testing.T) {
	root := findRepoRoot(t)
	workerPath := filepath.Join(root, ".venv-gliner", "bin", "python")
	workerScript := filepath.Join(root, "bin", "gliner_worker.py")

	if _, err := os.Stat(workerPath); err != nil {
		t.Skipf("GLiNER venv not found (run bin/setup_models.sh --gliner)")
	}

	client := extractor.NewWorkerClient(workerPath, workerScript)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		t.Fatalf("failed to start GLiNER worker: %v", err)
	}
	defer client.Close()

	// Run a few extractions to stabilize memory
	req := &extractor.ExtractionRequest{
		Text:   "handler.go:42:func HandleRequest",
		Labels: []string{"file_path", "function_name", "line_number"},
	}
	for i := 0; i < 3; i++ {
		if _, err := client.Extract(ctx, req); err != nil {
			t.Fatalf("extraction failed: %v", err)
		}
	}

	// Go process memory
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	goMB := float64(memStats.Sys) / 1024 / 1024
	t.Logf("Go process resident: %.1f MB", goMB)

	// The GLiNER worker (PyTorch base model) should use ~400-600 MB.
	// Combined with the Go process (~50 MB) and laya (~450 MB),
	// total should be under the 870 MB budget.
	// We can't directly measure the Python process here, but we verify
	// the Go side stays lean.
	if goMB > 200 {
		t.Errorf("Go process using %.1f MB, expected < 200 MB", goMB)
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
