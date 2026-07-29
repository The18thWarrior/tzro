package executor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"tzro/internal/inference"
)

// mockMapReduceEngine records calls and returns canned responses for testing.
type mockMapReduceEngine struct {
	calls     []string // user prompts received
	responses []string
	callIndex int
}

func (m *mockMapReduceEngine) Infer(_ context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error) {
	m.calls = append(m.calls, userPrompt)
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

func (m *mockMapReduceEngine) InferMessages(_ context.Context, msgs []inference.InferenceMessage, jsonSchema string) (string, error) {
	userPrompt := ""
	for _, msg := range msgs {
		if msg.Role == "user" {
			userPrompt = msg.Content
		}
	}
	return m.Infer(context.Background(), "", userPrompt, jsonSchema)
}

// --- Slice 14: MapReduceSynthesis single-chunk passthrough ---

func TestMapReduceSynthesis_SingleChunkPassthrough(t *testing.T) {
	// Content fits in DirectSynthesis cap → should be 1 call
	content := "# ADR-001\nDecision: Use Go for backend.\n\n# ADR-002\nDecision: Use SQLite for storage.\n"
	goal := "Summarize all ADRs"

	engine := &mockMapReduceEngine{
		responses: []string{"Summary: Two ADRs covering Go backend and SQLite storage."},
	}

	result, err := MapReduceSynthesis(context.Background(), goal, content, engine, 200_000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be exactly 1 call (passthrough to DirectSynthesis)
	if len(engine.calls) != 1 {
		t.Errorf("expected 1 engine call (passthrough), got %d", len(engine.calls))
	}

	if !strings.Contains(result, "Two ADRs") {
		t.Errorf("expected synthesis result, got: %s", result)
	}
}

// --- Slice 15: MapReduceSynthesis multi-chunk split + reduce ---

func TestMapReduceSynthesis_MultiChunkSplitReduce(t *testing.T) {
	// Build content that clearly exceeds the cap
	// 3 "files" of ~150 chars each = ~450 total, cap=200 → 3 map calls + 1 reduce
	chunk1 := "# File 1\n" + strings.Repeat("Content line A.\n", 10)
	chunk2 := "# File 2\n" + strings.Repeat("Content line B.\n", 10)
	chunk3 := "# File 3\n" + strings.Repeat("Content line C.\n", 10)
	content := chunk1 + chunk2 + chunk3

	goal := "Summarize all content"
	smallCap := 200

	// Provide enough responses: generous upper bound (content/cap + 2 for reduce + safety)
	numChunks := (len(content) / smallCap) + 1
	var responses []string
	for i := 0; i < numChunks; i++ {
		responses = append(responses, fmt.Sprintf("Chunk %d summary", i+1))
	}
	responses = append(responses, "Final synthesis of all chunks")

	engine := &mockMapReduceEngine{responses: responses}

	result, err := MapReduceSynthesis(context.Background(), goal, content, engine, smallCap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have multiple calls: N map calls + 1 reduce call
	if len(engine.calls) < 3 {
		t.Errorf("expected at least 3 engine calls for map-reduce (2 map + 1 reduce), got %d", len(engine.calls))
	}

	// Last call should be the reduce phase
	if result == "" {
		t.Error("expected non-empty result from map-reduce")
	}
}
