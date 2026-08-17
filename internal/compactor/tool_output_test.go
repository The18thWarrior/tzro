package compactor

import (
	"context"
	"strings"
	"testing"
)

// --- Slice 1: CompactToolOutput on engine interface ---

func TestPassthroughEngine_CompactToolOutput_ReturnsUnchanged(t *testing.T) {
	engine := &PassthroughEngine{}
	input := "LangChain has 92K GitHub stars. CrewAI focuses on multi-agent workflows."

	result, err := engine.CompactToolOutput(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != input {
		t.Errorf("PassthroughEngine should return input unchanged, got %q", result)
	}
}

// --- Slice 2: CompactToolOutputs segmentation ---

// CountingEngine tracks how many LLM calls are made and what type.
type CountingEngine struct {
	ReasoningCalls  int
	ToolOutputCalls int
}

func (c *CountingEngine) CompactReasoning(_ context.Context, chunk string) (string, error) {
	c.ReasoningCalls++
	return chunk, nil
}

func (c *CountingEngine) CompactToolOutput(_ context.Context, content string) (string, error) {
	c.ToolOutputCalls++
	// Simulate 4:1 compression for text
	if len(content) > 200 {
		return content[:len(content)/4], nil
	}
	return content, nil
}

func (c *CountingEngine) ExtractWebFacts(_ context.Context, content string, _ string) (string, error) {
	c.ToolOutputCalls++
	// Simulate 4:1 compression for web content (structured fact extraction)
	if len(content) > 200 {
		return content[:len(content)/4], nil
	}
	return content, nil
}

func TestCompactToolOutputs_CodeGetsSkeletonNotLLM(t *testing.T) {
	engine := &CountingEngine{}
	steps := []ToolOutputStep{
		{
			StepIndex:  1,
			ToolName:   "read_file",
			ToolArgs:   `{"path":"main.go"}`,
			ToolOutput: "package main\n\nimport \"fmt\"\n\n// Greet prints a greeting.\nfunc Greet(name string) {\n\tmsg := fmt.Sprintf(\"Hello, %s!\", name)\n\tfmt.Println(msg)\n\tfor i := 0; i < 3; i++ {\n\t\tfmt.Println(i)\n\t}\n}\n\nfunc helper() int {\n\treturn 42\n}\n",
		},
	}

	result, err := CompactToolOutputs(context.Background(), steps, 0, engine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Code should get skeleton extraction (deterministic), not LLM
	if engine.ToolOutputCalls != 0 {
		t.Errorf("expected 0 LLM tool output calls for code, got %d", engine.ToolOutputCalls)
	}

	// Skeleton should preserve signatures
	if !strings.Contains(result.Output, "func Greet(name string)") {
		t.Error("expected Greet signature in skeleton output")
	}
	if !strings.Contains(result.Output, "func helper() int") {
		t.Error("expected helper signature in skeleton output")
	}
}

func TestCompactToolOutputs_TextGetsLLMSummarization(t *testing.T) {
	engine := &CountingEngine{}
	// Web browse output — long prose, no code indicators
	webContent := strings.Repeat("LangChain is an open-source framework for building LLM applications. It provides tools for chains, agents, and retrieval augmented generation. ", 50)
	steps := []ToolOutputStep{
		{
			StepIndex:  1,
			ToolName:   "web_browse",
			ToolArgs:   `{"url":"https://example.com"}`,
			ToolOutput: webContent,
		},
	}

	result, err := CompactToolOutputs(context.Background(), steps, 0, engine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Text should get LLM summarization
	if engine.ToolOutputCalls != 1 {
		t.Errorf("expected 1 LLM tool output call for text, got %d", engine.ToolOutputCalls)
	}

	// Output should be shorter than input (4:1 simulated compression)
	if len(result.Output) >= len(webContent) {
		t.Errorf("expected compressed output, got %d chars (input: %d)", len(result.Output), len(webContent))
	}
}

// --- Slice 3: Budget enforcement + failure cascade ---

func TestCompactToolOutputs_BudgetEnforced(t *testing.T) {
	engine := &CountingEngine{}
	// 5 steps × 15K chars each = 75K total
	bigText := strings.Repeat("This is a substantial article about AI trends and market analysis. ", 250) // ~15K chars
	var steps []ToolOutputStep
	for i := 0; i < 5; i++ {
		steps = append(steps, ToolOutputStep{
			StepIndex:  i + 1,
			ToolName:   "web_browse",
			ToolArgs:   `{"url":"https://example.com"}`,
			ToolOutput: bigText,
		})
	}

	budget := 32000
	result, err := CompactToolOutputs(context.Background(), steps, budget, engine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Output) > budget {
		t.Errorf("output %d chars exceeds budget %d", len(result.Output), budget)
	}
}

// FailingEngine simulates router failure
type FailingEngine struct{}

func (f *FailingEngine) CompactReasoning(_ context.Context, chunk string) (string, error) {
	return chunk, nil
}

func (f *FailingEngine) CompactToolOutput(_ context.Context, content string) (string, error) {
	return "", context.DeadlineExceeded
}

func (f *FailingEngine) ExtractWebFacts(_ context.Context, _ string, _ string) (string, error) {
	return "", context.DeadlineExceeded
}

func TestCompactToolOutputs_EngineFailureFallsBackToTruncation(t *testing.T) {
	engine := &FailingEngine{}
	bigText := strings.Repeat("This is important web content about AI frameworks. ", 300)
	steps := []ToolOutputStep{
		{
			StepIndex:  1,
			ToolName:   "web_browse",
			ToolArgs:   `{"url":"https://example.com"}`,
			ToolOutput: bigText,
		},
	}

	budget := 4000
	result, err := CompactToolOutputs(context.Background(), steps, budget, engine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must still fit budget despite engine failure
	if len(result.Output) > budget {
		t.Errorf("output %d chars exceeds budget %d after engine failure", len(result.Output), budget)
	}
}

// --- Slice 4: Analyze Node exemption ---

func TestCompactToolOutputs_AnalyzeToolsExempt(t *testing.T) {
	engine := &CountingEngine{}
	sqlOutput := `[{"name":"Alice","email":"alice@example.com"},{"name":"Bob","email":"bob@example.com"}]`
	introspectOutput := `Table: cache_123456\nColumns: name (TEXT), email (TEXT), score (REAL)\nRows: 150`

	steps := []ToolOutputStep{
		{
			StepIndex:  1,
			ToolName:   "sql_cached_data",
			ToolArgs:   `{"sql":"SELECT * FROM cache_123456 LIMIT 5"}`,
			ToolOutput: sqlOutput,
		},
		{
			StepIndex:  2,
			ToolName:   "introspect_cache",
			ToolArgs:   `{"cache_id":"cache_123456"}`,
			ToolOutput: introspectOutput,
		},
	}

	result, err := CompactToolOutputs(context.Background(), steps, 0, engine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No LLM calls — exempt tools are preserved at full fidelity
	if engine.ToolOutputCalls != 0 {
		t.Errorf("expected 0 LLM calls for exempt tools, got %d", engine.ToolOutputCalls)
	}

	// Full output preserved
	if !strings.Contains(result.Output, sqlOutput) {
		t.Error("sql_cached_data output should be preserved at full fidelity")
	}
	if !strings.Contains(result.Output, introspectOutput) {
		t.Error("introspect_cache output should be preserved at full fidelity")
	}
}

func TestCompactToolOutputs_TextWithGoalUsesHybridCompactor(t *testing.T) {
	doc := `## Section 1: Memory Indexing
Database memory indexing structures like B-Trees and LSM Trees allow high speed lookups for key value queries.

## Section 2: Regional Weather Trends
The forecast for tomorrow predicts sunny skies with mild westerly winds across the valley.

## Section 3: Cache Eviction Policies
LRU and LFU cache eviction algorithms determine which cache entries to discard when memory capacity is reached.

## Section 4: Baking Recipes
To bake sourdough bread, mix flour and water and allow fermentation for 24 hours at room temperature.`

	steps := []ToolOutputStep{
		{
			StepIndex:  1,
			ToolName:   "read_file",
			ToolArgs:   `{"path":"notes.txt"}`,
			ToolOutput: doc,
		},
	}

	ctx := context.WithValue(context.Background(), CompactorGoalKey, "cache eviction and memory indexing")
	result, err := CompactToolOutputs(ctx, steps, 400, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.LLMCalls != 0 {
		t.Errorf("expected 0 LLM calls for deterministic hybrid compaction, got %d", result.LLMCalls)
	}

	// Should contain Section 1 (Indexing) and Section 3 (Cache Eviction)
	if !strings.Contains(result.Output, "Database Memory Indexing") && !strings.Contains(result.Output, "indexing") {
		t.Errorf("expected output to contain Section 1 (Indexing), got:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "Cache Eviction Policies") && !strings.Contains(result.Output, "eviction") {
		t.Errorf("expected output to contain Section 3 (Cache Eviction), got:\n%s", result.Output)
	}

	// Should omit Section 2 (Weather) or Section 4 (Baking)
	if strings.Contains(result.Output, "Regional Weather Trends") {
		t.Errorf("expected output to omit Section 2 (Weather)")
	}
	if strings.Contains(result.Output, "Baking Recipes") {
		t.Errorf("expected output to omit Section 4 (Baking)")
	}
}
