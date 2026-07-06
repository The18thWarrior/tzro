package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type EngineConfig struct {
	ModelMode                   string  `json:"modelMode"`     // "cooperative" | "local" | "cloud"
	CloudProvider               string  `json:"cloudProvider"` // "google" | "openai"
	CloudAPIKey                 string  `json:"cloudApiKey"`
	CloudModel                  string  `json:"cloudModel"`                            // the cloud model name to use (e.g. gemini-flash-latest)
	SpeedFloor                  float64 `json:"speedFloor"`                            // default 5.0 t/s
	SidecarEnabled              bool    `json:"sidecarEnabled"`                        // default true
	ThermalCooldownSeconds      int     `json:"thermalCooldownSeconds,omitempty"`      // default 30
	ThermalCloudCooldownMinutes int     `json:"thermalCloudCooldownMinutes,omitempty"` // default 5
	GGUFModelPath               string  `json:"ggufModelPath"`                         // path to local gguf model file
	ModelsDir                   string  `json:"modelsDir"`                             // directory for downloaded models
	ContextSize                 int     `json:"contextSize,omitempty"`                 // llama-server context window size in tokens (default: 32768)
	GPULayers                   *int    `json:"gpuLayers,omitempty"`                   // Override GPU layer offload count (-1 = all, 0 = CPU-only, nil = platform auto)
	ThreadCount                 *int    `json:"threadCount,omitempty"`                 // Override inference thread count (nil = platform auto-detect)
	MaxRAGContextChars          int     `json:"maxRagContextChars,omitempty"`          // max chars for Graph-RAG context injection (0 = use default 2000)

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

	// Dynamic Local Planning & Routing
	PrivacyLevel          string   `json:"privacyLevel,omitempty"`          // "strict-local" | "hybrid" | "cloud-preferred" (default: "hybrid")
	RestrictedDirectories []string `json:"restrictedDirectories,omitempty"` // Paths locked to local-only planning
	ComplexityThreshold   string   `json:"complexityThreshold,omitempty"`   // "T0" | "T1" | "T2" (default: "T1")
	SensitiveKeywords     []string `json:"sensitiveKeywords,omitempty"`     // Custom keywords; empty = built-in defaults

	// Vision / Multimodal
	MMProjModelPath string `json:"mmProjModelPath,omitempty"` // Path to mmproj GGUF for vision; empty = auto-detect in models dir
	PDFOcrBackend   string `json:"pdfOcrBackend,omitempty"`   // "vision" | "tesseract" | "auto" (default: "auto")

	// Visual dashboard pacing delays in milliseconds
	ExecutorNodeDelayMs  int `json:"executorNodeDelayMs,omitempty"`
	ExecutorLevelDelayMs int `json:"executorLevelDelayMs,omitempty"`

	// Circuit Breaker (P2): Multiplier for the weighted time budget.
	// The budget is computed as sum(nodeCount[type] × weight[type]) where
	// weights are: probe=10min, action=5min, deterministic/validator=90s.
	// Default 1.0. Set to 2.0 for lenient mode, 0.5 for aggressive.
	CircuitBreakerMultiplier float64 `json:"circuitBreakerMultiplier,omitempty"`

	// Code generation (tzro_code): Maximum lines for generated files.
	// Default 500. Set to 0 to use the default.
	CodeMaxLines int `json:"codeMaxLines,omitempty"`
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
		ModelMode:            "local",
		CloudProvider:        "google",
		CloudAPIKey:          "",
		CloudModel:           "gemini-flash-latest",
		SpeedFloor:           5.0,
		SidecarEnabled:       true,
		GGUFModelPath:        "models/Qwopus3.5-4B-Coder-MTP-Q4_K_M.gguf",
		ModelsDir:            defaultModelsDir(),
		ContextSize:          32768,
		ConfidenceThreshold:  3,
		ExecutorNodeDelayMs:  800,
		ExecutorLevelDelayMs: 500,
	}
	configMutex sync.RWMutex
	configPath  = "config.json"
)

func getConfigPath() string {
	if configPath != "config.json" {
		return configPath
	}
	return ResolvePath("config.json")
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
	GlobalConfig.PrivacyLevel = cfg.PrivacyLevel
	GlobalConfig.RestrictedDirectories = cfg.RestrictedDirectories
	GlobalConfig.ComplexityThreshold = cfg.ComplexityThreshold
	GlobalConfig.SensitiveKeywords = cfg.SensitiveKeywords
	GlobalConfig.MMProjModelPath = cfg.MMProjModelPath
	GlobalConfig.PDFOcrBackend = cfg.PDFOcrBackend
	GlobalConfig.ExecutorNodeDelayMs = cfg.ExecutorNodeDelayMs
	GlobalConfig.ExecutorLevelDelayMs = cfg.ExecutorLevelDelayMs
	GlobalConfig.CircuitBreakerMultiplier = cfg.CircuitBreakerMultiplier
	GlobalConfig.GPULayers = cfg.GPULayers
	GlobalConfig.ThreadCount = cfg.ThreadCount
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
	GlobalConfig.PrivacyLevel = cfg.PrivacyLevel
	GlobalConfig.RestrictedDirectories = cfg.RestrictedDirectories
	GlobalConfig.ComplexityThreshold = cfg.ComplexityThreshold
	GlobalConfig.SensitiveKeywords = cfg.SensitiveKeywords
	GlobalConfig.MMProjModelPath = cfg.MMProjModelPath
	GlobalConfig.PDFOcrBackend = cfg.PDFOcrBackend
	GlobalConfig.ExecutorNodeDelayMs = cfg.ExecutorNodeDelayMs
	GlobalConfig.ExecutorLevelDelayMs = cfg.ExecutorLevelDelayMs
	GlobalConfig.CircuitBreakerMultiplier = cfg.CircuitBreakerMultiplier
	GlobalConfig.GPULayers = cfg.GPULayers
	GlobalConfig.ThreadCount = cfg.ThreadCount
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
	resolved := ResolvePath("models")
	if resolved != "models" {
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

// GetContextSize returns the configured llama-server context window size.
// Falls back to 65536 (64K tokens) if not set or zero.
func GetContextSize() int {
	configMutex.RLock()
	size := GlobalConfig.ContextSize
	configMutex.RUnlock()

	if size <= 0 {
		return 32768
	}
	return size
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

// GetPrivacyLevel returns the configured privacy routing level.
// Defaults to "hybrid" if not explicitly configured or set to an invalid value.
func GetPrivacyLevel() string {
	configMutex.RLock()
	level := GlobalConfig.PrivacyLevel
	configMutex.RUnlock()

	switch level {
	case "strict-local", "hybrid", "cloud-preferred":
		return level
	default:
		return "hybrid"
	}
}

// GetPlanningComplexityThreshold returns the configured complexity tier threshold
// for routing planning to cloud vs. local. Tasks at or below this tier plan locally.
// Defaults to "T1" if not explicitly configured or set to an invalid value.
func GetPlanningComplexityThreshold() string {
	configMutex.RLock()
	t := GlobalConfig.ComplexityThreshold
	configMutex.RUnlock()

	switch t {
	case "T0", "T1", "T2":
		return t
	default:
		return "T1"
	}
}

// GetSensitiveKeywords returns the configured sensitive keywords list for privacy quarantine.
// If not configured, returns a built-in default list of common sensitive terms.
func GetSensitiveKeywords() []string {
	configMutex.RLock()
	keywords := GlobalConfig.SensitiveKeywords
	configMutex.RUnlock()

	if len(keywords) > 0 {
		return keywords
	}
	return []string{"password", "secret", "private_key", "api_key", "token", "credential", "db_url", "ssh_key"}
}

// GetRestrictedDirectories returns the configured list of directory paths
// that are locked to local-only planning for privacy quarantine.
func GetRestrictedDirectories() []string {
	configMutex.RLock()
	dirs := GlobalConfig.RestrictedDirectories
	configMutex.RUnlock()

	return dirs
}

// GetMMProjModelPath resolves the multimodal projector model path.
// If explicitly configured, uses that path. Otherwise auto-detects by scanning
// the models directory for a file matching *mmproj*.gguf.
func GetMMProjModelPath() string {
	configMutex.RLock()
	explicit := GlobalConfig.MMProjModelPath
	configMutex.RUnlock()

	if explicit != "" {
		if _, err := os.Stat(explicit); err == nil {
			return explicit
		}
	}

	// Auto-detect: scan models dir for mmproj file
	modelsDir := GetModelsDir()
	entries, err := os.ReadDir(modelsDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if strings.Contains(name, "mmproj") && strings.HasSuffix(name, ".gguf") {
			return filepath.Join(modelsDir, e.Name())
		}
	}
	return ""
}

// GetPDFOcrBackend returns the configured PDF OCR backend preference.
// Defaults to "auto" which prefers the local vision model (if mmproj is loaded),
// falling back to system tesseract.
func GetPDFOcrBackend() string {
	configMutex.RLock()
	backend := GlobalConfig.PDFOcrBackend
	configMutex.RUnlock()

	switch backend {
	case "vision", "tesseract":
		return backend
	default:
		return "auto"
	}
}

// GetThermalCooldownSeconds returns the configured cooldown pause duration
// between thermal re-samples when pressure is "serious".
// Defaults to 30 seconds if not explicitly configured.
func GetThermalCooldownSeconds() int {
	configMutex.RLock()
	v := GlobalConfig.ThermalCooldownSeconds
	configMutex.RUnlock()

	if v <= 0 {
		return 30
	}
	return v
}

// GetThermalCloudCooldownMinutes returns the configured duration a task stays
// on cloud after thermal escalation before retrying local inference.
// Defaults to 5 minutes if not explicitly configured.
func GetThermalCloudCooldownMinutes() int {
	configMutex.RLock()
	v := GlobalConfig.ThermalCloudCooldownMinutes
	configMutex.RUnlock()

	if v <= 0 {
		return 5
	}
	return v
}

// GetDaemonURL returns the active daemon HTTP URL by checking:
// 1. A cached/running daemon port in `.tzro/.daemon.port`.
// 2. The $PORT environment variable.
// 3. Fallback to default "8080".
func GetDaemonURL() string {
	portFile := ResolvePath(".daemon.port")
	if data, err := os.ReadFile(portFile); err == nil {
		portStr := strings.TrimSpace(string(data))
		if portStr != "" {
			return "http://127.0.0.1:" + portStr
		}
	}
	if envPort := os.Getenv("PORT"); envPort != "" {
		return "http://127.0.0.1:" + envPort
	}
	return "http://127.0.0.1:8080"
}

// WriteDaemonPort writes the daemon port to .tzro/.daemon.port.
func WriteDaemonPort(port int) error {
	portFile := ResolvePath(".daemon.port")
	_ = os.MkdirAll(filepath.Dir(portFile), 0755)
	return os.WriteFile(portFile, []byte(strconv.Itoa(port)), 0644)
}

// RemoveDaemonPort removes the daemon port file.
func RemoveDaemonPort() {
	portFile := ResolvePath(".daemon.port")
	_ = os.Remove(portFile)
}

// DashboardLock represents a running dashboard process tracked via a lock file.
type DashboardLock struct {
	PID  int `json:"pid"`
	Port int `json:"port"`
}

// WriteDashboardLock writes the dashboard lock file with the given PID and port.
func WriteDashboardLock(pid, port int) error {
	lockFile := ResolvePath(".dashboard.lock")
	_ = os.MkdirAll(filepath.Dir(lockFile), 0755)
	data, err := json.Marshal(DashboardLock{PID: pid, Port: port})
	if err != nil {
		return fmt.Errorf("marshal dashboard lock: %w", err)
	}
	return os.WriteFile(lockFile, data, 0644)
}

// ReadDashboardLock reads the dashboard lock file and validates PID liveness.
// Returns nil if the lock file is missing, malformed, or the process is no longer alive.
func ReadDashboardLock() *DashboardLock {
	lockFile := ResolvePath(".dashboard.lock")
	data, err := os.ReadFile(lockFile)
	if err != nil {
		return nil
	}

	var lock DashboardLock
	if err := json.Unmarshal(data, &lock); err != nil {
		// Malformed lock file — remove it
		_ = os.Remove(lockFile)
		return nil
	}

	if lock.PID <= 0 {
		_ = os.Remove(lockFile)
		return nil
	}

	// Probe whether the PID is still alive (signal 0 tests existence without
	// actually sending a signal).
	if err := syscall.Kill(lock.PID, 0); err != nil {
		// Process is gone — stale lock
		_ = os.Remove(lockFile)
		return nil
	}

	return &lock
}

// RemoveDashboardLock removes the dashboard lock file.
func RemoveDashboardLock() {
	lockFile := ResolvePath(".dashboard.lock")
	_ = os.Remove(lockFile)
}
