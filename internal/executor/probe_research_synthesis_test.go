package executor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"tzro/internal/inference"
	"tzro/internal/memory"
)

type mockSynthesisCaptureEngine struct {
	capturedSystemPrompt string
	capturedUserPrompt   string
	capturedSchema       string
}

func (m *mockSynthesisCaptureEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, _ ModelTarget) (string, error) {
	m.capturedSystemPrompt = systemPrompt
	m.capturedUserPrompt = userPrompt
	m.capturedSchema = jsonSchema
	return "# Overview\n\nFindings\n\n## Details\n\n- Fact\n\n## Comparative Overview\n\n| Item | Val |\n| --- | --- |\n| A | 1 |\n\n## Sources & Citations\n\n- https://example.com\n", nil
}

func (m *mockSynthesisCaptureEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, _ ModelTarget) (string, error) {
	m.capturedSchema = jsonSchema
	return m.Infer(ctx, "", "", jsonSchema, TargetWorker)
}

func TestProbeSynthesis_ResearchMarkdownGrammar(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_probe_synth_grammar.db")
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting(dbPath)
	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init test db: %v", err)
	}
	defer func() {
		memory.DB.Close()
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	probeID := "task_research_synth_probe"
	taskID := "task_research_synth"

	// Add a web_search thought step so research evidence is present
	_ = memory.DB.AddThoughtStep(memory.ThoughtStep{
		ID:         "step_res_1",
		ProbeID:    probeID,
		StepIndex:  1,
		ToolName:   "web_search",
		ToolArgs:   `{"query": "top LLM frameworks 2025"}`,
		ToolOutput: `[{"title": "LangChain vs LlamaIndex", "url": "https://langchain.com", "snippet": "Framework comparison"}]`,
	})

	mockEngine := &mockSynthesisCaptureEngine{}
	ctx := context.Background()

	res, err := runSynthesisPass(
		ctx,
		probeID,
		taskID,
		"Compare LLM frameworks in 2025 and present comparison table and citations",
		"Task context with URL requirement",
		mockEngine,
		nil,
		[]EdgeEntry{{ToolName: "web_search", ToolArgs: "frameworks", ResultSnippet: "results"}},
		"",
		false, // not analyze
	)
	if err != nil {
		t.Fatalf("runSynthesisPass failed: %v", err)
	}

	if !strings.Contains(res, "Comparative Overview") {
		t.Errorf("expected synthesis output to contain Comparative Overview, got:\n%s", res)
	}

	// Verify that research evidence was injected into synthesis context
	if !strings.Contains(mockEngine.capturedUserPrompt, "https://langchain.com") {
		t.Errorf("expected user prompt to contain injected web search URL, got:\n%s", mockEngine.capturedUserPrompt)
	}

	// Verify that the raw Markdown GBNF grammar was passed as the schema
	if !strings.HasPrefix(strings.TrimSpace(mockEngine.capturedSchema), "root ::=") {
		t.Errorf("expected research GBNF grammar (root ::=), got:\n%s", mockEngine.capturedSchema)
	}
}
