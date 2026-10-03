package turnreduction

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGradeFixture_TypeScriptRunsPlainGradingFilename(t *testing.T) {
	for _, tool := range []string{"node", "tsc"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required: %v", tool, err)
		}
	}
	ws, private := t.TempDir(), t.TempDir()
	writeGradeFile(t, ws, "tsconfig.json", `{"compilerOptions":{"module":"commonjs","target":"ES2022","outDir":"dist","types":[]}}`)
	writeGradeFile(t, ws, "answer.ts", "export const answer = 41;\n")
	writeGradeFile(t, private, "grading.test.ts", `declare const require: any;
const test = require('node:test');
const assert = require('node:assert/strict');
import { answer } from './answer';
test('grading answer', () => assert.equal(answer, 42));
`)
	f := Fixture{Language: "typescript", GradingDir: private}
	passed, log, err := GradeFixture(context.Background(), f, ws)
	if err != nil {
		t.Fatal(err)
	}
	if passed || !strings.Contains(log, "# fail 1") {
		t.Fatalf("the failing private test must run; passed=%v, log=%s", passed, log)
	}
}

func writeGradeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestGradeFixture_ZeroTestsCannotPass(t *testing.T) {
	for _, language := range []string{"go", "python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			ws, private := t.TempDir(), t.TempDir()
			switch language {
			case "go":
				writeGradeFile(t, ws, "go.mod", "module example\n\ngo 1.22\n")
				writeGradeFile(t, private, "grading_test.go", "package example\n")
			case "python":
				writeGradeFile(t, private, "test_grading.py", "# No tests\n")
			case "typescript":
				if _, err := exec.LookPath("tsc"); err != nil {
					t.Skip("tsc unavailable")
				}
				writeGradeFile(t, ws, "tsconfig.json", `{"compilerOptions":{"outDir":"dist","types":[]}}`)
				writeGradeFile(t, private, "empty.grading.test.ts", "// No tests\n")
			}
			passed, log, err := GradeFixture(context.Background(), Fixture{Language: language, GradingDir: private}, ws)
			if passed {
				t.Fatalf("zero tests were credited as passing: %s", log)
			}
			if err == nil || !strings.Contains(err.Error(), "no passing private tests") {
				t.Fatalf("missing grading coverage must be explicit: %v, %s", err, log)
			}
		})
	}
}

func TestGradeFixture_PreservesAgentWorkspace(t *testing.T) {
	ws, private := t.TempDir(), t.TempDir()
	writeGradeFile(t, ws, "answer.py", "answer = 42\n")
	writeGradeFile(t, ws, "test_grading.py", "# agent-owned sentinel\n")
	writeGradeFile(t, private, "test_grading.py", "import unittest\nfrom answer import answer\nclass Grade(unittest.TestCase):\n def test_answer(self): self.assertEqual(answer, 42)\n")
	passed, log, err := GradeFixture(context.Background(), Fixture{Language: "python", GradingDir: private}, ws)
	if !passed || err != nil {
		t.Fatalf("valid solution failed: %v, %s", err, log)
	}
	data, err := os.ReadFile(filepath.Join(ws, "test_grading.py"))
	if err != nil || string(data) != "# agent-owned sentinel\n" {
		t.Fatalf("private grading modified the agent workspace: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(ws, "__pycache__")); !os.IsNotExist(err) {
		t.Fatalf("grading must run outside the agent workspace: %v", err)
	}
}

func TestGradeFixture_RejectsWorkspaceSymlinks(t *testing.T) {
	ws, private, outside := t.TempDir(), t.TempDir(), t.TempDir()
	writeGradeFile(t, outside, "answer.py", "answer = 42\n")
	if err := os.Symlink(filepath.Join(outside, "answer.py"), filepath.Join(ws, "answer.py")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writeGradeFile(t, private, "test_grading.py", "import unittest\nfrom answer import answer\nclass Grade(unittest.TestCase):\n def test_answer(self): self.assertEqual(answer, 42)\n")
	passed, _, err := GradeFixture(context.Background(), Fixture{Language: "python", GradingDir: private}, ws)
	if passed || err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("grading accepted external input via symlink: passed=%v err=%v", passed, err)
	}
}

func TestGradeFixture_RequiresAllDeclaredPrivateTests(t *testing.T) {
	ws, private := t.TempDir(), t.TempDir()
	writeGradeFile(t, private, "test_grading.py", "import unittest\nclass Grade(unittest.TestCase):\n def test_first(self): self.assertTrue(True)\n")
	f := Fixture{Language: "python", GradingDir: private, RequiredGradeTests: []string{"test_first", "test_missing"}}
	passed, _, err := GradeFixture(context.Background(), f, ws)
	if passed || err == nil || !strings.Contains(err.Error(), "test_missing") {
		t.Fatalf("a missing required test must invalidate the grade: passed=%v err=%v", passed, err)
	}
}

func TestGradeFixture_CancelledContextIsInfrastructureFailure(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	passed, _, err := GradeFixture(ctx, fixtures[0], fixtures[0].SubjectDir)
	if passed || err == nil {
		t.Fatalf("cancelled grading must report infrastructure failure: pass=%v err=%v", passed, err)
	}
}
