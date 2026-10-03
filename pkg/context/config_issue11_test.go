package context_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tzroctx "tzro/pkg/context"
	"tzro/pkg/tokenizer"
)

func TestConfig_ZeroConfigDefaults(t *testing.T) {
	tempDir := t.TempDir()

	cfg, err := tzroctx.LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("unexpected error loading missing config: %v", err)
	}

	if cfg.Provenance != "default" {
		t.Errorf("expected provenance 'default', got %q", cfg.Provenance)
	}
	if cfg.SchemaVersion != 1 {
		t.Errorf("expected schema_version 1, got %d", cfg.SchemaVersion)
	}
	if cfg.DefaultBudget != 4000 {
		t.Errorf("expected default_budget 4000, got %d", cfg.DefaultBudget)
	}
	if cfg.TraversalDepth != 2 {
		t.Errorf("expected traversal_depth 2, got %d", cfg.TraversalDepth)
	}
	if cfg.Tokenizer != tokenizer.EncodingCl100kBase {
		t.Errorf("expected tokenizer %s, got %s", tokenizer.EncodingCl100kBase, cfg.Tokenizer)
	}
}

func TestConfig_SubdirectoryResolution(t *testing.T) {
	tempDir := t.TempDir()
	tzroDir := filepath.Join(tempDir, ".tzro")
	_ = os.MkdirAll(tzroDir, 0755)

	configContent := `schema_version: 1
default_budget: 7500
traversal_depth: 3
tokenizer: o200k_base
language_priority:
  - rust
  - go
`
	_ = os.WriteFile(filepath.Join(tzroDir, "context.yaml"), []byte(configContent), 0644)

	// Create deeply nested subdirectory
	subDir := filepath.Join(tempDir, "pkg", "core", "engine", "nested")
	_ = os.MkdirAll(subDir, 0755)

	// Resolve and load from nested subdirectory
	resolvedRoot, err := tzroctx.ResolveWorkspaceRoot(subDir)
	if err != nil {
		t.Fatalf("ResolveWorkspaceRoot failed: %v", err)
	}
	if resolvedRoot != tempDir {
		t.Errorf("expected resolved root %s, got %s", tempDir, resolvedRoot)
	}

	cfg, err := tzroctx.LoadConfig(subDir)
	if err != nil {
		t.Fatalf("LoadConfig from subDir failed: %v", err)
	}
	if cfg.Provenance != "file" {
		t.Errorf("expected provenance 'file', got %q", cfg.Provenance)
	}
	if cfg.DefaultBudget != 7500 {
		t.Errorf("expected default_budget 7500, got %d", cfg.DefaultBudget)
	}
	if cfg.TraversalDepth != 3 {
		t.Errorf("expected traversal_depth 3, got %d", cfg.TraversalDepth)
	}
	if cfg.Tokenizer != tokenizer.EncodingO200kBase {
		t.Errorf("expected tokenizer %s, got %s", tokenizer.EncodingO200kBase, cfg.Tokenizer)
	}
}

func TestConfig_StrictValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		yamlContent string
		errContains string
	}{
		{
			name: "unknown field rejected",
			yamlContent: `schema_version: 1
unknown_key: "forbidden"
default_budget: 4000
`,
			errContains: "unknown_key",
		},
		{
			name: "unsupported schema version",
			yamlContent: `schema_version: 2
default_budget: 4000
`,
			errContains: "unsupported schema_version 2",
		},
		{
			name: "negative budget",
			yamlContent: `schema_version: 1
default_budget: -500
`,
			errContains: "positive integer",
		},
		{
			name: "zero budget",
			yamlContent: `schema_version: 1
default_budget: 0
`,
			errContains: "positive integer",
		},
		{
			name: "out of range traversal depth high",
			yamlContent: `schema_version: 1
traversal_depth: 9
`,
			errContains: "between 0 and 8",
		},
		{
			name: "out of range traversal depth negative",
			yamlContent: `schema_version: 1
traversal_depth: -1
`,
			errContains: "between 0 and 8",
		},
		{
			name: "unsupported tokenizer encoding",
			yamlContent: `schema_version: 1
tokenizer: llama3_base
`,
			errContains: "unsupported tokenizer",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			tzroDir := filepath.Join(tempDir, ".tzro")
			_ = os.MkdirAll(tzroDir, 0755)
			_ = os.WriteFile(filepath.Join(tzroDir, "context.yaml"), []byte(tc.yamlContent), 0644)

			loader := &tzroctx.ConfigLoader{}
			_, err := loader.Load(tempDir)
			if err == nil {
				t.Fatalf("expected validation error for %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("error %q does not contain expected substring %q", err.Error(), tc.errContains)
			}
		})
	}
}

func TestConfig_ExclusionsAndCustomTestConventions(t *testing.T) {
	tempDir := t.TempDir()
	tzroDir := filepath.Join(tempDir, ".tzro")
	_ = os.MkdirAll(tzroDir, 0755)

	configContent := `schema_version: 1
default_budget: 5000
exclude_patterns:
  - "legacy/**"
  - "*.tmp"
test_conventions:
  go:
    - "_integration_test.go"
    - "suite_test.go"
  typescript:
    - ".e2e.ts"
`
	_ = os.WriteFile(filepath.Join(tzroDir, "context.yaml"), []byte(configContent), 0644)

	loader := &tzroctx.ConfigLoader{}
	cfg, err := loader.Load(tempDir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(cfg.ExcludePatterns) != 2 || cfg.ExcludePatterns[0] != "legacy/**" {
		t.Errorf("exclude patterns mismatch: %v", cfg.ExcludePatterns)
	}
	if len(cfg.TestConventions["go"]) != 2 {
		t.Errorf("go test conventions mismatch: %v", cfg.TestConventions["go"])
	}
	if len(cfg.TestConventions["typescript"]) != 1 || cfg.TestConventions["typescript"][0] != ".e2e.ts" {
		t.Errorf("ts test conventions mismatch: %v", cfg.TestConventions["typescript"])
	}
}

func TestConfig_FreshnessAndReloading(t *testing.T) {
	tempDir := t.TempDir()
	tzroDir := filepath.Join(tempDir, ".tzro")
	_ = os.MkdirAll(tzroDir, 0755)
	configFile := filepath.Join(tzroDir, "context.yaml")

	// 1. Initial valid config
	_ = os.WriteFile(configFile, []byte("schema_version: 1\ndefault_budget: 3000\n"), 0644)

	loader := &tzroctx.ConfigLoader{}
	cfg1, err := loader.Load(tempDir)
	if err != nil {
		t.Fatalf("initial load failed: %v", err)
	}
	if cfg1.DefaultBudget != 3000 {
		t.Errorf("expected 3000, got %d", cfg1.DefaultBudget)
	}

	// Sleep slightly to guarantee modtime difference on filesystem
	time.Sleep(10 * time.Millisecond)

	// 2. Edit config to new valid budget
	_ = os.WriteFile(configFile, []byte("schema_version: 1\ndefault_budget: 5500\n"), 0644)
	cfg2, err := loader.Load(tempDir)
	if err != nil {
		t.Fatalf("re-load after edit failed: %v", err)
	}
	if cfg2.DefaultBudget != 5500 {
		t.Errorf("expected reloaded budget 5500, got %d", cfg2.DefaultBudget)
	}

	time.Sleep(10 * time.Millisecond)

	// 3. Edit config to invalid YAML -> must produce error, not retain stale cached config
	_ = os.WriteFile(configFile, []byte("schema_version: 1\ndefault_budget: invalid_integer\n"), 0644)
	_, err = loader.Load(tempDir)
	if err == nil {
		t.Fatal("expected error on invalid configuration edit, got nil")
	}
}

func TestConfig_TemplateCreationIdempotency(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create template
	targetPath, err := tzroctx.CreateConfigTemplate(tempDir, false)
	if err != nil {
		t.Fatalf("CreateConfigTemplate failed: %v", err)
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read created template: %v", err)
	}
	if !strings.Contains(string(data), "schema_version: 1") || !strings.Contains(string(data), "traversal_depth: 2") {
		t.Errorf("template missing expected fields:\n%s", string(data))
	}

	// 2. Creating again without force must error
	_, err = tzroctx.CreateConfigTemplate(tempDir, false)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected error on existing template without force, got: %v", err)
	}

	// 3. Creating with force must overwrite and succeed
	_, err = tzroctx.CreateConfigTemplate(tempDir, true)
	if err != nil {
		t.Fatalf("expected success with force, got: %v", err)
	}

	// 4. Verify no hooks or extra files were created
	gitDir := filepath.Join(tempDir, ".git", "hooks")
	if _, err := os.Stat(gitDir); err == nil {
		t.Error("unexpected hooks directory created during config template creation")
	}
}
