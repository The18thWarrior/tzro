package compactor

import (
	"context"
	"strings"
	"testing"
)

func TestCompactContent_Code(t *testing.T) {
	code := `package main

import "fmt"

// Greet prints a greeting.
func Greet(name string) {
	msg := fmt.Sprintf("Hello, %s!", name)
	fmt.Println(msg)
	for i := 0; i < 3; i++ {
		fmt.Println(i)
	}
}

func helper() int {
	return 42
}
`
	result := CompactContent(code, 0)

	// Must preserve signatures
	if !strings.Contains(result, "func Greet(name string)") {
		t.Error("expected Greet signature preserved")
	}
	if !strings.Contains(result, "func helper() int") {
		t.Error("expected helper signature preserved")
	}
	// Must be shorter
	if len(result) >= len(code) {
		t.Errorf("expected compacted to be shorter: %d >= %d", len(result), len(code))
	}
}

func TestCompactContent_Text(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "This is a line of prose describing the architecture.")
	}
	text := strings.Join(lines, "\n")

	result := CompactContent(text, 2000)
	if len(result) > 2000 {
		t.Errorf("expected within budget, got %d chars", len(result))
	}
}

func TestCompactContent_WithinBudget(t *testing.T) {
	short := "Hello world, this is a short text."
	result := CompactContent(short, 1000)
	if result != short {
		t.Errorf("expected unchanged for short content, got %q", result)
	}
}

func TestCompactSteps_DeterministicOnly(t *testing.T) {
	steps := []Step{
		{
			Index:      1,
			Thought:    "I should look at the main package to understand the entry point.",
			ToolName:   "read_file",
			ToolArgs:   "main.go",
			ToolOutput: "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n",
		},
		{
			Index:      2,
			Thought:    "The main function is simple. Let me check the config.",
			ToolName:   "read_file",
			ToolArgs:   "config.go",
			ToolOutput: "package main\n\ntype Config struct {\n\tPort int\n\tHost string\n}\n",
		},
	}

	// nil engine — deterministic only
	result, err := CompactSteps(context.Background(), steps, "Explore the codebase", 0, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.LLMCalls != 0 {
		t.Errorf("expected 0 LLM calls with nil engine, got %d", result.LLMCalls)
	}

	// Must preserve step structure
	if !strings.Contains(result.Output, "Step 1:") {
		t.Error("expected Step 1 in output")
	}
	if !strings.Contains(result.Output, "Step 2:") {
		t.Error("expected Step 2 in output")
	}

	// Tool outputs should be compacted
	if !strings.Contains(result.Output, "func main()") {
		t.Error("expected main signature preserved in tool output")
	}
}

func TestCompactSteps_WithEngine(t *testing.T) {
	steps := []Step{
		{
			Index:      1,
			Thought:    "I need to explore the directory structure. Let me start by listing the top-level files and directories to understand the project layout. This will help me determine which packages to investigate further.",
			ToolName:   "list_dir",
			ToolArgs:   "/project",
			ToolOutput: "cmd/\ninternal/\ngo.mod\nREADME.md",
		},
	}

	engine := &PassthroughEngine{}
	result, err := CompactSteps(context.Background(), steps, "Explore the codebase", 0, engine, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// PassthroughEngine doesn't actually compress, so LLM calls should be 0
	// (the thought is short enough that it won't be chunked)
	if !strings.Contains(result.Output, "Step 1:") {
		t.Error("expected Step 1 in output")
	}
}

func TestCompactSteps_BudgetTriage(t *testing.T) {
	// Create steps with large tool outputs
	var steps []Step
	for i := 0; i < 10; i++ {
		output := strings.Repeat("x", 500) // 500 chars per step
		steps = append(steps, Step{
			Index:      i + 1,
			Thought:    "Exploring step",
			ToolName:   "read_file",
			ToolArgs:   "file.go",
			ToolOutput: output,
		})
	}

	// Budget that allows recent steps but requires dropping older ones.
	// 10 steps × ~530 chars = ~5300 total. Budget 2500 should trigger triage
	// and preserve the last 3 steps.
	result, err := CompactSteps(context.Background(), steps, "goal", 2500, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Most recent steps should still have content
	if !strings.Contains(result.Output, "Step 10:") {
		t.Error("expected most recent step preserved")
	}

	// Should be within budget
	if len(result.Output) > 2500 {
		t.Errorf("expected within budget, got %d chars", len(result.Output))
	}
}

func TestCompactFacts_Simple(t *testing.T) {
	facts := `- The cache package has 16 exported functions
- PruneColumns removes irrelevant TSV columns
- Process runs the full 5-layer compaction pipeline
- The main entry point is in cmd/tzro/main.go`

	result, err := CompactFacts(context.Background(), facts, "document the cache package", 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All facts should be preserved (no budget pressure)
	if !strings.Contains(result.Output, "PruneColumns") {
		t.Error("expected PruneColumns fact preserved")
	}
}

func TestCompactFacts_WithBudget(t *testing.T) {
	var factLines []string
	for i := 0; i < 50; i++ {
		factLines = append(factLines, "- This is a fact about the system that is somewhat verbose and descriptive")
	}
	facts := strings.Join(factLines, "\n")

	result, err := CompactFacts(context.Background(), facts, "goal", 500, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Output) > 500 {
		t.Errorf("expected within budget, got %d chars", len(result.Output))
	}
}

func TestChunkBySentence(t *testing.T) {
	text := "First sentence. Second sentence. Third sentence. Fourth sentence. Fifth sentence."
	chunks := chunkBySentence(text, 40)

	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks, got %d", len(chunks))
	}

	// Reassembled should contain all sentences
	joined := strings.Join(chunks, "")
	if !strings.Contains(joined, "First") || !strings.Contains(joined, "Fifth") {
		t.Error("expected all sentences in chunks")
	}
}

func TestChunkBySentence_ShortText(t *testing.T) {
	text := "Short text."
	chunks := chunkBySentence(text, 500)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk for short text, got %d", len(chunks))
	}
	if chunks[0] != text {
		t.Errorf("expected unchanged, got %q", chunks[0])
	}
}

func TestCompactSteps_PreserveToolOutput(t *testing.T) {
	// When preserveToolOutput=true, tool outputs should pass through verbatim
	// (no skeleton extraction, no tabular truncation, no middle-out truncation).
	// Reasoning text should still be compacted if an engine is provided.
	largeCode := "package main\n\nimport \"fmt\"\n\nfunc Greet(name string) {\n\tfmt.Println(name)\n\tfmt.Println(name)\n\tfmt.Println(name)\n}\n"
	steps := []Step{
		{
			Index:      1,
			Thought:    "Looking at the main package.",
			ToolName:   "sql_cached_data",
			ToolArgs:   `{"cacheId":"cache_123","sql":"SELECT * FROM cache_123"}`,
			ToolOutput: largeCode,
		},
	}

	// With preserveToolOutput=false, code output should be skeletonized
	resultCompacted, err := CompactSteps(context.Background(), steps, "goal", 0, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With preserveToolOutput=true, code output should be verbatim
	resultPreserved, err := CompactSteps(context.Background(), steps, "goal", 0, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Preserved output should contain the full function body
	if !strings.Contains(resultPreserved.Output, "fmt.Println(name)") {
		t.Error("expected full function body in preserved output")
	}

	// Preserved output should be >= compacted output (no data removed)
	if resultPreserved.OutputChars < resultCompacted.OutputChars {
		t.Errorf("expected preserved output (%d) >= compacted output (%d)",
			resultPreserved.OutputChars, resultCompacted.OutputChars)
	}
}
