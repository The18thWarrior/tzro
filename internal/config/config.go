package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type EngineConfig struct {
	ModelMode          string  `json:"modelMode"`     // "cooperative" | "local" | "cloud"
	CloudProvider      string  `json:"cloudProvider"` // "google" | "openai"
	CloudAPIKey        string  `json:"cloudApiKey"`
	CloudModel         string  `json:"cloudModel"`                   // the cloud model name to use (e.g. gemini-flash-latest)
	SpeedFloor         float64 `json:"speedFloor"`                   // default 5.0 t/s
	SidecarEnabled     bool    `json:"sidecarEnabled"`               // default true
	GGUFModelPath      string  `json:"ggufModelPath"`                // path to local gguf model file
	ModelsDir          string  `json:"modelsDir"`                    // directory for downloaded models
	MaxRAGContextChars int     `json:"maxRagContextChars,omitempty"` // max chars for Graph-RAG context injection (0 = use default 2000)

	// Inference Backend (ADR-0016)
	InferenceBackend BackendConfig `json:"inferenceBackend,omitempty"`

	// Observer Agent
	ObserverEnabled *bool `json:"observerEnabled,omitempty"` // nil = auto (on for llama-server, off otherwise)
}

type BackendConfig struct {
	Type   string `json:"type"`   // "llama-server" | "openai-compatible"
	URL    string `json:"url"`    // Remote endpoint URL
	Model  string `json:"model"`  // Model name/ID
	APIKey string `json:"apiKey"` // Optional, supports $VAR
}

var (
	GlobalConfig = &EngineConfig{
		ModelMode:      "cooperative",
		CloudProvider:  "google",
		CloudAPIKey:    "",
		CloudModel:     "gemini-flash-latest",
		SpeedFloor:     5.0,
		SidecarEnabled: true,
		GGUFModelPath:  "models/grm-2.5-q4.gguf",
		ModelsDir:      defaultModelsDir(),
	}
	configMutex sync.RWMutex
	configPath  = filepath.Join(".tzro", "config.json")
)

// Load reads config settings from disk or sets defaults
func Load() error {
	configMutex.Lock()
	defer configMutex.Unlock()

	// Ensure .tzro dir exists
	_ = os.MkdirAll(".tzro", 0755)

	file, err := os.Open(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Save default configurations
			return saveLocked(GlobalConfig)
		}
		return err
	}
	defer file.Close()

	return json.NewDecoder(file).Decode(GlobalConfig)
}

// Save writes current global configuration to disk
func Save(cfg *EngineConfig) error {
	configMutex.Lock()
	defer configMutex.Unlock()

	GlobalConfig.ModelMode = cfg.ModelMode
	GlobalConfig.CloudProvider = cfg.CloudProvider
	GlobalConfig.CloudAPIKey = cfg.CloudAPIKey
	GlobalConfig.CloudModel = cfg.CloudModel
	GlobalConfig.SpeedFloor = cfg.SpeedFloor
	GlobalConfig.SidecarEnabled = cfg.SidecarEnabled
	GlobalConfig.GGUFModelPath = cfg.GGUFModelPath
	GlobalConfig.InferenceBackend = cfg.InferenceBackend
	GlobalConfig.ObserverEnabled = cfg.ObserverEnabled
	if cfg.ModelsDir != "" {
		GlobalConfig.ModelsDir = cfg.ModelsDir
	}

	return saveLocked(GlobalConfig)
}

// Override updates the global configuration in memory without persisting to disk
func Override(cfg *EngineConfig) {
	configMutex.Lock()
	defer configMutex.Unlock()

	GlobalConfig.ModelMode = cfg.ModelMode
	GlobalConfig.CloudProvider = cfg.CloudProvider
	GlobalConfig.CloudAPIKey = cfg.CloudAPIKey
	GlobalConfig.CloudModel = cfg.CloudModel
	GlobalConfig.SpeedFloor = cfg.SpeedFloor
	GlobalConfig.SidecarEnabled = cfg.SidecarEnabled
	GlobalConfig.GGUFModelPath = cfg.GGUFModelPath
	GlobalConfig.InferenceBackend = cfg.InferenceBackend
	GlobalConfig.ObserverEnabled = cfg.ObserverEnabled
	if cfg.ModelsDir != "" {
		GlobalConfig.ModelsDir = cfg.ModelsDir
	}
}

// IsObserverEnabled returns true if the observer agent is enabled.
// If not explicitly set, defaults to true for llama-server (or empty type/embedded sidecar),
// and false for remote backends.
func (c EngineConfig) IsObserverEnabled() bool {
	if c.ObserverEnabled != nil {
		return *c.ObserverEnabled
	}
	return c.InferenceBackend.Type == "" || c.InferenceBackend.Type == "llama-server"
}

func saveLocked(cfg *EngineConfig) error {
	bytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, bytes, 0644)
}

func Get() EngineConfig {
	configMutex.RLock()
	defer configMutex.RUnlock()
	return *GlobalConfig
}

// defaultModelsDir returns the default models directory path (~/.tzro/models/).
func defaultModelsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".tzro", "models")
	}
	return filepath.Join(home, ".tzro", "models")
}

// GetModelsDir resolves the configured models directory, ensures it exists, and returns the path.
func GetModelsDir() string {
	configMutex.RLock()
	dir := GlobalConfig.ModelsDir
	configMutex.RUnlock()

	if dir == "" {
		dir = defaultModelsDir()
	}

	_ = os.MkdirAll(dir, 0755)
	return dir
}

// GetCloudAPIKey resolves the CloudAPIKey dynamically from the environment
// if it starts with "$" or is completely empty.
func GetCloudAPIKey() string {
	configMutex.RLock()
	key := GlobalConfig.CloudAPIKey
	provider := strings.ToLower(GlobalConfig.CloudProvider)
	configMutex.RUnlock()

	// 1. Resolve $VAR references
	if strings.HasPrefix(key, "$") {
		envVarName := strings.TrimPrefix(key, "$")
		return os.Getenv(envVarName)
	}

	// 2. Fallback to standard environment variables if empty
	if key == "" {
		if provider == "google" {
			if geminiKey := os.Getenv("GEMINI_API_KEY"); geminiKey != "" {
				return geminiKey
			}
			return os.Getenv("GOOGLE_API_KEY")
		} else if provider == "openai" {
			return os.Getenv("OPENAI_API_KEY")
		}
	}

	return key
}

// GetCloudModel resolves the CloudModel dynamically, defaulting to "gemini-flash-latest"
// for google or "gpt-4o-mini" for openai if not configured.
func GetCloudModel() string {
	configMutex.RLock()
	model := GlobalConfig.CloudModel
	provider := strings.ToLower(GlobalConfig.CloudProvider)
	configMutex.RUnlock()

	if model != "" {
		return model
	}

	if provider == "openai" {
		return "gpt-4o-mini"
	}
	return "gemini-flash-latest"
}

// GetMaxRAGContextChars returns the configured max character limit for Graph-RAG context injection.
// Defaults to 2000 (~500 tokens) if not explicitly configured.
func GetMaxRAGContextChars() int {
	configMutex.RLock()
	limit := GlobalConfig.MaxRAGContextChars
	configMutex.RUnlock()

	if limit <= 0 {
		return 2000
	}
	return limit
}
