package codegen

import (
	"strings"
	"testing"
)

func TestTreeSitterASTLinter_GoMissingJSONImport(t *testing.T) {
	code := `package mypkg

import "fmt"

func Process(data []byte) error {
	var m map[string]interface{}
	err := json.Unmarshal(data, &m)
	if err != nil {
		return err
	}
	fmt.Println(m)
	return nil
}`

	linter := NewTreeSitterASTLinter()
	violations, err := linter.CheckImports("processor.go", []byte(code))
	if err != nil {
		t.Fatalf("CheckImports failed: %v", err)
	}

	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for missing json import, got %d: %v", len(violations), violations)
	}

	if violations[0].Namespace != "json" {
		t.Errorf("expected namespace 'json', got %q", violations[0].Namespace)
	}
	if !strings.Contains(violations[0].Message, "encoding/json") {
		t.Errorf("expected message to mention 'encoding/json', got %q", violations[0].Message)
	}
}

func TestTreeSitterASTLinter_GoWithValidImports(t *testing.T) {
	code := `package mypkg

import (
	"encoding/json"
	"fmt"
)

func Process(data []byte) error {
	var m map[string]interface{}
	err := json.Unmarshal(data, &m)
	if err != nil {
		return err
	}
	fmt.Println(m)
	return nil
}`

	linter := NewTreeSitterASTLinter()
	violations, err := linter.CheckImports("processor.go", []byte(code))
	if err != nil {
		t.Fatalf("CheckImports failed: %v", err)
	}

	if len(violations) != 0 {
		t.Errorf("expected 0 violations for valid imports, got %d: %v", len(violations), violations)
	}
}

func TestTreeSitterASTLinter_PythonMissingOSImport(t *testing.T) {
	code := `import sys

def get_home():
    return os.environ.get("HOME")
`

	linter := NewTreeSitterASTLinter()
	violations, err := linter.CheckImports("util.py", []byte(code))
	if err != nil {
		t.Fatalf("CheckImports failed: %v", err)
	}

	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for missing os import, got %d: %v", len(violations), violations)
	}

	if violations[0].Namespace != "os" {
		t.Errorf("expected namespace 'os', got %q", violations[0].Namespace)
	}
}

func TestTreeSitterASTLinter_TypeScriptMissingPathImport(t *testing.T) {
	code := `import * as fs from 'fs';

export function getFullPath(rel: string): string {
    return path.join('/tmp', rel);
}
`

	linter := NewTreeSitterASTLinter()
	violations, err := linter.CheckImports("path_util.ts", []byte(code))
	if err != nil {
		t.Fatalf("CheckImports failed: %v", err)
	}

	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for missing path import, got %d: %v", len(violations), violations)
	}

	if violations[0].Namespace != "path" {
		t.Errorf("expected namespace 'path', got %q", violations[0].Namespace)
	}
}
