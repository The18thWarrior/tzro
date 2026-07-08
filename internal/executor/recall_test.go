package executor

import (
	"context"
	"os"
	"strings"
	"testing"
	"tzro/internal/inference"
	"tzro/internal/memory"
)

type mockRecallEngine struct {
	Calls []string
}

func (m *mockRecallEngine) Infer(ctx context.Context, systemPrompt, lastResult, userPrompt string) (string, error) {
	m.Calls = append(m.Calls, systemPrompt+"\nLAST_RESULT: "+lastResult)

	if strings.Contains(systemPrompt, "Synthesis Engine") {
		if strings.Contains(systemPrompt, "Found API key in config.go") {
			return "Final synthesis with fact", nil
		}
		return "Final synthesis MISSING fact", nil
	} else if strings.Contains(systemPrompt, "Recall Node") {
		if strings.Contains(lastResult, "Manifest loaded.") {
			return `<ACTION>{"tool": "fetch_details", "arguments": {"node_id": "probe_1", "step_index": 1}}</ACTION>`, nil
		}
		if strings.Contains(lastResult, "Details for probe_1 Step 1") {
			return `<ACTION>{"tool": "update_refined_context", "arguments": {"fact": "Found API key in config.go"}}</ACTION>`, nil
		}
		return "<SYNTHESIZE_READY>", nil
	}

	return "Unexpected prompt", nil
}

func (m *mockRecallEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string) (string, error) {
	var sys, usr string
	for _, msg := range messages {
		if msg.Role == "system" {
			sys = msg.Content
		} else if msg.Role == "user" {
			usr = msg.Content
		}
	}
	return m.Infer(ctx, sys, usr, jsonSchema)
}

func TestRunRecall_RefinedContext(t *testing.T) {
	ctx := context.Background()
	mock := &mockRecallEngine{}

	// Setup temporary DB
	tempDB := "test_recall.db"
	_ = os.Remove(tempDB)
	defer os.Remove(tempDB)

	memory.DB.SetDBPathForTesting(tempDB)
	err := memory.DB.Init()
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}

	// Insert a dummy thought step — ProbeID must use the composite
	// format "taskID_nodeID" matching production probe storage.
	_ = memory.DB.AddThoughtStep(memory.ThoughtStep{
		TaskID:     "t1",
		ProbeID:    "t1_probe_1",
		StepIndex:  1,
		ToolName:   "read_file",
		ToolArgs:   `{"path": "config.go"}`,
		ToolOutput: "API_KEY=12345",
	})

	// We need an ExecutionEngine but only for its publisher.
	// We can use a nil publisher if RunRecall handles it.
	eng := &ExecutionEngine{}

	result, err := eng.RunRecall(ctx, "t1", "recall_1", []string{"probe_1"}, "Find the API key", mock)
	if err != nil {
		t.Fatalf("RunRecall failed: %v", err)
	}

	if result != "Final synthesis with fact" {
		t.Errorf("Expected synthesis with fact, got: %s", result)
	}
}
