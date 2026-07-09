package executor

import (
	"context"
	"strings"
	"testing"

	"tzro/internal/compiler"
	"tzro/internal/memory"
)

// TestPreFlectHookInjectsCorrectiveSkill verifies that when a matching
// corrective skill exists for a node's action tool, the skill's SOP
// content is injected into the node's instructions.
func TestPreFlectHookInjectsCorrectiveSkill(t *testing.T) {
	hook := &PreFlectHook{
		SkillFinder: func(toolName string) []memory.Skill {
			if toolName == "web_search" {
				return []memory.Skill{
					{
						Name:               "web_search_retry",
						TriggerDescription: "corrective: web_search often returns empty",
						SOPContent:         "CORRECTION: Always validate web_search results are non-empty before using them.",
					},
				}
			}
			return nil
		},
	}

	node := &compiler.GraphNode{
		ID:           "action_1",
		Type:         "action",
		Action:       "web_search",
		Instructions: "Search for recent news about AI",
	}

	action, err := hook.BeforeNode(context.Background(), "task-1", node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != ActionContinue {
		t.Errorf("expected ActionContinue, got %v", action)
	}

	if !strings.Contains(node.Instructions, "CORRECTION:") {
		t.Error("expected corrective skill content to be injected into instructions")
	}

	// Verify the original instructions are preserved
	if !strings.Contains(node.Instructions, "Search for recent news about AI") {
		t.Error("expected original instructions to be preserved after injection")
	}
}

// TestPreFlectHookNoMatchReturnsCleanContinue verifies that when no
// corrective skills match, the hook returns ActionContinue without
// modifying instructions.
func TestPreFlectHookNoMatchReturnsCleanContinue(t *testing.T) {
	hook := &PreFlectHook{
		SkillFinder: func(toolName string) []memory.Skill {
			return nil // no matches
		},
	}

	originalInstructions := "Read the file at /path/to/config"
	node := &compiler.GraphNode{
		ID:           "action_1",
		Type:         "action",
		Action:       "read_file",
		Instructions: originalInstructions,
	}

	action, err := hook.BeforeNode(context.Background(), "task-1", node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != ActionContinue {
		t.Errorf("expected ActionContinue, got %v", action)
	}

	if node.Instructions != originalInstructions {
		t.Error("instructions should not be modified when no skills match")
	}
}

// TestPreFlectHookImplementsExecutionHook verifies the PreFlectHook
// satisfies the ExecutionHook interface.
func TestPreFlectHookImplementsExecutionHook(t *testing.T) {
	var _ ExecutionHook = &PreFlectHook{}
}

// TestPreFlectHookMultipleSkillsInjected verifies that when multiple
// corrective skills match, all are injected.
func TestPreFlectHookMultipleSkillsInjected(t *testing.T) {
	hook := &PreFlectHook{
		SkillFinder: func(toolName string) []memory.Skill {
			return []memory.Skill{
				{SOPContent: "CORRECTION 1: First fix."},
				{SOPContent: "CORRECTION 2: Second fix."},
			}
		},
	}

	node := &compiler.GraphNode{
		ID:           "action_1",
		Type:         "action",
		Action:       "some_tool",
		Instructions: "Do something",
	}

	hook.BeforeNode(context.Background(), "task-1", node)

	if !strings.Contains(node.Instructions, "CORRECTION 1") {
		t.Error("expected first correction to be injected")
	}
	if !strings.Contains(node.Instructions, "CORRECTION 2") {
		t.Error("expected second correction to be injected")
	}
}
