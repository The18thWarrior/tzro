package main

import (
	"encoding/json"
	"testing"

	"tzro/internal/memory"
)

func TestExtractEnvelopeResult_FindsStructuredOutput(t *testing.T) {
	nodes := []memory.NodeState{
		{NodeID: "step1", Status: "completed", Output: "step output"},
		{NodeID: "terminal_synthesis", Status: "completed", Output: "synthesis text",
			StructuredOutput: `{"synthesis":"summary","toolsUsed":["read_file"],"nodeCount":3}`},
	}

	result := extractEnvelopeResult(nodes)
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	synthesis, ok := result["synthesis"].(string)
	if !ok || synthesis != "summary" {
		t.Errorf("expected synthesis=summary, got %v", result["synthesis"])
	}

	tools, ok := result["toolsUsed"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Errorf("expected 1 tool, got %v", result["toolsUsed"])
	}
}

func TestExtractEnvelopeResult_ReturnsNilWhenMissing(t *testing.T) {
	nodes := []memory.NodeState{
		{NodeID: "step1", Status: "completed", Output: "output"},
		{NodeID: "step2", Status: "completed", Output: "output"},
	}

	result := extractEnvelopeResult(nodes)
	if result != nil {
		t.Errorf("expected nil when no StructuredOutput, got %v", result)
	}
}

func TestExtractEnvelopeResult_HoistableInResponse(t *testing.T) {
	// Verify the extracted envelope can be hoisted into a response map
	nodes := []memory.NodeState{
		{NodeID: "terminal_synthesis", Status: "completed",
			StructuredOutput: `{"synthesis":"final text","toolsUsed":["web_search","read_file"],"filesRead":["/foo.go"],"filesModified":[],"nodeCount":2,"nodesCompleted":2,"nodesFailed":0,"nodesSkipped":0,"durationMs":5000,"goalPrompt":"test goal"}`},
	}

	result := extractEnvelopeResult(nodes)
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Build response as handleTzroRun would
	respMap := map[string]interface{}{
		"taskId": "test-task",
		"status": "completed",
		"nodes":  nodes,
		"result": result,
	}

	respBytes, err := json.Marshal(respMap)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	// Verify the full response has the result hoisted
	var parsed map[string]interface{}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if _, ok := parsed["result"]; !ok {
		t.Error("response missing 'result' key")
	}
	envelope, ok := parsed["result"].(map[string]interface{})
	if !ok {
		t.Fatal("result is not a map")
	}
	if envelope["synthesis"] != "final text" {
		t.Errorf("result.synthesis = %v, want 'final text'", envelope["synthesis"])
	}
}
