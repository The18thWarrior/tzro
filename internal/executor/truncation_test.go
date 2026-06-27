package executor

import (
	"strings"
	"testing"
)

func TestClassifyContent_Code(t *testing.T) {
	code := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}

type Foo struct {
	Name string
}

// Bar does something important.
func (f *Foo) Bar() string {
	return f.Name
}
`
	if ct := classifyContent(code); ct != ContentCode {
		t.Errorf("expected ContentCode, got %d", ct)
	}
}

func TestClassifyContent_Tabular(t *testing.T) {
	csv := "name,age,email\nAlice,30,alice@example.com\nBob,25,bob@example.com\nCharlie,35,charlie@example.com\n"
	if ct := classifyContent(csv); ct != ContentTabular {
		t.Errorf("expected ContentTabular, got %d", ct)
	}
}

func TestClassifyContent_Text(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog.\nThis is a paragraph of prose that doesn't look like code or tabular data.\n"
	if ct := classifyContent(text); ct != ContentText {
		t.Errorf("expected ContentText, got %d", ct)
	}
}

func TestTruncateToolOutput_CodePreservesSignatures(t *testing.T) {
	// Build a code string with nested functions
	var sb strings.Builder
	sb.WriteString("package main\n\n")
	sb.WriteString("import \"fmt\"\n\n")
	sb.WriteString("// Add adds two numbers.\n")
	sb.WriteString("func Add(a, b int) int {\n")
	for i := 0; i < 50; i++ {
		sb.WriteString("\tx := a + b\n")
	}
	sb.WriteString("\treturn x\n}\n\n")
	sb.WriteString("// Multiply multiplies two numbers.\n")
	sb.WriteString("func Multiply(a, b int) int {\n")
	for i := 0; i < 50; i++ {
		sb.WriteString("\ty := a * b\n")
	}
	sb.WriteString("\treturn y\n}\n")

	content := sb.String()
	truncated := TruncateToolOutput(content, 500)

	// Function signatures should be preserved
	if !strings.Contains(truncated, "func Add") {
		t.Errorf("expected 'func Add' to be preserved in truncated output")
	}
	if !strings.Contains(truncated, "func Multiply") {
		t.Errorf("expected 'func Multiply' to be preserved in truncated output")
	}
	if !strings.Contains(truncated, "package main") {
		t.Errorf("expected 'package main' to be preserved in truncated output")
	}
}

func TestTruncateToolOutput_TabularKeepsSamples(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("name,age,city\n")
	for i := 0; i < 100; i++ {
		sb.WriteString("Alice,30,NYC\n")
	}
	content := sb.String()

	truncated := TruncateToolOutput(content, 500)

	// Should keep header and sample rows
	if !strings.Contains(truncated, "name,age,city") {
		t.Errorf("expected header to be preserved")
	}
	if !strings.Contains(truncated, "Alice,30,NYC") {
		t.Errorf("expected sample rows to be preserved")
	}
	if !strings.Contains(truncated, "more rows omitted") {
		t.Errorf("expected truncation marker")
	}
}

func TestTruncateToolOutput_TextMiddleOut(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("This is line number some very long text that needs truncation.\n")
	}
	content := sb.String()

	truncated := TruncateToolOutput(content, 3000)

	if !strings.Contains(truncated, "lines omitted") {
		t.Errorf("expected middle-out marker")
	}
	// Should keep head lines
	lines := strings.Split(truncated, "\n")
	if len(lines) < 10 {
		t.Errorf("expected at least 10 lines after truncation, got %d", len(lines))
	}
}

func TestTruncateToolOutput_NoTruncationNeeded(t *testing.T) {
	content := "short content"
	truncated := TruncateToolOutput(content, 1000)
	if truncated != content {
		t.Errorf("expected no truncation for short content")
	}
}

func TestTruncateSynthesisContext_WithinBudget(t *testing.T) {
	steps := []SynthesisStep{
		{StepIndex: 1, Thought: "Looking at project structure", ToolOutput: "dir1/ dir2/ main.go"},
		{StepIndex: 2, Thought: "Reading main.go", ToolOutput: "package main\nfunc main() {}"},
	}

	result := TruncateSynthesisContext(steps)

	if !strings.Contains(result, "Step 1:") {
		t.Errorf("expected Step 1 in output")
	}
	if !strings.Contains(result, "Step 2:") {
		t.Errorf("expected Step 2 in output")
	}
	if !strings.Contains(result, "package main") {
		t.Errorf("expected tool output to be included")
	}
}

func TestTruncateSynthesisContext_OldestTruncatedFirst(t *testing.T) {
	// Create steps where total exceeds budget
	bigOutput := strings.Repeat("x", maxSynthesisContextChars/2)
	steps := []SynthesisStep{
		{StepIndex: 1, Thought: "Old step", ToolOutput: bigOutput},
		{StepIndex: 2, Thought: "Recent step", ToolOutput: bigOutput},
	}

	result := TruncateSynthesisContext(steps)

	// Both steps should be present, but step 1's output should be truncated more
	if !strings.Contains(result, "Step 1:") {
		t.Errorf("expected Step 1 header to be present")
	}
	if !strings.Contains(result, "Step 2:") {
		t.Errorf("expected Step 2 header to be present")
	}

	// Step 2 (most recent) should have more content preserved than step 1
	step1Idx := strings.Index(result, "Step 1:")
	step2Idx := strings.Index(result, "Step 2:")
	step1Content := result[step1Idx:step2Idx]
	step2Content := result[step2Idx:]

	if len(step2Content) < len(step1Content) {
		t.Errorf("expected step 2 (recent) to have more content than step 1 (old)")
	}
}
