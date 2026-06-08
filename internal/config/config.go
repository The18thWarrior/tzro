package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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

	// Delegation Mode controls how aggressively the cloud model delegates
	// sub-tasks to the local model via tzro_completion/tzro_classification.
	//   "conservative" — only use tzro for DAG workflows (current behavior)
	//   "balanced"     — cloud delegates classification, extraction, formatting
	//   "aggressive"   — cloud delegates everything except frontier reasoning
	DelegationMode string `json:"delegationMode,omitempty"`

	// Observer Agent
	ObserverEnabled *bool `json:"observerEnabled,omitempty"` // nil = auto (on for llama-server, off otherwise)

	// Sentinel Agent (ADR-0023)
	SentinelEnabled  *bool `json:"sentinelEnabled,omitempty"`  // nil = auto (same logic as Observer)
	SentinelInterval int   `json:"sentinelInterval,omitempty"` // heartbeat interval in seconds (default 300 = 5 min)

	// Confidence Tier (ADR-0020): consecutive insufficient results before sticky cloud fallback
	ConfidenceThreshold int `json:"confidenceThreshold,omitempty"`

	// Visual dashboard pacing delays in milliseconds
	ExecutorNodeDelayMs  int `json:"executorNodeDelayMs,omitempty"`
	ExecutorLevelDelayMs int `json:"executorLevelDelayMs,omitempty"`
}

type BackendConfig struct {
	Type   string `json:"type"`   // "llama-server" | "openai-compatible"
	URL    string `json:"url"`    // Remote endpoint URL
	Model  string `json:"model"`  // Model name/ID
	APIKey string `json:"apiKey"` // Optional, supports $VAR
}

func detectTzroDir() string {
	if envDir := os.Getenv("TZRO_DIR"); envDir != "" {
		return envDir
	}
	if execPath, err := os.Executable(); err == nil {
		dir := filepath.Dir(execPath)
		for i := 0; i < 4; i++ {
			if _, err := os.Stat(filepath.Join(dir, ".tzro")); err == nil {
				os.Setenv("TZRO_DIR", dir)
				return dir
			}
			if _, err := os.Stat(filepath.Join(dir, "tzro.db")); err == nil {
				os.Setenv("TZRO_DIR", dir)
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return ""
}

// ResolvePath resolves a relative path against TZRO_DIR (if set).
// If TZRO_DIR is not set, returns the path unchanged. Use this instead of
// inlining os.Getenv("TZRO_DIR") checks at every callsite.
func ResolvePath(relPath string) string {
	if envDir := os.Getenv("TZRO_DIR"); envDir != "" {
		return filepath.Join(envDir, relPath)
	}
	return relPath
}

// FindBinary locates a managed binary by name using the standard tzro
// resolution order: TZRO_DIR/bin, sibling of current executable, ./bin.
// Returns empty string if not found.
func FindBinary(name string) string {
	if envDir := os.Getenv("TZRO_DIR"); envDir != "" {
		path := filepath.Join(envDir, "bin", name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if execPath, err := os.Executable(); err == nil {
		path := filepath.Join(filepath.Dir(execPath), name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	path := filepath.Join(".", "bin", name)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

var (
	detectedDir  = detectTzroDir()
	GlobalConfig = &EngineConfig{
		ModelMode:           "cooperative",
		CloudProvider:       "google",
		CloudAPIKey:         "",
		CloudModel:          "gemini-flash-latest",
		SpeedFloor:          5.0,
		SidecarEnabled:      true,
		GGUFModelPath:       "models/gemma-4-12b-it-qat-q4_0.gguf",
		ModelsDir:           defaultModelsDir(),
		ConfidenceThreshold: 3,
		ExecutorNodeDelayMs:  800,
		ExecutorLevelDelayMs: 500,
	}
	configMutex sync.RWMutex
	configPath  = filepath.Join(".tzro", "config.json")
)

func getConfigPath() string {
	if configPath != filepath.Join(".tzro", "config.json") {
		return configPath
	}
	return ResolvePath(filepath.Join(".tzro", "config.json"))
}

// Load reads config settings from disk or sets defaults
func Load() error {
	configMutex.Lock()
	defer configMutex.Unlock()

	cPath := getConfigPath()
	// Ensure .tzro dir exists
	_ = os.MkdirAll(filepath.Dir(cPath), 0755)

	file, err := os.Open(cPath)
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
	GlobalConfig.SentinelEnabled = cfg.SentinelEnabled
	GlobalConfig.SentinelInterval = cfg.SentinelInterval
	GlobalConfig.DelegationMode = cfg.DelegationMode
	GlobalConfig.ConfidenceThreshold = cfg.ConfidenceThreshold
	GlobalConfig.ExecutorNodeDelayMs = cfg.ExecutorNodeDelayMs
	GlobalConfig.ExecutorLevelDelayMs = cfg.ExecutorLevelDelayMs
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
	GlobalConfig.SentinelEnabled = cfg.SentinelEnabled
	GlobalConfig.SentinelInterval = cfg.SentinelInterval
	GlobalConfig.DelegationMode = cfg.DelegationMode
	GlobalConfig.ConfidenceThreshold = cfg.ConfidenceThreshold
	GlobalConfig.ExecutorNodeDelayMs = cfg.ExecutorNodeDelayMs
	GlobalConfig.ExecutorLevelDelayMs = cfg.ExecutorLevelDelayMs
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

// IsSentinelEnabled returns true if the sentinel agent is enabled.
// If not explicitly set, follows the same auto-detection logic as the Observer.
func (c EngineConfig) IsSentinelEnabled() bool {
	if c.SentinelEnabled != nil {
		return *c.SentinelEnabled
	}
	return c.InferenceBackend.Type == "" || c.InferenceBackend.Type == "llama-server"
}

// GetSentinelInterval returns the configured sentinel heartbeat interval.
// Defaults to 5 minutes (300 seconds) if not explicitly configured.
func GetSentinelInterval() time.Duration {
	configMutex.RLock()
	interval := GlobalConfig.SentinelInterval
	configMutex.RUnlock()

	if interval <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(interval) * time.Second
}

func saveLocked(cfg *EngineConfig) error {
	bytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(getConfigPath(), bytes, 0644)
}

func Get() EngineConfig {
	configMutex.RLock()
	defer configMutex.RUnlock()
	return *GlobalConfig
}

// defaultModelsDir returns the default models directory path (~/.tzro/models/).
func defaultModelsDir() string {
	resolved := ResolvePath(filepath.Join(".tzro", "models"))
	if resolved != filepath.Join(".tzro", "models") {
		return resolved
	}
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

// GetDelegationMode returns the configured delegation mode.
// Defaults to "balanced" if not explicitly configured.
func GetDelegationMode() string {
	configMutex.RLock()
	mode := GlobalConfig.DelegationMode
	configMutex.RUnlock()

	switch mode {
	case "conservative", "balanced", "aggressive":
		return mode
	default:
		return "balanced"
	}
}

// GetConfidenceThreshold returns the configured number of consecutive insufficient
// confidence assessments before sticky cloud fallback activates for a task.
// Defaults to 3 if not explicitly configured or set to a non-positive value.
func GetConfidenceThreshold() int {
	configMutex.RLock()
	t := GlobalConfig.ConfidenceThreshold
	configMutex.RUnlock()

	if t <= 0 {
		return 3
	}
	return t
}
