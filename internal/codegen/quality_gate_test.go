package codegen

import (
	"testing"
)

func TestStructuralQualityGate_ValidGoCode(t *testing.T) {
	code := `package cache

import (
	"sync"
	"time"
)

type Cache struct {
	mu sync.RWMutex
}

func New() *Cache {
	return &Cache{}
}
`
	result := RunStructuralQualityGate(code, "go")
	if !result.Pass {
		t.Errorf("valid Go code should pass quality gate, got: %s", result.Reason)
	}
}

func TestStructuralQualityGate_EmptyOutput(t *testing.T) {
	result := RunStructuralQualityGate("", "go")
	if result.Pass {
		t.Error("empty output should fail quality gate")
	}
	if result.Reason != "output is empty" {
		t.Errorf("unexpected reason: %s", result.Reason)
	}
}

func TestStructuralQualityGate_MarkdownFences(t *testing.T) {
	code := "```go\npackage foo\n```"
	result := RunStructuralQualityGate(code, "go")
	if result.Pass {
		t.Error("output with markdown fences should fail quality gate")
	}
}

func TestStructuralQualityGate_ProseOutput(t *testing.T) {
	code := "Here is the implementation:\npackage foo\nfunc Bar() {}"
	result := RunStructuralQualityGate(code, "go")
	if result.Pass {
		t.Error("output starting with prose should fail quality gate")
	}
}

func TestStructuralQualityGate_NoLanguageKeywords(t *testing.T) {
	code := "this is just random text without any code keywords"
	result := RunStructuralQualityGate(code, "go")
	if result.Pass {
		t.Error("output without Go keywords should fail quality gate")
	}
}

func TestStructuralQualityGate_ValidTypeScript(t *testing.T) {
	code := `export class EventEmitter {
  private listeners: Map<string, Set<Function>>;

  constructor() {
    this.listeners = new Map();
  }
}
`
	result := RunStructuralQualityGate(code, "typescript")
	if !result.Pass {
		t.Errorf("valid TypeScript code should pass, got: %s", result.Reason)
	}
}

func TestStructuralQualityGate_ValidPython(t *testing.T) {
	code := `import os
from pathlib import Path

def process(path: str) -> bool:
    return Path(path).exists()
`
	result := RunStructuralQualityGate(code, "python")
	if !result.Pass {
		t.Errorf("valid Python code should pass, got: %s", result.Reason)
	}
}

func TestStructuralQualityGate_UnknownLanguageSkipsKeywordCheck(t *testing.T) {
	// Unknown language should only check structure, not keywords
	code := "some content that is not standard code"
	result := RunStructuralQualityGate(code, "brainfuck")
	if !result.Pass {
		t.Errorf("unknown language should skip keyword check, got: %s", result.Reason)
	}
}
