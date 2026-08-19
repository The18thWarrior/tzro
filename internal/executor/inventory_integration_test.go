package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/internal/compiler"
	"tzro/internal/inference"
)

type mockInventoryIntegrationEngine struct {
	mapCount int
}

func (m *mockInventoryIntegrationEngine) Infer(ctx context.Context, systemPrompt, userPrompt, gbnfSchema string, target ModelTarget) (string, error) {
	if strings.Contains(systemPrompt, "schema planner") {
		return `{"fields": [{"name": "id", "description": "ADR ID"}, {"name": "title", "description": "Title"}, {"name": "status", "description": "Status"}, {"name": "decision", "description": "Decision"}]}`, nil
	}
	if strings.Contains(systemPrompt, "file inventory extractor") {
		m.mapCount++
		// Return valid GBNF JSON for this file
		return fmt.Sprintf(`{"relevant": true, "id": "ADR-%04d", "title": "Decision %d", "status": "Accepted", "decision": "Implication %d"}`, m.mapCount, m.mapCount, m.mapCount), nil
	}
	if strings.Contains(systemPrompt, "synthesizing a complete") {
		// Mock synthesis returning all discovered ADRs from the userPrompt
		return fmt.Sprintf("Consolidated Decision Log:\n\n%s", userPrompt), nil
	}
	return "{}", nil
}

func (m *mockInventoryIntegrationEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error) {
	if len(messages) > 0 {
		return m.Infer(ctx, messages[0].Content, messages[len(messages)-1].Content, jsonSchema, target)
	}
	return "{}", nil
}

func TestInventoryProbe_EndToEnd_37ADRs(t *testing.T) {
	tmpDir := t.TempDir()
	for i := 1; i <= 37; i++ {
		path := filepath.Join(tmpDir, fmt.Sprintf("%04d-decision-%d.md", i, i))
		content := fmt.Sprintf("# ADR-%04d: Decision %d\nStatus: Accepted\nDate: 2026-01-%02d\n\n## Decision\nWe choose pattern %d.", i, i, i%28+1, i)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}
	}

	ctx := context.Background()
	engine := &mockInventoryIntegrationEngine{}

	cfg := compiler.ProbeConfig{
		Goal:         "Read all ADR files in docs/adr/ and produce a consolidated decision log with status, date, and key implications for each decision. Focus on technical accuracy and consistent formatting. Every ADR must be included — there are 37+ ADR files.",
		PreloadPaths: []string{tmpDir},
	}

	taskID := fmt.Sprintf("task-test-37adrs-%d", os.Getpid())
	probeID := fmt.Sprintf("probe-test-37adrs-%d", os.Getpid())
	synthesis, err := RunProbePhases(ctx, taskID, probeID, cfg, engine, engine, nil)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	if engine.mapCount != 37 {
		t.Errorf("expected 37 map extractions, got %d", engine.mapCount)
	}

	// Verify all 37 ADRs are present in synthesis output
	for i := 1; i <= 37; i++ {
		expectedTag := fmt.Sprintf("%04d-decision-%d.md", i, i)
		if !strings.Contains(synthesis, expectedTag) {
			t.Errorf("expected synthesis to contain %s", expectedTag)
		}
	}
}
