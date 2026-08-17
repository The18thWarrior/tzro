package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompilationCommand_Go(t *testing.T) {
	cmd, available := CompilationCommand("go", "/tmp/project/cache/lru.go")
	if !available {
		t.Fatal("Go compilation command should be available")
	}
	if !strings.Contains(cmd, "go build") {
		t.Errorf("Go command should contain 'go build', got: %s", cmd)
	}
}

func TestCompilationCommand_TypeScript(t *testing.T) {
	cmd, available := CompilationCommand("typescript", "/tmp/project/src/emitter.ts")
	if !available {
		t.Fatal("TypeScript compilation command should be available")
	}
	if !strings.Contains(cmd, "tsc") {
		t.Errorf("TypeScript command should contain 'tsc', got: %s", cmd)
	}
	if !strings.Contains(cmd, "--noEmit") {
		t.Errorf("TypeScript command should include --noEmit, got: %s", cmd)
	}
	if !strings.Contains(cmd, "--target es2020") {
		t.Errorf("TypeScript command should include --target es2020 (prevents es5 false-negatives), got: %s", cmd)
	}
}

func TestCompilationCommand_Python(t *testing.T) {
	cmd, available := CompilationCommand("python", "/tmp/project/main.py")
	if !available {
		t.Fatal("Python compilation command should be available")
	}
	if !strings.Contains(cmd, "py_compile") {
		t.Errorf("Python command should contain 'py_compile', got: %s", cmd)
	}
}

func TestCompilationCommand_UnknownLanguage(t *testing.T) {
	_, available := CompilationCommand("brainfuck", "/tmp/project/main.bf")
	if available {
		t.Error("Unknown language should return available=false")
	}
}

func TestCompilationCommand_GoUsesPackageDir(t *testing.T) {
	cmd, _ := CompilationCommand("go", "/tmp/project/internal/cache/lru.go")
	// Go build should target the package directory, not the individual file
	if strings.Contains(cmd, "lru.go") {
		t.Errorf("Go command should not reference individual file, got: %s", cmd)
	}
	if !strings.HasSuffix(cmd, "/...") {
		t.Errorf("Go command should end with /... pattern, got: %s", cmd)
	}
}

func TestRunCompilationGate_ValidGoFile(t *testing.T) {
	// Create a temp Go module with a valid file
	dir := t.TempDir()
	modFile := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(modFile, []byte("module testmod\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	goFile := filepath.Join(dir, "main.go")
	validCode := "package testmod\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"
	if err := os.WriteFile(goFile, []byte(validCode), 0644); err != nil {
		t.Fatalf("failed to write go file: %v", err)
	}

	result := RunCompilationGate("go", goFile)
	if !result.Pass {
		t.Errorf("valid Go file should pass compilation gate, got: %s", result.Reason)
	}
}

func TestRunCompilationGate_InvalidGoFile(t *testing.T) {
	dir := t.TempDir()
	modFile := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(modFile, []byte("module testmod\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	goFile := filepath.Join(dir, "main.go")
	invalidCode := "package main\n\nfunc Add(a, b int) string {\n\treturn a + b\n}\n"
	if err := os.WriteFile(goFile, []byte(invalidCode), 0644); err != nil {
		t.Fatalf("failed to write go file: %v", err)
	}

	result := RunCompilationGate("go", goFile)
	if result.Pass {
		t.Error("invalid Go file should fail compilation gate")
	}
	if result.Reason == "" {
		t.Error("failure should include compiler error text")
	}
}

func TestRunCompilationGate_UnknownLanguageSkips(t *testing.T) {
	result := RunCompilationGate("brainfuck", "/nonexistent/file.bf")
	if !result.Pass {
		t.Error("unknown language should pass (skip) compilation gate")
	}
}

func TestRunCompilationGate_NonexistentFile(t *testing.T) {
	result := RunCompilationGate("go", "/nonexistent/path/main.go")
	if result.Pass {
		t.Error("nonexistent file should fail compilation gate")
	}
}

func TestCompilationCommand_TypeScriptWithTsconfig(t *testing.T) {
	dir := t.TempDir()
	tsconfig := filepath.Join(dir, "tsconfig.json")
	_ = os.WriteFile(tsconfig, []byte(`{"compilerOptions":{"strict":true}}`), 0644)

	// File in a subdirectory should find tsconfig in parent
	srcDir := filepath.Join(dir, "src")
	_ = os.MkdirAll(srcDir, 0755)
	tsFile := filepath.Join(srcDir, "config.ts")

	cmd, available := CompilationCommand("typescript", tsFile)
	if !available {
		t.Fatal("TypeScript compilation command should be available")
	}
	if !strings.Contains(cmd, "--project") {
		t.Errorf("should use --project when tsconfig.json exists, got: %s", cmd)
	}
	if !strings.Contains(cmd, "tsconfig.json") {
		t.Errorf("should reference tsconfig.json path, got: %s", cmd)
	}
}

func TestCompilationCommand_TypeScriptWithoutTsconfig(t *testing.T) {
	dir := t.TempDir()
	tsFile := filepath.Join(dir, "config.ts")

	cmd, available := CompilationCommand("typescript", tsFile)
	if !available {
		t.Fatal("TypeScript compilation command should be available")
	}
	if strings.Contains(cmd, "--project") {
		t.Errorf("should NOT use --project without tsconfig.json, got: %s", cmd)
	}
	if !strings.Contains(cmd, "--skipLibCheck") {
		t.Errorf("fallback should include --skipLibCheck, got: %s", cmd)
	}
}

func TestRunCompilationGate_ValidTypeScriptWithShim(t *testing.T) {
	// Skip if npx/tsc is not available
	if _, err := os.Stat("/usr/local/bin/npx"); os.IsNotExist(err) {
		if _, err := os.Stat("/opt/homebrew/bin/npx"); os.IsNotExist(err) {
			t.Skip("npx not found, skipping TypeScript compilation test")
		}
	}

	dir := t.TempDir()

	// Create tsconfig.json
	tsconfig := `{
  "compilerOptions": {
    "strict": true,
    "target": "es2020",
    "lib": ["es2020"],
    "moduleResolution": "node",
    "noEmit": true,
    "skipLibCheck": true,
    "typeRoots": ["./typings"]
  },
  "include": ["**/*.ts"]
}`
	_ = os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(tsconfig), 0644)

	// Create ambient type shim
	typingsDir := filepath.Join(dir, "typings", "node")
	_ = os.MkdirAll(typingsDir, 0755)
	nodeShim := `declare var process: {
  env: Record<string, string | undefined>;
  exit(code?: number): never;
  cwd(): string;
};
`
	_ = os.WriteFile(filepath.Join(typingsDir, "index.d.ts"), []byte(nodeShim), 0644)

	// Write a TypeScript file that uses process.env
	tsFile := filepath.Join(dir, "config.ts")
	validCode := `export function getPort(): number {
  const raw = process.env.PORT;
  return raw ? parseInt(raw, 10) : 3000;
}
`
	_ = os.WriteFile(tsFile, []byte(validCode), 0644)

	result := RunCompilationGate("typescript", tsFile)
	if !result.Pass {
		t.Errorf("valid TypeScript with process.env shim should pass, got: %s", result.Reason)
	}
}

func TestSanitizeSourceCode_StripsConversationalPreamble(t *testing.T) {
	rawGo := `Here is the complete updated Go code with all original public methods preserved:

package models

import "errors"

type User struct {
	ID string
}
`
	sanitizedGo := SanitizeSourceCode(rawGo, "go")
	if !strings.HasPrefix(strings.TrimSpace(sanitizedGo), "package models") {
		t.Errorf("expected Go code to start with 'package models', got:\n%s", sanitizedGo)
	}

	rawTS := `Sure! Below is the requested TypeScript event emitter implementation:

export interface EventMap {
	[event: string]: unknown[];
}
`
	sanitizedTS := SanitizeSourceCode(rawTS, "typescript")
	if !strings.HasPrefix(strings.TrimSpace(sanitizedTS), "export interface EventMap") {
		t.Errorf("expected TS code to start with 'export interface EventMap', got:\n%s", sanitizedTS)
	}
}

