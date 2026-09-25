package context

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
	"tzro/pkg/tokenizer"
)

// ConfigFileName is the canonical configuration file path relative to workspace root.
const ConfigFileName = ".tzro/context.yaml"

// RepoConfig defines the repository-level context configuration (Schema Version 1).
type RepoConfig struct {
	SchemaVersion    int                 `yaml:"schema_version" json:"schema_version"`
	DefaultBudget    int                 `yaml:"default_budget" json:"default_budget"`
	TraversalDepth   int                 `yaml:"traversal_depth" json:"traversal_depth"`
	Tokenizer        string              `yaml:"tokenizer" json:"tokenizer"`
	LanguagePriority []string            `yaml:"language_priority" json:"language_priority"`
	ExcludePatterns  []string            `yaml:"exclude_patterns" json:"exclude_patterns"`
	TestConventions  map[string][]string `yaml:"test_conventions" json:"test_conventions"`
	CommandAllowlist []string            `yaml:"command_allowlist,omitempty" json:"command_allowlist,omitempty"`

	// Runtime metadata
	Provenance string    `yaml:"-" json:"provenance,omitempty"` // default | file
	LoadedAt   time.Time `yaml:"-" json:"loaded_at,omitempty"`
	ConfigPath string    `yaml:"-" json:"config_path,omitempty"`
}

// DefaultConfig returns the built-in default configuration.
func DefaultConfig() *RepoConfig {
	return &RepoConfig{
		SchemaVersion:    1,
		DefaultBudget:    4000,
		TraversalDepth:   2,
		Tokenizer:        tokenizer.EncodingDefault,
		LanguagePriority: []string{"go", "typescript", "python", "rust"},
		ExcludePatterns:  []string{},
		TestConventions:  make(map[string][]string),
		CommandAllowlist: []string{},
		Provenance:       "default",
		LoadedAt:         time.Now().UTC(),
	}
}

// ConfigTemplateContent is the documented template created by tzro init --config.
const ConfigTemplateContent = `# .tzro/context.yaml — Repository Context Configuration
# Schema Version 1
schema_version: 1

# Maximum tokens for serialized context pack
default_budget: 4000

# Maximum reference-graph hops from anchor (0-8, 0 includes only the anchor)
traversal_depth: 2

# Explicit tokenizer encoding (cl100k_base | o200k_base)
tokenizer: cl100k_base

# Preferred language ranking
language_priority:
  - go
  - typescript
  - python
  - rust

# Additional workspace-relative exclusion patterns (gitignore syntax)
exclude_patterns: []

# Additional per-language test file conventions (adds to built-in patterns)
test_conventions:
  go: []
  typescript: []
  python: []
  rust: []
`

// ConfigLoader provides thread-safe, freshness-aware configuration loading.
type ConfigLoader struct {
	mu          sync.RWMutex
	cachedPath  string
	cachedMtime time.Time
	cachedCfg   *RepoConfig
}

var (
	defaultLoader = &ConfigLoader{}
)

// ResolveWorkspaceRoot finds the canonical workspace root given a directory or subdirectory.
func ResolveWorkspaceRoot(startDir string) (string, error) {
	if startDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		startDir = cwd
	}
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}

	// Try git rev-parse --show-toplevel first
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = abs
	out, err := cmd.Output()
	if err == nil {
		top := strings.TrimSpace(string(out))
		if top != "" {
			return filepath.Clean(top), nil
		}
	}

	// Fallback: walk up directory tree looking for .tzro or .git
	curr := abs
	for {
		if _, err := os.Stat(filepath.Join(curr, ".tzro")); err == nil {
			return curr, nil
		}
		if _, err := os.Stat(filepath.Join(curr, ".git")); err == nil {
			return curr, nil
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}

	return abs, nil
}

// LoadConfig loads the repository context configuration, falling back to defaults if absent.
func LoadConfig(workspaceRoot string) (*RepoConfig, error) {
	return defaultLoader.Load(workspaceRoot)
}

// Load loads and validates the configuration from workspaceRoot/.tzro/context.yaml.
func (l *ConfigLoader) Load(workspaceRoot string) (*RepoConfig, error) {
	canonicalRoot, err := ResolveWorkspaceRoot(workspaceRoot)
	if err != nil {
		canonicalRoot = workspaceRoot
	}

	configPath := filepath.Join(canonicalRoot, ConfigFileName)

	fi, statErr := os.Stat(configPath)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			// Zero-config default
			cfg := DefaultConfig()
			cfg.ConfigPath = configPath
			return cfg, nil
		}
		return nil, fmt.Errorf("failed to access %s: %w", configPath, statErr)
	}

	// Check cache freshness
	l.mu.RLock()
	if l.cachedCfg != nil && l.cachedPath == configPath && l.cachedMtime.Equal(fi.ModTime()) {
		cfgCopy := *l.cachedCfg
		l.mu.RUnlock()
		return &cfgCopy, nil
	}
	l.mu.RUnlock()

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", configPath, err)
	}

	cfg := DefaultConfig()
	cfg.Provenance = "file"
	cfg.ConfigPath = configPath
	cfg.LoadedAt = time.Now().UTC()

	// Strict YAML decoder to reject unknown fields and duplicates
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration in %s: %w", configPath, err)
	}

	// Validate configuration fields
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("configuration error in %s: %w", configPath, err)
	}

	// Update cache
	l.mu.Lock()
	l.cachedPath = configPath
	l.cachedMtime = fi.ModTime()
	l.cachedCfg = cfg
	l.mu.Unlock()

	cfgCopy := *cfg
	return &cfgCopy, nil
}

// Validate checks all schema constraints and ranges.
func (c *RepoConfig) Validate() error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schema_version %d: only schema_version 1 is supported", c.SchemaVersion)
	}

	if c.DefaultBudget <= 0 {
		return fmt.Errorf("invalid default_budget %d: must be a positive integer greater than zero", c.DefaultBudget)
	}

	if c.TraversalDepth < 0 || c.TraversalDepth > 8 {
		return fmt.Errorf("invalid traversal_depth %d: must be between 0 and 8", c.TraversalDepth)
	}

	if c.Tokenizer == "" {
		c.Tokenizer = tokenizer.EncodingDefault
	}
	normTok := strings.ToLower(strings.TrimSpace(c.Tokenizer))
	if normTok != tokenizer.EncodingCl100kBase && normTok != tokenizer.EncodingO200kBase {
		return fmt.Errorf("unsupported tokenizer %q: supported encodings are cl100k_base, o200k_base", c.Tokenizer)
	}
	c.Tokenizer = normTok

	if len(c.LanguagePriority) == 0 {
		c.LanguagePriority = []string{"go", "typescript", "python", "rust"}
	}

	if c.TestConventions == nil {
		c.TestConventions = make(map[string][]string)
	}

	return nil
}

// CreateConfigTemplate creates the initial .tzro/context.yaml template file.
func CreateConfigTemplate(workspaceRoot string, force bool) (string, error) {
	canonicalRoot, err := ResolveWorkspaceRoot(workspaceRoot)
	if err != nil {
		canonicalRoot = workspaceRoot
	}

	tzroDir := filepath.Join(canonicalRoot, ".tzro")
	if err := os.MkdirAll(tzroDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", tzroDir, err)
	}

	targetPath := filepath.Join(canonicalRoot, ConfigFileName)
	if _, err := os.Stat(targetPath); err == nil && !force {
		return targetPath, errors.New("configuration file already exists (use --force to overwrite)")
	}

	// Atomic write
	tmpFile, err := os.CreateTemp(tzroDir, ".context-*.tmp")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	tmpName := tmpFile.Name()

	if _, err := tmpFile.WriteString(ConfigTemplateContent); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}

	if err := os.Rename(tmpName, targetPath); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}

	return targetPath, nil
}
