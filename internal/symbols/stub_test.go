package symbols

import (
	"strings"
	"testing"
)

func TestStubCodeBodies_Go(t *testing.T) {
	source := `package cache

import (
	"context"
	"fmt"
)

// Cache is a thread-safe in-memory cache.
type Cache struct {
	data map[string]string
}

// New creates a new Cache.
func New() *Cache {
	c := &Cache{
		data: make(map[string]string),
	}
	fmt.Println("Cache initialized")
	return c
}

// Get retrieves a key from cache.
func (c *Cache) Get(ctx context.Context, key string) (string, bool) {
	val, ok := c.data[key]
	if !ok {
		return "", false
	}
	return val, true
}
`

	stubbed := StubCodeBodies("cache.go", []byte(source))

	// Verify type declarations and struct fields remain intact
	if !strings.Contains(stubbed, "type Cache struct") {
		t.Errorf("Expected Cache struct in stubbed code, got:\n%s", stubbed)
	}
	if !strings.Contains(stubbed, "data map[string]string") {
		t.Errorf("Expected struct fields in stubbed code, got:\n%s", stubbed)
	}

	// Verify function signatures remain intact
	if !strings.Contains(stubbed, "func New() *Cache") {
		t.Errorf("Expected func New signature in stubbed code, got:\n%s", stubbed)
	}
	if !strings.Contains(stubbed, "func (c *Cache) Get(ctx context.Context, key string) (string, bool)") {
		t.Errorf("Expected func Get signature in stubbed code, got:\n%s", stubbed)
	}

	// Verify internal function bodies are stubbed out
	if strings.Contains(stubbed, "fmt.Println(\"Cache initialized\")") {
		t.Errorf("Internal body lines should be stubbed out, got:\n%s", stubbed)
	}
	if strings.Contains(stubbed, "val, ok := c.data[key]") {
		t.Errorf("Method body lines should be stubbed out, got:\n%s", stubbed)
	}

	// Verify stub indicator exists
	if !strings.Contains(stubbed, "/* ... */") && !strings.Contains(stubbed, "...") {
		t.Errorf("Expected stub indicator in function body, got:\n%s", stubbed)
	}
}

func TestStubCodeBodies_Python(t *testing.T) {
	source := `import os
import sys

class ModelRunner:
    def __init__(self, model_path: str):
        self.model_path = model_path
        self.loaded = False
        print(f"Loading {model_path}")

    def generate(self, prompt: str, max_tokens: int = 100) -> str:
        tokens = prompt.split()
        if len(tokens) == 0:
            return ""
        return "generated response"
`

	stubbed := StubCodeBodies("runner.py", []byte(source))

	// Verify class and method signatures are preserved
	if !strings.Contains(stubbed, "class ModelRunner:") {
		t.Errorf("Expected class declaration in stubbed code, got:\n%s", stubbed)
	}
	if !strings.Contains(stubbed, "def __init__(self, model_path: str):") {
		t.Errorf("Expected __init__ signature in stubbed code, got:\n%s", stubbed)
	}
	if !strings.Contains(stubbed, "def generate(self, prompt: str, max_tokens: int = 100) -> str:") {
		t.Errorf("Expected generate signature in stubbed code, got:\n%s", stubbed)
	}

	// Verify bodies are stubbed
	if strings.Contains(stubbed, "print(f\"Loading {model_path}\")") {
		t.Errorf("Python method body should be stubbed out, got:\n%s", stubbed)
	}
	if strings.Contains(stubbed, "tokens = prompt.split()") {
		t.Errorf("Python method body should be stubbed out, got:\n%s", stubbed)
	}
}
