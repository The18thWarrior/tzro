package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tzro/internal/inference"
)

func TestQuickstartFlow(t *testing.T) {
	// Create a temp directory for isolated configuration and DB
	tempDir, err := os.MkdirTemp("", "tzro-quickstart-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "quickstart.db")

	// Start a mock HTTP server to simulate llama-server local sidecar (SSE streaming)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read body to see which node is requesting
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		var toolCallContent string
		// Determine which tool is being targeted based on prompt
		isMath := false
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "custom_math_tool") {
				isMath = true
				break
			}
		}

		if isMath {
			toolCallContent = `{"tool_arguments": {"a": 15, "b": 35}}`
		} else {
			// slack_message tool expectations
			toolCallContent = `{"tool_arguments": {"channel": "general", "text": "Confirm the sum is 50."}}`
		}

		// SSE stream headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Send delta content chunk
		chunk := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"delta": map[string]string{
						"content": toolCallContent,
					},
				},
			},
		}
		bytesVal, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", string(bytesVal))

		// Send usage chunk
		usageChunk := map[string]interface{}{
			"usage": map[string]interface{}{
				"prompt_tokens":     100,
				"completion_tokens": 20,
			},
		}
		bytesUsage, _ := json.Marshal(usageChunk)
		fmt.Fprintf(w, "data: %s\n\n", string(bytesUsage))

		// Send DONE signal
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	// Parse port from local mock server
	_, portStr, _ := net.SplitHostPort(server.Listener.Addr().String())
	var mockPort int
	_, _ = fmt.Sscanf(portStr, "%d", &mockPort)

	// Save original sidecar state and inject mock server coordinates
	oldStatus, oldPort, oldPID, oldProgress, oldModel := inference.GlobalLocalModel.GetStatusInfo()
	
	inference.GlobalLocalModel.Status = "Adopted"
	inference.GlobalLocalModel.ActivePort = mockPort

	defer func() {
		inference.GlobalLocalModel.Status = oldStatus
		inference.GlobalLocalModel.ActivePort = oldPort
		inference.GlobalLocalModel.ActivePID = oldPID
		inference.GlobalLocalModel.ManifestProgress = oldProgress
		inference.GlobalLocalModel.GGUFModelPath = oldModel
	}()

	// Run quickstart with 5-second timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = RunQuickstart(ctx, dbPath)
	if err != nil {
		t.Fatalf("RunQuickstart failed with error: %v", err)
	}
}
