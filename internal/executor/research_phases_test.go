package executor

import (
	"context"
	"testing"

	"tzro/internal/compiler"
)

func TestResearchPhases_DeterministicSearchAndBrowse(t *testing.T) {
	var dispatchedTools []string

	ctx := context.WithValue(context.Background(), ToolDispatcherKey, func(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
		dispatchedTools = append(dispatchedTools, toolName)
		if toolName == "web_search" {
			query := args["query"].(string)
			return "Search results for " + query + ":\n- [Doc 1](https://example.com/doc1)\n- [Doc 2](https://example.com/doc2)", nil
		}
		if toolName == "web_browse" {
			url := args["url"].(string)
			return "Content from " + url, nil
		}
		return "ok", nil
	})

	mockEngine := NewMockPhaseEngine()
	// Mock 1-shot query generation
	mockEngine.PhaseResponses["queries"] = []MockPhaseStep{
		{Reasoning: `["tzro durable execution", "local model orchestration"]`},
	}
	// Mock 1-shot synthesis
	mockEngine.PhaseResponses["synthesize"] = []MockPhaseStep{
		{Reasoning: "Final research synthesis report"},
	}

	config := compiler.ProbeConfig{
		Goal: "Research tzro durable execution and local model orchestration",
	}

	synthesis, err := RunResearchPhases(ctx, "task_res_1", "probe_res_1", config, mockEngine, mockEngine, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if synthesis == "" {
		t.Errorf("expected non-empty synthesis")
	}

	// Verify dispatched tools: 2 web_search calls + 2 web_browse calls
	var searchCalls, browseCalls int
	for _, tool := range dispatchedTools {
		if tool == "web_search" {
			searchCalls++
		}
		if tool == "web_browse" {
			browseCalls++
		}
	}

	if searchCalls < 2 {
		t.Errorf("expected at least 2 web_search calls, got %d (dispatched: %v)", searchCalls, dispatchedTools)
	}
	if browseCalls < 1 {
		t.Errorf("expected at least 1 web_browse call on discovered URLs, got %d (dispatched: %v)", browseCalls, dispatchedTools)
	}
}
