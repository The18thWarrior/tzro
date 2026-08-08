package executor

import (
	"fmt"
	"strings"
	"testing"
)

// TestRepetitionThreshold_FalsePositive_StructuralMarkdown verifies that
// structural markdown repetition (e.g., repeated "## Section\n" patterns)
// does NOT trigger the false-positive repetition detector with 5-gram + scaled threshold.
func TestRepetitionThreshold_FalsePositive_StructuralMarkdown(t *testing.T) {
	// Build a document where section headers repeat structurally but content varies.
	// This represents a real probe output listing multiple Go files with similar
	// structure (package declaration, imports, function signatures).
	var sb strings.Builder
	sb.WriteString("# Architecture Overview\n\n")
	sections := []string{
		"The executor package coordinates task execution across DAG nodes.",
		"The planner module compiles prompts into topologically sorted layers.",
		"The inference subsystem manages routing between worker and router models.",
		"The memory store persists thought steps and edge entries to SQLite.",
		"The compactor implements rolling summarization for long thought chains.",
		"The MCP server exposes tools via JSON-RPC over stdio transport.",
		"The observer agent monitors execution quality and synthesizes insights.",
		"The knowledge graph stores entity relationships with semantic embeddings.",
	}
	closings := []string{
		"It exposes a public API through the handler interface.",
		"It provides structured output via JSON schema constraints.",
		"It supports both synchronous and streaming execution modes.",
		"It integrates with the SQLite persistence layer directly.",
		"It implements the ProbeInferenceEngine interface for routing.",
		"It uses GBNF grammar constraints for structured generation.",
		"It connects to upstream DAG nodes via binding keys.",
		"It supports configurable timeouts and retry backoff policies.",
	}
	for i, desc := range sections {
		sb.WriteString(fmt.Sprintf("## Module %d\n\n", i+1))
		fmt.Fprintf(&sb, "%s %s\n\n", desc, closings[i])
	}
	output := sb.String()

	// With 4-gram @ 3x, "It exposes a public API" would false-positive.
	// With 5-gram @ scaled threshold, only genuine degeneration is caught.
	reason := validateSynthesisOutput(output)
	if reason != "" {
		t.Errorf("structural markdown should NOT be flagged as repetitive, got: %s", reason)
	}
}

// TestRepetitionThreshold_TruePositive_DegenerateOutput verifies that
// genuinely degenerate repetition is still caught by the 5-gram detector.
func TestRepetitionThreshold_TruePositive_DegenerateOutput(t *testing.T) {
	// Build degenerate output: same exact sentence repeated many times
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString("I need to analyze the data more carefully. ")
	}
	output := sb.String()

	reason := validateSynthesisOutput(output)
	if reason == "" {
		t.Error("degenerate repetition should be detected")
	}
}
