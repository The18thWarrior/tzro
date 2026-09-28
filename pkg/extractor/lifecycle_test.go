package extractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkerStartupHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slow")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 1\necho '{\"status\":\"ready\"}'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	client := NewWorkerClient(path)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := client.Start(ctx); err == nil || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("startup ignored deadline: %v, %v", err, time.Since(start))
	}
}

func TestWorkerErrorIsNotAnEmptySuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "error")
	script := "#!/bin/sh\necho '{\"status\":\"ready\"}'\nwhile IFS= read -r line; do echo '{\"error\":\"model unavailable\",\"spans\":[]}'; done\n"
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	client := NewWorkerClient(path)
	defer client.Close()
	if _, err := client.Extract(context.Background(), &ExtractionRequest{Text: "main.go", Labels: []string{"file_path"}}); err == nil {
		t.Fatal("worker error was treated as successful extraction")
	}
}
