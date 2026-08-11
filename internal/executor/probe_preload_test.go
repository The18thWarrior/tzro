package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/internal/compiler"
)

func TestPreloadDirectoryContext(t *testing.T) {
	t.Run("PreloadsGoFiles_ExtractsSymbols", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create a Go file with exported symbols
		goContent := `package cache

import "context"

// Cache holds cached data.
type Cache struct {
	store map[string]string
}

// NewCache creates a new Cache instance.
func NewCache() *Cache {
	return &Cache{store: make(map[string]string)}
}

// Get retrieves a value by key.
func (c *Cache) Get(ctx context.Context, key string) (string, bool) {
	v, ok := c.store[key]
	return v, ok
}

// Set stores a key-value pair.
func (c *Cache) Set(key, value string) {
	c.store[key] = value
}
`
		if err := os.WriteFile(filepath.Join(tmpDir, "cache.go"), []byte(goContent), 0644); err != nil {
			t.Fatal(err)
		}

		// Create a second Go file
		metricsContent := `package cache

// HitRate returns the cache hit rate.
func HitRate() float64 {
	return 0.95
}
`
		if err := os.WriteFile(filepath.Join(tmpDir, "metrics.go"), []byte(metricsContent), 0644); err != nil {
			t.Fatal(err)
		}

		// Create a test file that should be skipped
		testContent := `package cache

func TestCache(t *testing.T) {}
`
		if err := os.WriteFile(filepath.Join(tmpDir, "cache_test.go"), []byte(testContent), 0644); err != nil {
			t.Fatal(err)
		}

		result := preloadDirectoryContext([]string{tmpDir}, 50000)

		// Should include both source files
		if !strings.Contains(result, "cache.go") {
			t.Error("Expected preloaded context to include cache.go")
		}
		if !strings.Contains(result, "metrics.go") {
			t.Error("Expected preloaded context to include metrics.go")
		}
		// Should NOT include test files
		if strings.Contains(result, "cache_test.go") {
			t.Error("Expected preloaded context to exclude test files")
		}
		// Should include exported symbols
		if !strings.Contains(result, "NewCache") {
			t.Error("Expected preloaded context to include NewCache")
		}
		if !strings.Contains(result, "HitRate") {
			t.Error("Expected preloaded context to include HitRate")
		}
		if !strings.Contains(result, "Cache") {
			t.Error("Expected preloaded context to include Cache type")
		}
	})

	t.Run("PreloadsMarkdownFiles_RawContent", func(t *testing.T) {
		tmpDir := t.TempDir()

		mdContent := "# ADR-0001\n\n## Decision\nWe will use Go.\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "0001-use-go.md"), []byte(mdContent), 0644); err != nil {
			t.Fatal(err)
		}

		result := preloadDirectoryContext([]string{tmpDir}, 50000)

		if !strings.Contains(result, "ADR-0001") {
			t.Error("Expected preloaded context to include markdown content")
		}
		if !strings.Contains(result, "We will use Go") {
			t.Error("Expected preloaded context to include full markdown content")
		}
	})

	t.Run("RespectsCharBudget", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create a large file
		largeContent := strings.Repeat("x", 10000)
		if err := os.WriteFile(filepath.Join(tmpDir, "large.md"), []byte(largeContent), 0644); err != nil {
			t.Fatal(err)
		}

		result := preloadDirectoryContext([]string{tmpDir}, 5000)

		if len(result) > 6000 { // Some overhead for headers
			t.Errorf("Expected preloaded context to be under budget, got %d chars", len(result))
		}
	})

	t.Run("HandlesEmptyDirectory", func(t *testing.T) {
		tmpDir := t.TempDir()

		result := preloadDirectoryContext([]string{tmpDir}, 50000)

		if result != "" {
			t.Errorf("Expected empty result for empty directory, got %q", result)
		}
	})

	t.Run("HandlesNonexistentDirectory", func(t *testing.T) {
		result := preloadDirectoryContext([]string{"/nonexistent/path"}, 50000)

		if result != "" {
			t.Errorf("Expected empty result for nonexistent directory, got %q", result)
		}
	})

	t.Run("MultipleDirectories", func(t *testing.T) {
		dir1 := t.TempDir()
		dir2 := t.TempDir()

		if err := os.WriteFile(filepath.Join(dir1, "a.go"), []byte("package a\nfunc Alpha() {}\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir2, "b.go"), []byte("package b\nfunc Beta() {}\n"), 0644); err != nil {
			t.Fatal(err)
		}

		result := preloadDirectoryContext([]string{dir1, dir2}, 50000)

		if !strings.Contains(result, "Alpha") {
			t.Error("Expected result to include symbols from first directory")
		}
		if !strings.Contains(result, "Beta") {
			t.Error("Expected result to include symbols from second directory")
		}
	})

	t.Run("GoFiles_ASTFallbackOnTightBudget", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create a Go file with known symbols
		goContent := `package demo

import "context"

// LargeFunc does something.
func LargeFunc(ctx context.Context) error {
	// Imagine lots of code here
	return nil
}

// SmallFunc is brief.
func SmallFunc() int {
	return 42
}
`
		if err := os.WriteFile(filepath.Join(tmpDir, "demo.go"), []byte(goContent), 0644); err != nil {
			t.Fatal(err)
		}

		// With large budget: should get raw content (includes function bodies)
		largeBudget := preloadDirectoryContext([]string{tmpDir}, 50000)
		if !strings.Contains(largeBudget, "return nil") {
			t.Error("With large budget, expected raw content including function bodies")
		}

		// With tiny budget: should get AST-extracted symbols (no function bodies)
		tinyBudget := preloadDirectoryContext([]string{tmpDir}, 200)
		if tinyBudget != "" && strings.Contains(tinyBudget, "return nil") {
			t.Error("With tiny budget, expected AST extraction without function bodies")
		}
		if tinyBudget != "" && !strings.Contains(tinyBudget, "LargeFunc") {
			t.Error("With tiny budget, AST extraction should still include symbol names")
		}
	})
}

func TestProbeConfig_PreloadPaths_IntegrationWithLastToolOutput(t *testing.T) {
	t.Run("PreloadPathsWritesTempFileAndAugmentsGoal", func(t *testing.T) {
		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "handler.go"), []byte("package handlers\n\n// HandleRequest processes incoming requests.\nfunc HandleRequest() {}\n"), 0644); err != nil {
			t.Fatal(err)
		}

		config := compiler.ProbeConfig{
			Goal:         "Document all handlers",
			PreloadPaths: []string{tmpDir},
			AllowedTools: []string{"read_file", "list_dir"},
			StepBudget:   5,
		}

		// Simulate the preload injection (as RunProbe would do)
		preloaded := preloadDirectoryContext(config.PreloadPaths, defaultPreloadMaxChars)
		if preloaded == "" {
			t.Fatal("Expected non-empty preloaded content")
		}

		// Write temp file
		preloadFile := filepath.Join(config.PreloadPaths[0], ".preload_context.md")
		if err := os.WriteFile(preloadFile, []byte(preloaded), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(preloadFile)

		// Verify temp file contains the source content
		content, err := os.ReadFile(preloadFile)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "HandleRequest") {
			t.Error("Temp file should contain preloaded source content")
		}

		// Augment goal
		config.Goal = fmt.Sprintf("%s\n\nIMPORTANT: Start by reading the pre-compiled source context file at '%s'", config.Goal, preloadFile)

		// System prompt should NOT contain the source content itself
		systemPrompt := buildProbeSystemPrompt(config.Goal, config.AllowedTools, config.TaskContext, "")
		if strings.Contains(systemPrompt, "HandleRequest") {
			t.Error("System prompt should NOT contain preloaded source content")
		}
		// But it SHOULD contain the preload directive
		if !strings.Contains(systemPrompt, ".preload_context.md") {
			t.Error("System prompt goal should contain the preload file path directive")
		}
	})
}

func TestIsWebOnlyProbe(t *testing.T) {
	tests := []struct {
		name         string
		allowedTools []string
		want         bool
	}{
		{
			name:         "web_search_and_web_browse",
			allowedTools: []string{"web_search", "web_browse"},
			want:         true,
		},
		{
			name:         "web_search_only",
			allowedTools: []string{"web_search"},
			want:         true,
		},
		{
			name:         "web_browse_only",
			allowedTools: []string{"web_browse"},
			want:         true,
		},
		{
			name:         "mixed_web_and_file_tools",
			allowedTools: []string{"web_search", "web_browse", "read_file"},
			want:         false,
		},
		{
			name:         "codebase_tools_only",
			allowedTools: []string{"read_file", "list_dir", "search_files"},
			want:         false,
		},
		{
			name:         "empty_tools",
			allowedTools: []string{},
			want:         false,
		},
		{
			name:         "nil_tools",
			allowedTools: nil,
			want:         false,
		},
		{
			name:         "cache_tools",
			allowedTools: []string{"introspect_cache", "sql_cached_data"},
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isWebOnlyProbe(tt.allowedTools)
			if got != tt.want {
				t.Errorf("isWebOnlyProbe(%v) = %v, want %v", tt.allowedTools, got, tt.want)
			}
		})
	}
}
