package repomap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateRepoMap(t *testing.T) {
	t.Run("ProducesStructuredMarkdown", func(t *testing.T) {
		// Create a temp Go source tree
		tmpDir := t.TempDir()
		pkgDir := filepath.Join(tmpDir, "internal", "cache")
		if err := os.MkdirAll(pkgDir, 0755); err != nil {
			t.Fatal(err)
		}

		goSource := `package cache

import "sync"

// Cache provides an in-memory LRU cache.
type Cache struct {
	mu sync.Mutex
	items map[string]interface{}
}

// Eviction is an interface for eviction policies.
type Eviction interface {
	ShouldEvict(key string) bool
}

// NewCache creates a new Cache instance.
func NewCache() *Cache {
	return &Cache{items: make(map[string]interface{})}
}

// Get retrieves a value from the cache.
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[key]
	return v, ok
}

func privateHelper() {}
`
		if err := os.WriteFile(filepath.Join(pkgDir, "cache.go"), []byte(goSource), 0644); err != nil {
			t.Fatal(err)
		}

		result, err := GenerateRepoMap(tmpDir)
		if err != nil {
			t.Fatalf("GenerateRepoMap failed: %v", err)
		}

		// Should contain the header
		if !strings.Contains(result, "# ") {
			t.Error("Expected markdown header")
		}

		// Should contain package name
		if !strings.Contains(result, "cache") {
			t.Error("Expected package name 'cache' in output")
		}

		// Should contain exported struct
		if !strings.Contains(result, "Cache") {
			t.Error("Expected exported struct 'Cache' in output")
		}

		// Should contain exported interface
		if !strings.Contains(result, "Eviction") {
			t.Error("Expected exported interface 'Eviction' in output")
		}

		// Should contain exported functions
		if !strings.Contains(result, "NewCache") {
			t.Error("Expected exported function 'NewCache' in output")
		}
		if !strings.Contains(result, "Get") {
			t.Error("Expected exported method 'Get' in output")
		}

		// Should NOT contain unexported functions
		if strings.Contains(result, "privateHelper") {
			t.Error("Should NOT contain unexported function 'privateHelper'")
		}
	})

	t.Run("SkipsTestFiles", func(t *testing.T) {
		tmpDir := t.TempDir()
		pkgDir := filepath.Join(tmpDir, "pkg")
		if err := os.MkdirAll(pkgDir, 0755); err != nil {
			t.Fatal(err)
		}

		testFile := `package pkg

func TestSomething() {}
`
		if err := os.WriteFile(filepath.Join(pkgDir, "pkg_test.go"), []byte(testFile), 0644); err != nil {
			t.Fatal(err)
		}

		result, err := GenerateRepoMap(tmpDir)
		if err != nil {
			t.Fatalf("GenerateRepoMap failed: %v", err)
		}

		if strings.Contains(result, "TestSomething") {
			t.Error("Should NOT contain test functions from _test.go files")
		}
	})

	t.Run("SkipsNonGoFiles", func(t *testing.T) {
		tmpDir := t.TempDir()
		pkgDir := filepath.Join(tmpDir, "docs")
		if err := os.MkdirAll(pkgDir, 0755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(pkgDir, "readme.md"), []byte("# README"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "data.json"), []byte(`{"key": "val"}`), 0644); err != nil {
			t.Fatal(err)
		}

		result, err := GenerateRepoMap(tmpDir)
		if err != nil {
			t.Fatalf("GenerateRepoMap failed: %v", err)
		}

		// Should produce valid but minimal output
		if !strings.Contains(result, "#") {
			t.Error("Expected at least a header in output")
		}
	})

	t.Run("HandlesEmptyDirectory", func(t *testing.T) {
		tmpDir := t.TempDir()

		result, err := GenerateRepoMap(tmpDir)
		if err != nil {
			t.Fatalf("GenerateRepoMap failed: %v", err)
		}

		// Should return valid markdown with header
		if !strings.Contains(result, "#") {
			t.Error("Expected header in output for empty directory")
		}
		// Should not have file entries
		if strings.Contains(result, "## File:") {
			t.Error("Should not have file entries for empty directory")
		}
	})

	t.Run("MultiplePackages", func(t *testing.T) {
		tmpDir := t.TempDir()
		pkg1 := filepath.Join(tmpDir, "internal", "auth")
		pkg2 := filepath.Join(tmpDir, "internal", "db")
		os.MkdirAll(pkg1, 0755)
		os.MkdirAll(pkg2, 0755)

		os.WriteFile(filepath.Join(pkg1, "auth.go"), []byte(`package auth
type Token struct{}
func Authenticate() {}
`), 0644)

		os.WriteFile(filepath.Join(pkg2, "db.go"), []byte(`package db
type Connection struct{}
func Connect() {}
`), 0644)

		result, err := GenerateRepoMap(tmpDir)
		if err != nil {
			t.Fatalf("GenerateRepoMap failed: %v", err)
		}

		// Should contain both packages
		if !strings.Contains(result, "auth") {
			t.Error("Expected package 'auth' in output")
		}
		if !strings.Contains(result, "db") {
			t.Error("Expected package 'db' in output")
		}
		if !strings.Contains(result, "Token") {
			t.Error("Expected struct 'Token' from auth package")
		}
		if !strings.Contains(result, "Connection") {
			t.Error("Expected struct 'Connection' from db package")
		}
	})
}
