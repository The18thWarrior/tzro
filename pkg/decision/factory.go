package decision

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config specifies the runtime settings for the decision subsystem.
type Config struct {
	Provider  string        `json:"provider"`   // "local" or "remote"
	BinPath   string        `json:"bin_path"`   // Path to jev-score binary
	ModelPath string        `json:"model_path"` // Path to GGUF model
	RemoteURL string        `json:"remote_url"` // URL for remote provider
	APIKey    string        `json:"api_key"`    // Optional Bearer token
	Timeout   time.Duration `json:"timeout"`
}

// DefaultConfig builds configuration initialized from environment variables or defaults.
func DefaultConfig() *Config {
	provider := os.Getenv("TZRO_DECISION_PROVIDER")
	if provider == "" {
		provider = "local"
	}

	binPath := os.Getenv("TZRO_DECISION_BIN")
	if binPath == "" {
		homeDir, _ := os.UserHomeDir()
		binPath = filepath.Join(homeDir, ".tzro", "bin", "jev-score")
	}

	modelPath := os.Getenv("TZRO_DECISION_MODEL")
	if modelPath == "" {
		homeDir, _ := os.UserHomeDir()
		modelPath = filepath.Join(homeDir, ".tzro", "models", "decision", "Jev-Style-0.8B-Decision-v3-Q4_K_M.gguf")
	}

	remoteURL := os.Getenv("TZRO_DECISION_URL")
	apiKey := os.Getenv("TZRO_DECISION_API_KEY")

	return &Config{
		Provider:  provider,
		BinPath:   binPath,
		ModelPath: modelPath,
		RemoteURL: remoteURL,
		APIKey:    apiKey,
		Timeout:   10 * time.Second,
	}
}

// NewProviderFromConfig instantiates either a LocalDaemonProvider or RemoteHTTPProvider.
func NewProviderFromConfig(cfg *Config) (DecisionProvider, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	switch strings.ToLower(cfg.Provider) {
	case "remote":
		if cfg.RemoteURL == "" {
			return nil, errors.New("remote provider requires remote_url or TZRO_DECISION_URL")
		}
		return NewRemoteHTTPProvider(cfg.RemoteURL, cfg.APIKey, cfg.Timeout), nil

	case "local", "":
		args := []string{}
		if cfg.ModelPath != "" {
			args = append(args, "--model", cfg.ModelPath)
		}
		return NewLocalDaemonProvider(cfg.BinPath, args...), nil

	default:
		return nil, errors.New("unsupported decision provider: " + cfg.Provider)
	}
}
