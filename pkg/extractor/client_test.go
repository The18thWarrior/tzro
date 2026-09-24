package extractor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var mockWorkerPath string

func TestMain(m *testing.M) {
	// Compile mock worker
	tmpDir, err := os.MkdirTemp("", "extractor_test")
	if err != nil {
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	mockWorkerPath = filepath.Join(tmpDir, "mock_worker")
	cmd := exec.Command("go", "build", "-o", mockWorkerPath, "./testdata/mock_worker.go")
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func TestExtract_SpanBoundary(t *testing.T) {
	client := NewWorkerClient(mockWorkerPath)
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer client.Close()

	req := &ExtractionRequest{
		Text:   "FAIL: TestSessionSave\n  pkg/session/manifest_test.go:42",
		Labels: []string{"file_path", "function_name", "line_number"},
	}

	resp, err := client.Extract(ctx, req)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if len(resp.Spans) != 3 {
		t.Fatalf("Expected 3 spans, got %d", len(resp.Spans))
	}

	expectedLabels := map[string]string{
		"file_path":     "pkg/session/manifest_test.go",
		"function_name": "TestSessionSave",
		"line_number":   "42",
	}

	for _, span := range resp.Spans {
		expectedText, ok := expectedLabels[span.Label]
		if !ok {
			t.Errorf("Unexpected label: %s", span.Label)
			continue
		}
		if span.Text != expectedText {
			t.Errorf("Expected text %q for label %q, got %q", expectedText, span.Label, span.Text)
		}
	}
}

func TestExtract_EmptyInput(t *testing.T) {
	client := NewWorkerClient(mockWorkerPath)
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer client.Close()

	req := &ExtractionRequest{
		Text:   "",
		Labels: []string{"file_path", "function_name", "line_number"},
	}

	resp, err := client.Extract(ctx, req)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if len(resp.Spans) != 0 {
		t.Fatalf("Expected 0 spans, got %d", len(resp.Spans))
	}
}

func TestExtract_NoMatch(t *testing.T) {
	client := NewWorkerClient(mockWorkerPath)
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer client.Close()

	req := &ExtractionRequest{
		Text:   "hello world",
		Labels: []string{"file_path", "function_name", "line_number"},
	}

	resp, err := client.Extract(ctx, req)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if len(resp.Spans) != 0 {
		t.Fatalf("Expected 0 spans, got %d", len(resp.Spans))
	}
}
