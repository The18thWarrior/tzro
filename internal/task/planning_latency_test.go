package task

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tzro/internal/inference"
)

type latencyMockBackend struct {
	CapturedMessages []inference.InferenceMessage
}

func (m *latencyMockBackend) CallModel(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string) (*inference.InferenceResult, error) {
	m.CapturedMessages = messages
	return &inference.InferenceResult{Content: `{"taskId": "t_test", "nodes": []}`}, nil
}
func (m *latencyMockBackend) CallModelStream(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, meta inference.StreamMeta) (*inference.InferenceResult, error) {
	return nil, nil
}
func (m *latencyMockBackend) Status() string { return "active" }
func (m *latencyMockBackend) Start(ctx context.Context) error { return nil }
func (m *latencyMockBackend) Stop() error { return nil }

func TestPlan_ShallowMapIntegration(t *testing.T) {
	ctx := context.Background()
	mock := &latencyMockBackend{}
	
	// Save current working directory
	oldWd, _ := os.Getwd()
	// Find repo root (heuristic: look for go.mod)
	repoRoot := oldWd
	for {
		if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(repoRoot)
		if parent == repoRoot {
			t.Fatal("Could not find repo root")
		}
		repoRoot = parent
	}
	_ = os.Chdir(repoRoot)
	defer func() { _ = os.Chdir(oldWd) }()

	// Override active backend
	oldBackend := inference.ActiveBackend
	inference.ActiveBackend = mock
	defer func() { inference.ActiveBackend = oldBackend }()

	_, _ = Plan(ctx, "Test shallow planning", ExecuteOptions{TaskID: "t_test"})

	foundShallow := false
	foundSignatures := false

	for _, msg := range mock.CapturedMessages {
		if msg.Role == "system" {
			// t.Logf("Captured System Prompt: %s", msg.Content)
			
			// Extract map section
			startIdx := strings.Index(msg.Content, "## Static Repository Map Scaffolding:")
			if startIdx == -1 {
				continue
			}
			endIdx := strings.Index(msg.Content[startIdx:], "## Output Schema Constraints:")
			if endIdx == -1 {
				continue
			}
			mapContent := msg.Content[startIdx : startIdx+endIdx]

			if strings.Contains(mapContent, "internal/task/") {
				foundShallow = true
			}
			
			// Look for evidence of full AST signatures in the mapContent
			if strings.Contains(mapContent, "### File:") || strings.Contains(mapContent, "func ") || strings.Contains(mapContent, "type ") {
				foundSignatures = true
			}
		}
	}

	if !foundShallow {
		t.Error("Expected shallow map (directories) in planner prompt map section, but not found")
	}
	if foundSignatures {
		t.Error("Found full AST signatures in planner prompt, expected only shallow directory tree")
	}
}
