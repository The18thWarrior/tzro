package context

import (
	stdctx "context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPythonAdapter_Comprehensive(t *testing.T) {
	wsDir := t.TempDir()

	// 1. Create src/ package layout
	mustWrite(t, filepath.Join(wsDir, "src", "mypkg", "__init__.py"), `
from .core import Worker
from .sub import helper
`)
	mustWrite(t, filepath.Join(wsDir, "src", "mypkg", "core.py"), `
class Worker:
    def __init__(self, name):
        self.name = name

    def execute(self):
        return f"working: {self.name}"
`)
	mustWrite(t, filepath.Join(wsDir, "src", "mypkg", "sub.py"), `
from ..core import Worker

def helper():
    w = Worker("sub")
    return w.execute()
`)

	// 2. Caller with alias
	mustWrite(t, filepath.Join(wsDir, "app.py"), `
from mypkg import Worker as MyWorker

def run():
    w = MyWorker("main")
    return w.execute()
`)

	// 3. Shadowing: local variable/parameter shadows Worker
	mustWrite(t, filepath.Join(wsDir, "shadow.py"), `
from mypkg import Worker

def process(Worker):
    return Worker + 1
`)

	// 4. Test files and conftest.py
	mustWrite(t, filepath.Join(wsDir, "tests", "conftest.py"), `
import pytest
from mypkg.core import Worker

@pytest.fixture
def shared_worker():
    return Worker("fixture")
`)
	mustWrite(t, filepath.Join(wsDir, "tests", "test_core.py"), `
from mypkg.core import Worker

def test_worker():
    w = Worker("test")
    assert w.execute() == "working: test"
`)
	// Inferred test (naming convention without explicit call)
	mustWrite(t, filepath.Join(wsDir, "tests", "test_core_inferred.py"), `
# Test file matching core naming convention but without direct reference
def test_something_else():
    pass
`)

	// 5. Limitations: star import, importlib, getattr
	mustWrite(t, filepath.Join(wsDir, "wildcard.py"), `
from mypkg.core import *
w = Worker("wild")
`)
	mustWrite(t, filepath.Join(wsDir, "dynamic.py"), `
import importlib
mod = importlib.import_module("mypkg.core")
w_cls = getattr(mod, "Worker")
`)

	// 6. Config file
	mustWrite(t, filepath.Join(wsDir, "pyproject.toml"), `
[tool.poetry.scripts]
run-worker = "mypkg.core:Worker"
`)

	// 7. Virtual environment that must be excluded
	mustWrite(t, filepath.Join(wsDir, ".venv", "lib", "mypkg", "core.py"), `
class Worker:
    pass
`)

	adapter := NewPythonAdapter(nil, nil)
	sym := Symbol{
		Name:     "Worker",
		Kind:     "class",
		FilePath: "src/mypkg/core.py",
		Language: "python",
	}

	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 10*time.Second)
	defer cancel()

	refs, cov, err := adapter.FindReferencesBatch(ctx, wsDir, []Symbol{sym}, nil, false)
	if err != nil {
		t.Fatalf("FindReferencesBatch failed: %v", err)
	}

	if cov == nil {
		t.Fatalf("expected non-nil coverage report")
	}

	// Verify coverage limitations detected
	if !cov.IncompleteDiscovery {
		t.Errorf("expected IncompleteDiscovery = true due to star import and dynamic imports")
	}
	if len(cov.UnsupportedSyntax) == 0 {
		t.Errorf("expected UnsupportedSyntax for star import and getattr")
	}
	if len(cov.UnresolvedImports) == 0 {
		t.Errorf("expected UnresolvedImports for importlib")
	}

	// Verify files found
	fileMap := make(map[string]RawReference)
	for _, r := range refs {
		fileMap[r.FilePath] = r
	}

	// Virtual environment must NOT be in refs
	for f := range fileMap {
		if filepath.HasPrefix(f, ".venv") {
			t.Errorf("found reference in excluded .venv directory: %s", f)
		}
	}

	// src/mypkg/sub.py should have syntactic reference
	if ref, ok := fileMap["src/mypkg/sub.py"]; !ok {
		t.Errorf("expected reference in src/mypkg/sub.py")
	} else if ref.Precision != PrecisionSyntactic {
		t.Errorf("expected precision syntactic, got %s", ref.Precision)
	}

	// app.py with alias MyWorker should have syntactic reference
	if ref, ok := fileMap["app.py"]; !ok {
		t.Errorf("expected reference in app.py with alias")
	} else if ref.Precision != PrecisionSyntactic {
		t.Errorf("expected precision syntactic for app.py, got %s", ref.Precision)
	}

	// shadow.py should NOT have reference because Worker is shadowed by param
	if _, ok := fileMap["shadow.py"]; ok {
		t.Errorf("shadow.py should not be recorded as a reference due to parameter shadowing")
	}

	// tests/test_core.py should have test relationship
	if ref, ok := fileMap["tests/test_core.py"]; !ok {
		t.Errorf("expected reference in tests/test_core.py")
	} else if ref.Relationship != RelTest {
		t.Errorf("expected RelTest for tests/test_core.py, got %s", ref.Relationship)
	}

	// tests/test_core_inferred.py should have precision inferred (naming convention)
	if ref, ok := fileMap["tests/test_core_inferred.py"]; !ok {
		t.Errorf("expected inferred reference in tests/test_core_inferred.py")
	} else if ref.Precision != PrecisionInferred {
		t.Errorf("expected PrecisionInferred for naming convention, got %s", ref.Precision)
	}

	// pyproject.toml should have config relationship
	if ref, ok := fileMap["pyproject.toml"]; !ok {
		t.Errorf("expected reference in pyproject.toml")
	} else if ref.Relationship != RelConfig {
		t.Errorf("expected RelConfig for pyproject.toml, got %s", ref.Relationship)
	}
}

func TestRustAdapter_Comprehensive(t *testing.T) {
	wsDir := t.TempDir()

	// 1. Cargo workspace with two crates
	mustWrite(t, filepath.Join(wsDir, "Cargo.toml"), `
[workspace]
members = [
    "crates/core",
    "crates/cli",
]
`)
	mustWrite(t, filepath.Join(wsDir, "Cargo.lock"), `
[[package]]
name = "core"
version = "0.1.0"
`)

	// Crate: core
	mustWrite(t, filepath.Join(wsDir, "crates", "core", "Cargo.toml"), `
[package]
name = "core"
version = "0.1.0"
`)
	mustWrite(t, filepath.Join(wsDir, "crates", "core", "src", "lib.rs"), `
pub struct Engine;

impl Engine {
    pub fn start(&self) {}
}

pub mod helper;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_engine() {
        let e = Engine;
        e.start();
    }
}
`)
	// Submodule re-export
	mustWrite(t, filepath.Join(wsDir, "crates", "core", "src", "helper.rs"), `
pub use crate::Engine;
`)
	// Integration test
	mustWrite(t, filepath.Join(wsDir, "crates", "core", "tests", "test_engine.rs"), `
use core::Engine;

#[test]
fn integration_engine() {
    let e = Engine;
}
`)
	// Inferred test by naming convention without direct Engine call
	mustWrite(t, filepath.Join(wsDir, "crates", "core", "tests", "test_engine_inferred.rs"), `
#[test]
fn test_inferred() {}
`)

	// Crate: cli
	mustWrite(t, filepath.Join(wsDir, "crates", "cli", "Cargo.toml"), `
[package]
name = "cli"
version = "0.1.0"
`)
	// Grouped use and alias
	mustWrite(t, filepath.Join(wsDir, "crates", "cli", "src", "main.rs"), `
use core::{helper::Engine as CustomEngine, helper};

fn main() {
    let e = CustomEngine;
    e.start();
}
`)

	// Limitations: glob use, macro, and conditional cfg
	mustWrite(t, filepath.Join(wsDir, "crates", "cli", "src", "advanced.rs"), `
use core::*;

#[cfg(feature = "special")]
fn special_run() {
    let e = Engine;
}

macro_rules! my_macro {
    () => { let _ = Engine; };
}
`)

	adapter := NewRustAdapter(nil, nil)
	sym := Symbol{
		Name:     "Engine",
		Kind:     "type",
		FilePath: "crates/core/src/lib.rs",
		Language: "rust",
	}

	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 10*time.Second)
	defer cancel()

	refs, cov, err := adapter.FindReferencesBatch(ctx, wsDir, []Symbol{sym}, nil, false)
	if err != nil {
		t.Fatalf("Rust FindReferencesBatch failed: %v", err)
	}

	if cov == nil {
		t.Fatalf("expected non-nil coverage report")
	}

	// Verify coverage limitations detected
	if !cov.IncompleteDiscovery {
		t.Errorf("expected IncompleteDiscovery = true due to glob import and macros")
	}
	if len(cov.UnsupportedSyntax) == 0 {
		t.Errorf("expected UnsupportedSyntax for macro, glob, or cfg")
	}

	fileMap := make(map[string]RawReference)
	for _, r := range refs {
		fileMap[r.FilePath] = r
	}

	// crates/cli/src/main.rs with grouped use and alias CustomEngine
	if ref, ok := fileMap["crates/cli/src/main.rs"]; !ok {
		t.Errorf("expected reference in crates/cli/src/main.rs")
	} else if ref.Precision != PrecisionSyntactic {
		t.Errorf("expected precision syntactic for main.rs, got %s", ref.Precision)
	}

	// Inline test in crates/core/src/lib.rs should have RelTest
	if ref, ok := fileMap["crates/core/src/lib.rs"]; !ok {
		t.Errorf("expected inline test reference in crates/core/src/lib.rs")
	} else if ref.Relationship != RelTest {
		t.Errorf("expected RelTest for inline test in lib.rs, got %s", ref.Relationship)
	}

	// Integration test in crates/core/tests/test_engine.rs should have RelTest
	if ref, ok := fileMap["crates/core/tests/test_engine.rs"]; !ok {
		t.Errorf("expected reference in crates/core/tests/test_engine.rs")
	} else if ref.Relationship != RelTest {
		t.Errorf("expected RelTest for test_engine.rs, got %s", ref.Relationship)
	}

	// Inferred test by naming convention
	if ref, ok := fileMap["crates/core/tests/test_engine_inferred.rs"]; !ok {
		t.Errorf("expected inferred reference in test_engine_inferred.rs")
	} else if ref.Precision != PrecisionInferred {
		t.Errorf("expected PrecisionInferred for naming convention, got %s", ref.Precision)
	}

	// Config file
	if ref, ok := fileMap["Cargo.lock"]; !ok {
		t.Errorf("expected reference in Cargo.lock")
	} else if ref.Relationship != RelConfig {
		t.Errorf("expected RelConfig for Cargo.lock, got %s", ref.Relationship)
	}
}

func TestMixedLanguageWorkspace(t *testing.T) {
	wsDir := t.TempDir()

	// Go file
	mustWrite(t, filepath.Join(wsDir, "go.mod"), "module example.com/mixed\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(wsDir, "pkg", "service.go"), `package pkg
type GoService struct{}
func (s *GoService) Run() {}
`)
	mustWrite(t, filepath.Join(wsDir, "pkg", "service_test.go"), `package pkg
import "testing"
func TestGoService(t *testing.T) {
	s := &GoService{}
	s.Run()
}
`)

	// TS file
	mustWrite(t, filepath.Join(wsDir, "package.json"), `{"name": "mixed"}`)
	mustWrite(t, filepath.Join(wsDir, "src", "client.ts"), `
export class Client {
    connect() {}
}
`)
	mustWrite(t, filepath.Join(wsDir, "src", "client.test.ts"), `
import { Client } from './client';
test('client', () => {
    const c = new Client();
    c.connect();
});
`)

	// Python file
	mustWrite(t, filepath.Join(wsDir, "pyproject.toml"), `[tool.poetry]\nname = "mixed"`)
	mustWrite(t, filepath.Join(wsDir, "backend", "worker.py"), `
class PyWorker:
    def do_work(self): pass
`)
	mustWrite(t, filepath.Join(wsDir, "tests", "test_worker.py"), `
from backend.worker import PyWorker
def test_worker():
    w = PyWorker()
    w.do_work()
`)

	// Rust file
	mustWrite(t, filepath.Join(wsDir, "Cargo.toml"), `[package]\nname = "mixed"\nversion = "0.1.0"`)
	mustWrite(t, filepath.Join(wsDir, "rust_src", "lib.rs"), `
pub struct RustEngine;
`)
	mustWrite(t, filepath.Join(wsDir, "rust_src", "tests", "test_engine.rs"), `
use mixed::RustEngine;
`)

	registry := NewAdapterRegistry(nil, nil)
	analyzer := NewImpactAnalyzerWithRegistry(nil, nil, registry)

	symbols := []Symbol{
		{Name: "GoService", Kind: "type", FilePath: "pkg/service.go", Language: "go"},
		{Name: "Client", Kind: "type", FilePath: "src/client.ts", Language: "typescript"},
		{Name: "PyWorker", Kind: "class", FilePath: "backend/worker.py", Language: "python"},
		{Name: "RustEngine", Kind: "type", FilePath: "rust_src/lib.rs", Language: "rust"},
	}

	report, pack, err := analyzer.AnalyzeReport(stdctx.Background(), wsDir, symbols, 5000, false, "all")
	if err != nil {
		t.Fatalf("AnalyzeImpact failed on mixed workspace: %v", err)
	}

	if report == nil || pack == nil {
		t.Fatalf("expected non-nil report and pack")
	}

	// Verify all candidate test files were discovered across languages
	testFilesFound := make(map[string]bool)
	for _, tf := range report.CandidateTestFiles {
		testFilesFound[tf] = true
	}

	if !testFilesFound["pkg/service_test.go"] {
		t.Errorf("missing Go test file pkg/service_test.go in %v", report.CandidateTestFiles)
	}
	if !testFilesFound["src/client.test.ts"] {
		t.Errorf("missing TS test file src/client.test.ts in %v", report.CandidateTestFiles)
	}
	if !testFilesFound["tests/test_worker.py"] {
		t.Errorf("missing Python test file tests/test_worker.py in %v", report.CandidateTestFiles)
	}
}
