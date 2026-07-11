package inference

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"
)

// startMockLlamaServer creates a minimal mock llama-server that returns
// a fixed response. Returns the listener (for port extraction) and cleanup func.
func startMockLlamaServer(t *testing.T, response string) (int, func()) {
	t.Helper()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": response}},
				},
				"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start mock server: %v", err)
	}

	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()

	port := listener.Addr().(*net.TCPAddr).Port
	cleanup := func() {
		_ = server.Close()
	}
	return port, cleanup
}

func TestCallWorker_UsesWorkerBackend(t *testing.T) {
	// Start a mock server and point the worker at it
	port, cleanup := startMockLlamaServer(t, `{"source":"worker"}`)
	defer cleanup()

	// Save and restore global state
	savedPort := GlobalWorkerModel.ActivePort
	savedStatus := GlobalWorkerModel.Status
	defer func() {
		GlobalWorkerModel.ActivePort = savedPort
		GlobalWorkerModel.Status = savedStatus
	}()

	GlobalWorkerModel.ActivePort = port
	GlobalWorkerModel.Status = "Active"

	msgs := []InferenceMessage{
		{Role: "system", Content: "test"},
		{Role: "user", Content: "hello"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := CallWorker(ctx, msgs, "")
	if err != nil {
		t.Fatalf("CallWorker failed: %v", err)
	}
	if result.Content != `{"source":"worker"}` {
		t.Errorf("expected worker response, got %q", result.Content)
	}
}

func TestCallRouter_UsesRouterBackend(t *testing.T) {
	// Start mock servers for both — router should hit the router one
	routerPort, routerCleanup := startMockLlamaServer(t, `{"source":"router"}`)
	defer routerCleanup()
	workerPort, workerCleanup := startMockLlamaServer(t, `{"source":"worker"}`)
	defer workerCleanup()

	// Save and restore
	savedRouterPort := GlobalRouterModel.ActivePort
	savedRouterStatus := GlobalRouterModel.Status
	savedWorkerPort := GlobalWorkerModel.ActivePort
	savedWorkerStatus := GlobalWorkerModel.Status
	defer func() {
		GlobalRouterModel.ActivePort = savedRouterPort
		GlobalRouterModel.Status = savedRouterStatus
		GlobalWorkerModel.ActivePort = savedWorkerPort
		GlobalWorkerModel.Status = savedWorkerStatus
	}()

	GlobalRouterModel.ActivePort = routerPort
	GlobalRouterModel.Status = "Active"
	GlobalWorkerModel.ActivePort = workerPort
	GlobalWorkerModel.Status = "Active"

	msgs := []InferenceMessage{
		{Role: "system", Content: "test"},
		{Role: "user", Content: "classify"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := CallRouter(ctx, msgs, "")
	if err != nil {
		t.Fatalf("CallRouter failed: %v", err)
	}
	if result.Content != `{"source":"router"}` {
		t.Errorf("expected router response, got %q", result.Content)
	}
}

func TestCallRouter_FallsBackToWorker_WhenRouterUnavailable(t *testing.T) {
	// Only start a worker mock — router is "Stopped"
	workerPort, workerCleanup := startMockLlamaServer(t, `{"source":"worker-fallback"}`)
	defer workerCleanup()

	// Save and restore
	savedRouterStatus := GlobalRouterModel.Status
	savedWorkerPort := GlobalWorkerModel.ActivePort
	savedWorkerStatus := GlobalWorkerModel.Status
	defer func() {
		GlobalRouterModel.Status = savedRouterStatus
		GlobalWorkerModel.ActivePort = savedWorkerPort
		GlobalWorkerModel.Status = savedWorkerStatus
	}()

	GlobalRouterModel.Status = "Stopped"
	GlobalWorkerModel.ActivePort = workerPort
	GlobalWorkerModel.Status = "Active"

	msgs := []InferenceMessage{
		{Role: "system", Content: "test"},
		{Role: "user", Content: "classify"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := CallRouter(ctx, msgs, "")
	if err != nil {
		t.Fatalf("CallRouter fallback failed: %v", err)
	}
	if result.Content != `{"source":"worker-fallback"}` {
		t.Errorf("expected worker fallback response, got %q", result.Content)
	}
}
