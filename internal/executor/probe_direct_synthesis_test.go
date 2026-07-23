package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/internal/compiler"
	"tzro/internal/inference"
)

// mockSynthesisEngine captures the prompts sent to the synthesis engine for verification.
type mockSynthesisEngine struct {
	lastSystemPrompt string
	lastUserPrompt   string
	response         string
	err              error
}

func (m *mockSynthesisEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error) {
	m.lastSystemPrompt = systemPrompt
	m.lastUserPrompt = userPrompt
	return m.response, m.err
}

func (m *mockSynthesisEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string) (string, error) {
	if len(messages) >= 2 {
		m.lastSystemPrompt = messages[0].Content
		m.lastUserPrompt = messages[1].Content
	}
	return m.response, m.err
}

// TestDirectSynthesis validates the DirectSynthesis ProbeConfig option that bypasses
// the Thought Chain loop and runs single-shot inference against a pre-compiled context file.
// (Grilling Decision #3)
func TestDirectSynthesis(t *testing.T) {
	t.Run("DirectSynthesis_ReadsFileAndReturnsResult", func(t *testing.T) {
		// Create a temp file to act as the pre-compiled context
		tmpDir := t.TempDir()
		contextPath := filepath.Join(tmpDir, "context.md")
		contextContent := "# Codebase Map\n\n## Package: internal/cache\n- func NewCache() *Cache\n- type Cache struct\n"
		if err := os.WriteFile(contextPath, []byte(contextContent), 0644); err != nil {
			t.Fatal(err)
		}

		engine := &mockSynthesisEngine{response: "canned probe response"}
		synthEngine := &mockSynthesisEngine{response: "## Architecture\n\nThe cache package provides..."}

		config := compiler.ProbeConfig{
			Goal:            "Generate architecture documentation",
			DirectSynthesis: true,
			ContextFile:     contextPath,
		}

		result, err := RunProbe(
			context.Background(),
			"task-direct-synthesis-test",
			"probe_arch",
			config,
			engine,
			synthEngine,
			nil,
		)

		if err != nil {
			t.Fatalf("DirectSynthesis should not error: %v", err)
		}
		if result != "## Architecture\n\nThe cache package provides..." {
			t.Errorf("Expected synthesis engine response, got %q", result)
		}
		// The synthesis engine should have received the context file content
		if !strings.Contains(synthEngine.lastUserPrompt, contextContent) {
			t.Errorf("Expected context file content in user prompt, got %q", synthEngine.lastUserPrompt)
		}
		// The goal should be in the system prompt
		if !strings.Contains(synthEngine.lastSystemPrompt, "Generate architecture documentation") {
			t.Errorf("Expected goal in system prompt, got %q", synthEngine.lastSystemPrompt)
		}
	})

	t.Run("DirectSynthesis_MissingContextFile_Errors", func(t *testing.T) {
		engine := &mockSynthesisEngine{response: "unused"}
		synthEngine := &mockSynthesisEngine{response: "unused"}

		config := compiler.ProbeConfig{
			Goal:            "", // Missing both ContextFile and Goal
			DirectSynthesis: true,
			ContextFile:     "", // Missing!
		}

		_, err := RunProbe(
			context.Background(),
			"task-direct-synthesis-missing",
			"probe_missing",
			config,
			engine,
			synthEngine,
			nil,
		)

		if err == nil {
			t.Fatal("Expected error when ContextFile and Goal are missing")
		}
		if !strings.Contains(err.Error(), "ContextFile") && !strings.Contains(err.Error(), "Goal") {
			t.Errorf("Error should mention ContextFile or Goal, got: %v", err)
		}
	})

	t.Run("DirectSynthesis_NonexistentFile_Errors", func(t *testing.T) {
		engine := &mockSynthesisEngine{response: "unused"}
		synthEngine := &mockSynthesisEngine{response: "unused"}

		config := compiler.ProbeConfig{
			Goal:            "Generate docs",
			DirectSynthesis: true,
			ContextFile:     "/nonexistent/path/context.md",
		}

		_, err := RunProbe(
			context.Background(),
			"task-direct-synthesis-nofile",
			"probe_nofile",
			config,
			engine,
			synthEngine,
			nil,
		)

		if err == nil {
			t.Fatal("Expected error when context file doesn't exist")
		}
	})
}
