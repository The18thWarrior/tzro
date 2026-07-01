package codegen

import (
	"strings"
	"testing"
)

func TestBuildRepairPrompt_IncludesCompilerErrors(t *testing.T) {
	prompt := BuildRepairPrompt(
		"package main\n\nfunc Add(a, b int) string { return a + b }",
		"cannot use a + b (value of type int) as string value in return statement",
		"Create an Add function",
		"go",
		500,
		"",
	)
	if !strings.Contains(prompt, "cannot use a + b") {
		t.Error("repair prompt should include compiler errors")
	}
}

func TestBuildRepairPrompt_IncludesOriginalCode(t *testing.T) {
	prompt := BuildRepairPrompt(
		"package main\n\nfunc Add(a, b int) string { return a + b }",
		"type mismatch",
		"Create an Add function",
		"go",
		500,
		"",
	)
	if !strings.Contains(prompt, "func Add") {
		t.Error("repair prompt should include the original generated code")
	}
}

func TestBuildRepairPrompt_IncludesLanguage(t *testing.T) {
	prompt := BuildRepairPrompt(
		"code here",
		"error here",
		"spec here",
		"typescript",
		500,
		"",
	)
	if !strings.Contains(prompt, "typescript") && !strings.Contains(prompt, "TypeScript") {
		t.Error("repair prompt should mention the target language")
	}
}

func TestBuildRepairPrompt_IncludesLineLimit(t *testing.T) {
	prompt := BuildRepairPrompt("code", "error", "spec", "go", 300, "")
	if !strings.Contains(prompt, "300") {
		t.Error("repair prompt should mention the line limit")
	}
}

func TestBuildRepairDAG_SingleNode(t *testing.T) {
	dag := BuildRepairDAG("repair-test-1", "original code", "type error", "spec", "go", 500, "")
	if len(dag.Nodes) != 1 {
		t.Fatalf("repair DAG should have 1 node, got %d", len(dag.Nodes))
	}
	if dag.Nodes[0].ID != "reason_code" {
		t.Errorf("node ID should be reason_code, got %s", dag.Nodes[0].ID)
	}
	if dag.Nodes[0].Type != "synthesis" {
		t.Errorf("node type should be synthesis, got %s", dag.Nodes[0].Type)
	}
	if !strings.Contains(dag.Nodes[0].Instructions, "type error") {
		t.Error("repair DAG instructions should contain the compiler errors")
	}
	if !strings.Contains(dag.Nodes[0].Instructions, "original code") {
		t.Error("repair DAG instructions should contain the original code")
	}
}

func TestClassifyCompilerError_ImportViolation(t *testing.T) {
	tests := []string{
		`./main.go:5:2: cannot find module providing package github.com/some/pkg`,
		`./main.go:3:8: could not import github.com/foo/bar (no required module)`,
		`no required module provides package github.com/example/lib`,
		`cannot find package "github.com/missing/dep" in any of: /usr/local/go/src/...`,
		`package github.com/test/pkg is not in GOROOT (/usr/local/go/src/...)`,
	}
	for _, errText := range tests {
		category, constraint := ClassifyCompilerError(errText)
		if category != "import_violation" {
			t.Errorf("ClassifyCompilerError(%q) category = %q, want %q", errText, category, "import_violation")
		}
		if !strings.Contains(constraint, "standard library") {
			t.Errorf("ClassifyCompilerError(%q) constraint should mention standard library, got %q", errText, constraint)
		}
	}
}

func TestClassifyCompilerError_UndefinedReference(t *testing.T) {
	tests := []string{
		`./main.go:10:5: undefined: SomeType`,
		`./main.go:15:10: u.Name is not defined`,
		`./main.go:20:3: t.Foo has no field or method Bar`,
		`./main.go:8:12: undeclared name: myFunc`,
	}
	for _, errText := range tests {
		category, constraint := ClassifyCompilerError(errText)
		if category != "undefined_reference" {
			t.Errorf("ClassifyCompilerError(%q) category = %q, want %q", errText, category, "undefined_reference")
		}
		if constraint == "" {
			t.Errorf("ClassifyCompilerError(%q) should return non-empty constraint", errText)
		}
	}
}

func TestClassifyCompilerError_TypeMismatch(t *testing.T) {
	tests := []string{
		`./main.go:12:15: cannot use x (variable of type string) as int value`,
		`./main.go:20:8: incompatible types: got int, want string`,
		`./main.go:5:10: cannot convert "hello" (untyped string constant) to int`,
	}
	for _, errText := range tests {
		category, constraint := ClassifyCompilerError(errText)
		if category != "type_mismatch" {
			t.Errorf("ClassifyCompilerError(%q) category = %q, want %q", errText, category, "type_mismatch")
		}
		if constraint == "" {
			t.Errorf("ClassifyCompilerError(%q) should return non-empty constraint", errText)
		}
	}
}

func TestClassifyCompilerError_SyntaxError(t *testing.T) {
	tests := []string{
		`./main.go:8:1: expected ';', found 'func'`,
		`./main.go:3:10: illegal character U+0023 '#'`,
		`./main.go:15:1: syntax error: unexpected end of input`,
	}
	for _, errText := range tests {
		category, constraint := ClassifyCompilerError(errText)
		if category != "syntax_error" {
			t.Errorf("ClassifyCompilerError(%q) category = %q, want %q", errText, category, "syntax_error")
		}
		if !strings.Contains(constraint, "syntax") {
			t.Errorf("ClassifyCompilerError(%q) constraint should mention syntax, got %q", errText, constraint)
		}
	}
}

func TestClassifyCompilerError_Unknown(t *testing.T) {
	category, constraint := ClassifyCompilerError("some completely unknown error format")
	if category != "" {
		t.Errorf("ClassifyCompilerError(unknown) category = %q, want empty", category)
	}
	if constraint != "" {
		t.Errorf("ClassifyCompilerError(unknown) constraint = %q, want empty", constraint)
	}
}

func TestClassifyCompilerError_EmptyInput(t *testing.T) {
	category, constraint := ClassifyCompilerError("")
	if category != "" || constraint != "" {
		t.Errorf("ClassifyCompilerError(\"\") = (%q, %q), want empty", category, constraint)
	}
}

func TestClassifyCompilerError_PriorityOrder(t *testing.T) {
	// When an error contains both import violation AND undefined reference
	// patterns, import violation should win (higher priority)
	mixed := `cannot find module providing package foo; undefined: Bar`
	category, _ := ClassifyCompilerError(mixed)
	if category != "import_violation" {
		t.Errorf("ClassifyCompilerError(mixed) = %q, want import_violation (higher priority)", category)
	}
}

func TestBuildRepairPrompt_IncludesErrorCategory(t *testing.T) {
	prompt := BuildRepairPrompt(
		"package main\nimport \"github.com/missing/pkg\"\n",
		"cannot find module providing package github.com/missing/pkg",
		"Build a simple server",
		"go",
		500,
		"",
	)

	if !strings.Contains(prompt, "## Error Category Analysis") {
		t.Error("BuildRepairPrompt should include Error Category Analysis section for classifiable errors")
	}
	if !strings.Contains(prompt, "import_violation") {
		t.Error("BuildRepairPrompt should include import_violation category")
	}
	if !strings.Contains(prompt, "standard library") {
		t.Error("BuildRepairPrompt should include constraint about standard library")
	}
}

func TestBuildRepairPrompt_NoErrorCategory_ForUnknownErrors(t *testing.T) {
	prompt := BuildRepairPrompt(
		"package main\n",
		"something went very wrong but not in any known pattern",
		"Build a simple server",
		"go",
		500,
		"",
	)

	if strings.Contains(prompt, "## Error Category Analysis") {
		t.Error("BuildRepairPrompt should NOT include Error Category Analysis for unknown error patterns")
	}
}
