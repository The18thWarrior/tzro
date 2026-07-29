package codegen

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// mockEditLoopEngine is a test double that returns canned responses.
// It records calls for assertion and returns responses from a queue.
type mockEditLoopEngine struct {
	calls     []mockCall
	responses []string
	callIndex int
}

type mockCall struct {
	systemPrompt string
	userPrompt   string
	jsonSchema   string
}

func (m *mockEditLoopEngine) Infer(_ context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error) {
	m.calls = append(m.calls, mockCall{
		systemPrompt: systemPrompt,
		userPrompt:   userPrompt,
		jsonSchema:   jsonSchema,
	})
	if m.callIndex >= len(m.responses) {
		return "", fmt.Errorf("mock: no more responses (call %d)", m.callIndex)
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

// --- Slice 1: Plan step produces a prose plan from spec + file ---

func TestEditLoop_PlanStepProducesPlan(t *testing.T) {
	// The plan step is step 0 — unconstrained prose output.
	// After the plan, the engine signals done immediately (single hunk with done=true).
	planResponse := "1. Add a new function Bar()\n2. Update the import block"
	hunkResponse := mustJSON(t, EditHunkStep{
		SearchContent:  "func Hello() {}",
		ReplaceContent: "func Hello() {}\n\nfunc Bar() {}",
		Done:           true,
	})

	engine := &mockEditLoopEngine{
		responses: []string{planResponse, hunkResponse},
	}

	existing := "package foo\n\nfunc Hello() {}\n"
	result, err := RunEditLoop(context.Background(), engine,
		"Add a Bar() function",
		"/tmp/foo.go",
		existing,
		"go",
		nil, // no siblings
		"",  // no module context
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Plan step should be the first call (no JSON schema constraint)
	if len(engine.calls) < 1 {
		t.Fatal("expected at least 1 engine call for plan step")
	}
	planCall := engine.calls[0]
	if planCall.jsonSchema != "" {
		t.Errorf("plan step should have no JSON schema constraint, got: %s", planCall.jsonSchema)
	}
	if !strings.Contains(planCall.userPrompt, "Add a Bar() function") {
		t.Error("plan step user prompt should contain the spec")
	}
	if !strings.Contains(planCall.userPrompt, "func Hello()") {
		t.Error("plan step user prompt should contain the existing file content")
	}

	// Result should contain the new function
	if !strings.Contains(result, "func Bar()") {
		t.Errorf("expected result to contain Bar(), got:\n%s", result)
	}
	// Original should be preserved
	if !strings.Contains(result, "func Hello()") {
		t.Errorf("expected result to preserve Hello(), got:\n%s", result)
	}
}

// --- Slice 2: Single hunk applies correctly ---

func TestEditLoop_SingleHunkAppliesCorrectly(t *testing.T) {
	planResponse := "1. Change greeting from hello to world"
	hunkResponse := mustJSON(t, EditHunkStep{
		SearchContent:  `return "hello"`,
		ReplaceContent: `return "world"`,
		Done:           true,
	})

	engine := &mockEditLoopEngine{
		responses: []string{planResponse, hunkResponse},
	}

	existing := "package foo\n\nfunc Hello() string {\n\treturn \"hello\"\n}\n"
	result, err := RunEditLoop(context.Background(), engine,
		"Change greeting to world", "/tmp/foo.go", existing, "go", nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, `return "world"`) {
		t.Errorf("expected hunk to be applied, got:\n%s", result)
	}
	if strings.Contains(result, `return "hello"`) {
		t.Error("original content should be replaced")
	}

	// Hunk step should have JSON schema constraint
	hunkCall := engine.calls[1]
	if hunkCall.jsonSchema == "" {
		t.Error("hunk step should have JSON schema constraint")
	}
}

// --- Slice 3: Multi-hunk sequence patches file cumulatively ---

func TestEditLoop_MultiHunkSequence(t *testing.T) {
	planResponse := "1. Change hello to hi\n2. Change goodbye to bye\n3. Change main to start"

	existing := `package foo

func Hello() string {
	return "hello"
}

func Goodbye() string {
	return "goodbye"
}

func Main() {
	fmt.Println("main")
}
`
	hunk1 := mustJSON(t, EditHunkStep{
		SearchContent:  `return "hello"`,
		ReplaceContent: `return "hi"`,
		Done:           false,
	})
	hunk2 := mustJSON(t, EditHunkStep{
		SearchContent:  `return "goodbye"`,
		ReplaceContent: `return "bye"`,
		Done:           false,
	})
	hunk3 := mustJSON(t, EditHunkStep{
		SearchContent:  `fmt.Println("main")`,
		ReplaceContent: `fmt.Println("start")`,
		Done:           true,
	})

	engine := &mockEditLoopEngine{
		responses: []string{planResponse, hunk1, hunk2, hunk3},
	}

	result, err := RunEditLoop(context.Background(), engine,
		"Rename strings", "/tmp/foo.go", existing, "go", nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All three changes applied
	if !strings.Contains(result, `return "hi"`) {
		t.Error("first hunk not applied")
	}
	if !strings.Contains(result, `return "bye"`) {
		t.Error("second hunk not applied")
	}
	if !strings.Contains(result, `fmt.Println("start")`) {
		t.Error("third hunk not applied")
	}

	// Originals gone
	if strings.Contains(result, `"hello"`) {
		t.Error("original 'hello' should be replaced")
	}
	if strings.Contains(result, `"goodbye"`) {
		t.Error("original 'goodbye' should be replaced")
	}

	// 4 total calls: 1 plan + 3 hunks
	if len(engine.calls) != 4 {
		t.Errorf("expected 4 engine calls (1 plan + 3 hunks), got %d", len(engine.calls))
	}
}

// --- Slice 4: done=true breaks loop early ---

func TestEditLoop_DoneBreaksEarly(t *testing.T) {
	planResponse := "1. Change A\n2. Change B\n3. Change C"

	existing := "package foo\n\nfunc A() {}\nfunc B() {}\nfunc C() {}\n"

	// Only the first hunk sets done=true — loop should stop after 1 hunk
	hunk1 := mustJSON(t, EditHunkStep{
		SearchContent:  "func A() {}",
		ReplaceContent: "func A() { /* modified */ }",
		Done:           true,
	})

	engine := &mockEditLoopEngine{
		responses: []string{planResponse, hunk1},
	}

	result, err := RunEditLoop(context.Background(), engine,
		"Modify functions", "/tmp/foo.go", existing, "go", nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only A was modified
	if !strings.Contains(result, "/* modified */") {
		t.Error("first hunk should be applied")
	}

	// 2 calls total: 1 plan + 1 hunk (stopped early)
	if len(engine.calls) != 2 {
		t.Errorf("expected 2 engine calls (done=true should stop loop), got %d", len(engine.calls))
	}
}

// --- Slice 5: Budget exhaustion at step 15 ---

func TestEditLoop_BudgetExhaustion(t *testing.T) {
	planResponse := "Many changes needed"
	existing := "package foo\n\nvar x = 1\n"

	// Build 15 hunks that never set done=true, plus 1 plan response
	var responses []string
	responses = append(responses, planResponse)

	// Each step just appends a comment — never done
	for i := 0; i < 20; i++ { // provide more responses than maxEditSteps
		search := fmt.Sprintf("var x = %d", i+1)
		replace := fmt.Sprintf("var x = %d", i+2)
		responses = append(responses, mustJSON(t, EditHunkStep{
			SearchContent:  search,
			ReplaceContent: replace,
			Done:           false,
		}))
	}

	engine := &mockEditLoopEngine{responses: responses}

	result, err := RunEditLoop(context.Background(), engine,
		"Increment x many times", "/tmp/foo.go", existing, "go", nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have stopped at maxEditSteps (15 hunk steps + 1 plan = 16 calls)
	expectedCalls := 1 + maxEditSteps // plan + max hunk steps
	if len(engine.calls) != expectedCalls {
		t.Errorf("expected %d engine calls (budget guard), got %d", expectedCalls, len(engine.calls))
	}

	// Result should still be valid (partial application)
	if result == "" {
		t.Error("result should not be empty even with budget exhaustion")
	}
}

// --- Slice 6: Hunk application failure returns error ---

func TestEditLoop_HunkFailureReturnsError(t *testing.T) {
	planResponse := "1. Replace nonexistent text"
	hunkResponse := mustJSON(t, EditHunkStep{
		SearchContent:  "this text does not exist anywhere in the file",
		ReplaceContent: "replacement",
		Done:           true,
	})

	engine := &mockEditLoopEngine{
		responses: []string{planResponse, hunkResponse},
	}

	existing := "package foo\n\nfunc Hello() {}\n"
	_, err := RunEditLoop(context.Background(), engine,
		"Replace nonexistent", "/tmp/foo.go", existing, "go", nil, "")

	if err == nil {
		t.Fatal("expected error for failed hunk application")
	}
	if !strings.Contains(err.Error(), "hunk application failed") {
		t.Errorf("expected 'hunk application failed' in error, got: %v", err)
	}
}

// --- Slice 6b: Empty file returns error ---

func TestEditLoop_EmptyFileReturnsError(t *testing.T) {
	engine := &mockEditLoopEngine{}

	_, err := RunEditLoop(context.Background(), engine,
		"Some spec", "/tmp/foo.go", "", "go", nil, "")

	if err == nil {
		t.Fatal("expected error for empty file")
	}
	if !strings.Contains(err.Error(), "existing file content") {
		t.Errorf("expected 'existing file content' in error, got: %v", err)
	}
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustJSON: %v", err)
	}
	return string(b)
}
