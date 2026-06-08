package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tzro/internal/config"
	"tzro/internal/telemetry"
)

func TestNewBackendFactory(t *testing.T) {
	// 1. llama-server (default)
	cfgDefault := config.BackendConfig{Type: ""}
	backendDefault := NewBackend(cfgDefault, telemetry.Default)
	if _, ok := backendDefault.(*LlamaServerBackend); !ok {
		t.Errorf("Expected LlamaServerBackend, got %T", backendDefault)
	}

	cfgLlama := config.BackendConfig{Type: "llama-server"}
	backendLlama := NewBackend(cfgLlama, telemetry.Default)
	if _, ok := backendLlama.(*LlamaServerBackend); !ok {
		t.Errorf("Expected LlamaServerBackend, got %T", backendLlama)
	}

	// 2. openai-compatible
	cfgRemote := config.BackendConfig{
		Type:  "openai-compatible",
		URL:   "http://localhost:11434/v1",
		Model: "some-model",
	}
	backendRemote := NewBackend(cfgRemote, telemetry.Default)
	remoteBackend, ok := backendRemote.(*RemoteOpenAIBackend)
	if !ok {
		t.Fatalf("Expected RemoteOpenAIBackend, got %T", backendRemote)
	}
	if remoteBackend.model != "some-model" {
		t.Errorf("Expected model 'some-model', got '%s'", remoteBackend.model)
	}
	if remoteBackend.url != "http://localhost:11434/v1/chat/completions" {
		t.Errorf("Expected cleaned url 'http://localhost:11434/v1/chat/completions', got '%s'", remoteBackend.url)
	}
}

func TestRemoteOpenAIBackend_CallModel(t *testing.T) {
	// Setup mock HTTP server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected application/json Content-Type, got %s", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Expected Bearer test-key Authorization, got %s", r.Header.Get("Authorization"))
		}

		// Decode request to verify model & prompts
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Failed to decode request body: %v", err)
		}
		if req.Model != "test-model" {
			t.Errorf("Expected model 'test-model', got '%s'", req.Model)
		}

		// Return standard OpenAI completion response
		w.Header().Set("Content-Type", "application/json")
		response := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]string{
						"role":    "assistant",
						"content": `{"status":"ok"}`,
					},
				},
			},
			"usage": map[string]int{
				"prompt_tokens":     15,
				"completion_tokens": 10,
			},
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	cfg := config.BackendConfig{
		Type:   "openai-compatible",
		URL:    mockServer.URL,
		Model:  "test-model",
		APIKey: "test-key",
	}

	backend := NewRemoteOpenAIBackend(cfg, telemetry.Default)
	res, err := backend.CallModel(context.Background(), []InferenceMessage{{Role: "system", Content: "System prompt"}, {Role: "user", Content: "User prompt"}}, "")
	if err != nil {
		t.Fatalf("CallModel failed: %v", err)
	}

	if res.Content != `{"status":"ok"}` {
		t.Errorf("Expected content '{\"status\":\"ok\"}', got '%s'", res.Content)
	}
	if res.PromptTokens != 15 {
		t.Errorf("Expected 15 prompt tokens, got %d", res.PromptTokens)
	}
	if res.CompletionTokens != 10 {
		t.Errorf("Expected 10 completion tokens, got %d", res.CompletionTokens)
	}
}

func TestRemoteOpenAIBackend_Status(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	cfg := config.BackendConfig{
		Type: "openai-compatible",
		URL:  mockServer.URL,
	}

	backend := NewRemoteOpenAIBackend(cfg, telemetry.Default)
	if status := backend.Status(); status != "active" {
		t.Errorf("Expected status 'active', got '%s'", status)
	}
}
