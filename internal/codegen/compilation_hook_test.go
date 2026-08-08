package codegen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/internal/compiler"
	"tzro/internal/executor"
	"tzro/internal/memory"
)

func TestCompilationGateHook_SourceCodeNode_Pass(t *testing.T) {
	// Setup: valid Go file with go.mod
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")

	// Create go.mod so "go build" finds a module root
	goMod := "module testmod\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	hook := &CompilationGateHook{
		FilePath: goFile,
		Language: "go",
	}

	// Valid Go code
	rawOutput := "package main\n\nfunc main() {}\n"
	node := &compiler.GraphNode{
		ID:           "reason_code",
		OutputFormat: "source_code",
	}

	action, err := hook.AfterNode(context.Background(), "test-task", node, &rawOutput)
	if err != nil {
		t.Fatalf("AfterNode error: %v", err)
	}
	if action != executor.ActionContinue {
		t.Errorf("expected ActionContinue, got %v", action)
	}

	// Output should contain compilation result
	if !strings.Contains(rawOutput, "## Compilation Result") {
		t.Error("output should contain compilation result section")
	}
	if !strings.Contains(rawOutput, "PASSED") {
		t.Errorf("expected PASSED in output, got: %s", rawOutput)
	}

	// File should have been written
	content, err := os.ReadFile(goFile)
	if err != nil {
		t.Fatalf("file should exist: %v", err)
	}
	if !strings.Contains(string(content), "func main()") {
		t.Error("written file should contain the generated code")
	}
}

func TestCompilationGateHook_SourceCodeNode_Fail(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")

	// Create go.mod so "go build" doesn't complain about missing module
	goMod := "module testmod\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	hook := &CompilationGateHook{
		FilePath: goFile,
		Language: "go",
	}

	// Invalid Go code
	rawOutput := "package main\n\nfunc main() { undeclaredVar }\n"
	node := &compiler.GraphNode{
		ID:           "reason_code",
		OutputFormat: "source_code",
	}

	action, err := hook.AfterNode(context.Background(), "test-task", node, &rawOutput)
	if err != nil {
		t.Fatalf("AfterNode error: %v", err)
	}
	if action != executor.ActionContinue {
		t.Errorf("expected ActionContinue even on failure, got %v", action)
	}

	if !strings.Contains(rawOutput, "FAILED") {
		t.Errorf("expected FAILED in output, got: %s", rawOutput)
	}
	if !strings.Contains(rawOutput, "## Compilation Result") {
		t.Error("output should contain compilation result section")
	}
}

func TestCompilationGateHook_NonSourceCodeNode_Skipped(t *testing.T) {
	hook := &CompilationGateHook{
		FilePath: "/tmp/noop.go",
		Language: "go",
	}

	rawOutput := "some non-code output"
	node := &compiler.GraphNode{
		ID:           "explore_context",
		OutputFormat: "", // Not source_code
	}

	action, err := hook.AfterNode(context.Background(), "test-task", node, &rawOutput)
	if err != nil {
		t.Fatalf("AfterNode error: %v", err)
	}
	if action != executor.ActionContinue {
		t.Errorf("expected ActionContinue, got %v", action)
	}

	// Output should NOT be modified
	if rawOutput != "some non-code output" {
		t.Errorf("non-source_code nodes should not be modified, got: %s", rawOutput)
	}
}

func TestCompilationGateHook_OnEdgeTraversal_CompilationFailed(t *testing.T) {
	hook := &CompilationGateHook{
		FilePath: "/tmp/noop.go",
		Language: "go",
		Spec:     "Create a main function that prints hello world",
	}

	// Store a node state with compilation failure evidence
	initTestDB(t)

	taskID := "test-edge-fail"
	failOutput := "package main\n\nfunc main() {}\n\n## Compilation Result\nFAILED\nmain.go:3: syntax error"
	_ = memory.DB.SetNodeState(taskID, "reason_code", "completed", failOutput)
	_ = memory.DB.SetNodeRawOutput(taskID, "reason_code", failOutput)

	sourceNode := &compiler.GraphNode{
		ID:           "reason_code",
		OutputFormat: "source_code",
	}
	targetNode := &compiler.GraphNode{
		ID:                  "validate_code",
		ActivationThreshold: 0.9,
	}

	et := &memory.EdgeThought{
		GoalConfidence: 0.95, // LM says high confidence
		GoalAchieved:   true, // LM says goal achieved
	}

	action, err := hook.OnEdgeTraversal(context.Background(), taskID, sourceNode, targetNode, et)
	if err != nil {
		t.Fatalf("OnEdgeTraversal error: %v", err)
	}
	if action != executor.ActionContinue {
		t.Errorf("expected ActionContinue, got %v", action)
	}

	// Confidence should be overridden to 0.0
	if et.GoalConfidence != 0.0 {
		t.Errorf("compilation failure should force GoalConfidence=0.0, got %.2f", et.GoalConfidence)
	}
	if et.GoalAchieved {
		t.Error("compilation failure should force GoalAchieved=false")
	}

	// Thought should contain a structured repair prompt with compiler errors
	if et.Thought == "" {
		t.Error("compilation failure should inject a repair prompt into et.Thought")
	}
	if !strings.Contains(et.Thought, "Compilation Errors") {
		t.Error("repair prompt should contain 'Compilation Errors' section")
	}
	if !strings.Contains(et.Thought, "main.go:3: syntax error") {
		t.Error("repair prompt should contain the actual compiler error")
	}
	if !strings.Contains(et.Thought, "package main") {
		t.Error("repair prompt should contain the original code")
	}
}

func TestCompilationGateHook_OnEdgeTraversal_CompilationPassed(t *testing.T) {
	hook := &CompilationGateHook{
		FilePath: "/tmp/noop.go",
		Language: "go",
	}

	initTestDB(t)

	taskID := "test-edge-pass"
	passOutput := "package main\n\nfunc main() {}\n\n## Compilation Result\nPASSED\n"
	_ = memory.DB.SetNodeState(taskID, "reason_code", "completed", passOutput)
	_ = memory.DB.SetNodeRawOutput(taskID, "reason_code", passOutput)

	sourceNode := &compiler.GraphNode{
		ID:           "reason_code",
		OutputFormat: "source_code",
	}
	targetNode := &compiler.GraphNode{
		ID:                  "validate_code",
		ActivationThreshold: 0.9,
	}

	et := &memory.EdgeThought{
		GoalConfidence: 0.85,
		GoalAchieved:   false,
	}

	action, err := hook.OnEdgeTraversal(context.Background(), taskID, sourceNode, targetNode, et)
	if err != nil {
		t.Fatalf("OnEdgeTraversal error: %v", err)
	}
	if action != executor.ActionContinue {
		t.Errorf("expected ActionContinue, got %v", action)
	}

	// Confidence should NOT be modified
	if et.GoalConfidence != 0.85 {
		t.Errorf("compilation pass should not modify GoalConfidence, got %.2f", et.GoalConfidence)
	}
}

// initTestDB initializes an isolated test database for hook tests.
func initTestDB(t *testing.T) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_hook.db")
	oldPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting(dbPath)
	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init test DB: %v", err)
	}
	t.Cleanup(func() {
		memory.DB.Close()
		memory.DB.SetDBPathForTesting(oldPath)
		_ = memory.DB.Init()
	})
}

// TestCompilationGateHook_CloudSemanticReview_RejectsBrokenLogic validates ADR-0070:
// T4+ codegen tasks get a cloud semantic review after compilation passes. When the
// review rejects the code, full cloud regeneration is triggered.
func TestCompilationGateHook_CloudSemanticReview_RejectsBrokenLogic(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "query_builder.go")

	goMod := "module testmod\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	var reviewCalled bool

	hook := &CompilationGateHook{
		FilePath:         goFile,
		Language:         "go",
		Spec:             "Create a query builder with Limit(n int) that renders 'LIMIT n' for any integer",
		AllowCloudRepair: true,
		TaskTier:         5,         // T5 — triggers cloud review
		AllowCloudReview: true,
		CloudReviewFunc: func(ctx context.Context, code, spec, language string) (bool, string, error) {
			reviewCalled = true
			// Simulate rejection: code has a logic error
			return false, "Limit fails for numbers > 9 — uses rune conversion instead of Itoa", nil
		},
	}

	// Valid Go code that compiles but has a subtle logic bug
	rawOutput := "package querybuilder\n\nimport \"strconv\"\n\nfunc Limit(n int) string {\n\treturn \"LIMIT \" + strconv.Itoa(n)\n}\n"
	node := &compiler.GraphNode{
		ID:           "reason_code",
		OutputFormat: "source_code",
	}

	action, err := hook.AfterNode(context.Background(), "test-semantic-review", node, &rawOutput)
	if err != nil {
		t.Fatalf("AfterNode error: %v", err)
	}
	if action != executor.ActionContinue {
		t.Errorf("expected ActionContinue, got %v", action)
	}

	if !reviewCalled {
		t.Error("cloud semantic review should have been called for T5 task")
	}

	// When review rejects and we can't actually regenerate (no real cloud model in test),
	// the output should still contain the compilation pass evidence
	if !strings.Contains(rawOutput, "PASSED") {
		t.Errorf("output should contain PASSED (compilation succeeded even if review rejected): %s", rawOutput)
	}
}

// TestCompilationGateHook_T3_SkipsCloudReview validates ADR-0070:
// T3 tasks should NOT trigger cloud review, even with AllowCloudReview=true.
func TestCompilationGateHook_T3_SkipsCloudReview(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "simple.go")

	goMod := "module testmod\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	var reviewCalled bool

	hook := &CompilationGateHook{
		FilePath:         goFile,
		Language:         "go",
		Spec:             "Create a simple hello world function",
		AllowCloudRepair: true,
		TaskTier:         3,    // T3 — should NOT trigger cloud review
		AllowCloudReview: true,
		CloudReviewFunc: func(ctx context.Context, code, spec, language string) (bool, string, error) {
			reviewCalled = true
			return true, "", nil
		},
	}

	rawOutput := "package main\n\nfunc Hello() string {\n\treturn \"hello world\"\n}\n"
	node := &compiler.GraphNode{
		ID:           "reason_code",
		OutputFormat: "source_code",
	}

	_, err := hook.AfterNode(context.Background(), "test-t3-skip", node, &rawOutput)
	if err != nil {
		t.Fatalf("AfterNode error: %v", err)
	}

	if reviewCalled {
		t.Error("cloud semantic review should NOT be called for T3 tasks")
	}
}

// TestCheckSymbolPreservation_DetectsRemovedSymbols validates that the
// preservation assertion catches when generated code removes existing
// public methods (FM-4).
func TestCheckSymbolPreservation_DetectsRemovedSymbols(t *testing.T) {
	originalCode := `package user

// User represents a system user.
type User struct {
	Name  string
	Email string
}

// NewUser creates a new User.
func NewUser(name, email string) *User {
	return &User{Name: name, Email: email}
}

// DisplayName returns the formatted display name.
func (u *User) DisplayName() string {
	return u.Name
}
`
	generatedCode := `package user

// User represents a system user.
type User struct {
	Name  string
	Email string
	Age   int
}

// Validate checks if the user is valid.
func (u *User) Validate() error {
	if u.Email == "" {
		return fmt.Errorf("email required")
	}
	return nil
}
`
	hook := &CompilationGateHook{
		FilePath:        "user.go",
		Language:        "go",
		OriginalContent: originalCode,
	}

	missing := hook.checkSymbolPreservation(generatedCode)

	if len(missing) == 0 {
		t.Fatal("Expected missing symbols, got none")
	}

	// Should detect NewUser and DisplayName as missing
	missingStr := strings.Join(missing, ", ")
	if !strings.Contains(missingStr, "NewUser") {
		t.Errorf("Expected 'NewUser' in missing list, got: %s", missingStr)
	}
	if !strings.Contains(missingStr, "DisplayName") {
		t.Errorf("Expected 'DisplayName' in missing list, got: %s", missingStr)
	}
}

// TestCheckSymbolPreservation_AllPreserved validates no false positives
// when all original symbols are preserved.
func TestCheckSymbolPreservation_AllPreserved(t *testing.T) {
	originalCode := `package user

type User struct {
	Name string
}

func NewUser(name string) *User {
	return &User{Name: name}
}
`
	generatedCode := `package user

import "fmt"

type User struct {
	Name  string
	Email string
}

func NewUser(name string) *User {
	return &User{Name: name}
}

func (u *User) Validate() error {
	if u.Name == "" {
		return fmt.Errorf("name required")
	}
	return nil
}
`
	hook := &CompilationGateHook{
		FilePath:        "user.go",
		Language:        "go",
		OriginalContent: originalCode,
	}

	missing := hook.checkSymbolPreservation(generatedCode)

	if len(missing) != 0 {
		t.Errorf("Expected no missing symbols, got: %v", missing)
	}
}
