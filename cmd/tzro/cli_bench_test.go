package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_BenchSignalDensity(t *testing.T) {
	// Mock server for fast CLI execution test
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]string{
						"role":    "assistant",
						"content": "3 running instances",
					},
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     50,
				"completion_tokens": 10,
				"cost":              0.0001,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	outJSON := filepath.Join(tmpDir, "cli_test_sdm.json")

	rootCmd := newRootCmd()
	var outBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetArgs([]string{
		"bench", "signal-density",
		"--primitive", "json",
		"--tier", "micro",
		"--base-url", server.URL,
		"--output", outJSON,
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("CLI command execution failed: %v", err)
	}

	// Verify output JSON file exists and has content
	data, err := os.ReadFile(outJSON)
	if err != nil {
		t.Fatalf("failed to read generated output JSON: %v", err)
	}

	if !strings.Contains(string(data), "smart_json") {
		t.Errorf("expected smart_json in output JSON, got:\n%s", string(data))
	}
}
