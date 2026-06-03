package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfig_DelegatedSecrets(t *testing.T) {
	// Create a temporary isolated config folder
	tempDir, err := os.MkdirTemp("", "tzro-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldConfigPath := configPath
	configPath = filepath.Join(tempDir, "config.json")
	defer func() { configPath = oldConfigPath }()

	// Configure environment variables
	os.Setenv("TEST_ENV_API_KEY", "resolved-api-key-123")
	defer os.Unsetenv("TEST_ENV_API_KEY")

	// Set dynamic ref
	cfg := &EngineConfig{
		ModelMode:     "cooperative",
		CloudProvider: "google",
		CloudAPIKey:   "$TEST_ENV_API_KEY",
	}

	if err := Save(cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// Trigger load
	if err := Load(); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	resolvedKey := GetCloudAPIKey()
	if resolvedKey != "resolved-api-key-123" {
		t.Errorf("expected CloudAPIKey to resolve dynamically, got: %s", resolvedKey)
	}
}

func TestConfig_ProviderFallback(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tzro-config-fallback-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldConfigPath := configPath
	configPath = filepath.Join(tempDir, "config.json")
	defer func() { configPath = oldConfigPath }()

	// Configure default provider environment variable
	os.Setenv("GEMINI_API_KEY", "fallback-gemini-key")
	defer os.Unsetenv("GEMINI_API_KEY")

	// Set empty cloud key
	cfg := &EngineConfig{
		ModelMode:     "cooperative",
		CloudProvider: "google",
		CloudAPIKey:   "",
	}

	if err := Save(cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// Trigger load
	if err := Load(); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	resolvedKey := GetCloudAPIKey()
	if resolvedKey != "fallback-gemini-key" {
		t.Errorf("expected CloudAPIKey to fallback to GEMINI_API_KEY, got: %s", resolvedKey)
	}
}

func TestConfig_IsObserverEnabled(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name     string
		cfg      EngineConfig
		expected bool
	}{
		{
			name: "Explicit true",
			cfg: EngineConfig{
				ObserverEnabled: &trueVal,
			},
			expected: true,
		},
		{
			name: "Explicit false",
			cfg: EngineConfig{
				ObserverEnabled: &falseVal,
			},
			expected: false,
		},
		{
			name: "Default (nil) for llama-server",
			cfg: EngineConfig{
				InferenceBackend: BackendConfig{
					Type: "llama-server",
				},
				ObserverEnabled: nil,
			},
			expected: true,
		},
		{
			name: "Default (nil) for empty type",
			cfg: EngineConfig{
				InferenceBackend: BackendConfig{
					Type: "",
				},
				ObserverEnabled: nil,
			},
			expected: true,
		},
		{
			name: "Default (nil) for openai-compatible",
			cfg: EngineConfig{
				InferenceBackend: BackendConfig{
					Type: "openai-compatible",
				},
				ObserverEnabled: nil,
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.IsObserverEnabled()
			if got != tc.expected {
				t.Errorf("Expected %v, got %v", tc.expected, got)
			}
		})
	}
}
