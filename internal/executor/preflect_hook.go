package executor

import (
	"context"
	"fmt"
	"os"

	"tzro/internal/compiler"
	"tzro/internal/memory"
)

// SkillFinderFunc is a function type that finds corrective skills relevant to
// a given tool name. This abstraction enables dependency injection for testing
// without requiring a live SQLite database.
type SkillFinderFunc func(toolName string) []memory.Skill

// PreFlectHook is an ExecutionHook that injects corrective micro-skills into
// node instructions before execution. It queries the skill store for any
// skills whose trigger description matches the node's tool action, and
// prefixes matching SOP content to the node's instructions.
//
// This implements the "pre-flight correction" pattern: if a tool has a known
// failure mode (captured as a corrective micro-skill from a previous failed
// execution), the correction is applied proactively before the node runs.
type PreFlectHook struct {
	SkillFinder SkillFinderFunc
}

// BeforeLevel is a no-op for PreFlect — corrections are applied per-node.
func (h *PreFlectHook) BeforeLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error) {
	return ActionContinue, nil
}

// AfterLevel is a no-op for PreFlect.
func (h *PreFlectHook) AfterLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error) {
	return ActionContinue, nil
}

// BeforeNode checks for corrective micro-skills matching the node's tool
// action and injects their SOP content as instruction prefixes.
func (h *PreFlectHook) BeforeNode(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error) {
	if h.SkillFinder == nil {
		return ActionContinue, nil
	}

	skills := h.SkillFinder(node.Action)
	if len(skills) == 0 {
		return ActionContinue, nil
	}

	// Inject each corrective skill's SOP content as a prefix to the
	// node's instructions, preserving the original instructions.
	var corrections string
	for _, skill := range skills {
		corrections += skill.SOPContent + "\n\n"
		fmt.Fprintf(os.Stderr, "[PreFlect] Injecting corrective skill %q into node %s (tool: %s)\n",
			skill.Name, node.ID, node.Action)
	}

	node.Instructions = corrections + node.Instructions

	return ActionContinue, nil
}

// AfterNode is a no-op for PreFlect.
func (h *PreFlectHook) AfterNode(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (HookAction, error) {
	return ActionContinue, nil
}

// OnEdgeTraversal is a no-op for PreFlect.
func (h *PreFlectHook) OnEdgeTraversal(ctx context.Context, taskID string, sourceNode, targetNode *compiler.GraphNode, edgeThought *memory.EdgeThought) (HookAction, error) {
	return ActionContinue, nil
}
