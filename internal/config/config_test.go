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

func TestConfig_ProbeStepMaxTokensDefault(t *testing.T) {
	// Zero value should return the default of 2048
	configMutex.Lock()
	saved := GlobalConfig.ProbeStepMaxTokens
	GlobalConfig.ProbeStepMaxTokens = 0
	configMutex.Unlock()
	defer func() {
		configMutex.Lock()
		GlobalConfig.ProbeStepMaxTokens = saved
		configMutex.Unlock()
	}()

	got := GetProbeStepMaxTokens()
	if got != 2048 {
		t.Errorf("expected default 2048, got %d", got)
	}
}

func TestConfig_ProbeStepMaxTokensExplicit(t *testing.T) {
	configMutex.Lock()
	saved := GlobalConfig.ProbeStepMaxTokens
	GlobalConfig.ProbeStepMaxTokens = 4096
	configMutex.Unlock()
	defer func() {
		configMutex.Lock()
		GlobalConfig.ProbeStepMaxTokens = saved
		configMutex.Unlock()
	}()

	got := GetProbeStepMaxTokens()
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}
}

func TestConfig_AccumulatedContextMaxCharsDefault(t *testing.T) {
	configMutex.Lock()
	saved := GlobalConfig.AccumulatedContextMaxChars
	GlobalConfig.AccumulatedContextMaxChars = 0
	configMutex.Unlock()
	defer func() {
		configMutex.Lock()
		GlobalConfig.AccumulatedContextMaxChars = saved
		configMutex.Unlock()
	}()

	got := GetAccumulatedContextMaxChars()
	if got != 16000 {
		t.Errorf("expected default 16000, got %d", got)
	}
}

func TestConfig_AccumulatedContextMaxCharsExplicit(t *testing.T) {
	configMutex.Lock()
	saved := GlobalConfig.AccumulatedContextMaxChars
	GlobalConfig.AccumulatedContextMaxChars = 24000
	configMutex.Unlock()
	defer func() {
		configMutex.Lock()
		GlobalConfig.AccumulatedContextMaxChars = saved
		configMutex.Unlock()
	}()

	got := GetAccumulatedContextMaxChars()
	if got != 24000 {
		t.Errorf("expected 24000, got %d", got)
	}
}

func TestConfig_RecallCompactionBudgetCharsDefault(t *testing.T) {
	configMutex.Lock()
	saved := GlobalConfig.RecallCompactionBudgetChars
	GlobalConfig.RecallCompactionBudgetChars = 0
	configMutex.Unlock()
	defer func() {
		configMutex.Lock()
		GlobalConfig.RecallCompactionBudgetChars = saved
		configMutex.Unlock()
	}()

	got := GetRecallCompactionBudgetChars()
	if got != 32000 {
		t.Errorf("expected default 32000, got %d", got)
	}
}

func TestConfig_RecallCompactionBudgetCharsExplicit(t *testing.T) {
	configMutex.Lock()
	saved := GlobalConfig.RecallCompactionBudgetChars
	GlobalConfig.RecallCompactionBudgetChars = 48000
	configMutex.Unlock()
	defer func() {
		configMutex.Lock()
		GlobalConfig.RecallCompactionBudgetChars = saved
		configMutex.Unlock()
	}()

	got := GetRecallCompactionBudgetChars()
	if got != 48000 {
		t.Errorf("expected 48000, got %d", got)
	}
}

func TestConfig_RouterModelPath_PersistsToConfig(t *testing.T) {
	// Create an isolated config file
	tempDir, err := os.MkdirTemp("", "tzro-config-router-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldConfigPath := configPath
	configPath = filepath.Join(tempDir, "config.json")
	defer func() { configPath = oldConfigPath }()

	// Save with routerModelPath set
	cfg := &EngineConfig{
		ModelMode:       "cooperative",
		GGUFModelPath:   "worker-model.gguf",
		RouterModelPath: "router-model.gguf",
	}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Reload and verify
	if err := Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	got := GlobalConfig.RouterModelPath
	if got != "router-model.gguf" {
		t.Errorf("expected RouterModelPath 'router-model.gguf', got %q", got)
	}
}
