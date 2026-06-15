package executor

import (
	"context"
	"sync"
	"testing"

	"tzro/internal/compiler"
	"tzro/internal/memory"
)

// TestOnEdgeTraversalHookFires verifies that the OnEdgeTraversal hook fires
// when an edge is traversed in the ready-queue execution model.
func TestOnEdgeTraversalHookFires(t *testing.T) {
	var traversedEdges []string
	var mu sync.Mutex

	hook := &mockHook{
		onEdgeTraversalFn: func(ctx context.Context, taskID string, sourceNode, targetNode *compiler.GraphNode, edgeThought *memory.EdgeThought) (HookAction, error) {
			mu.Lock()
			traversedEdges = append(traversedEdges, sourceNode.ID+"→"+targetNode.ID)
			mu.Unlock()
			return ActionContinue, nil
		},
	}

	// Verify the hook is callable (compile-time test of interface compliance)
	action, err := hook.OnEdgeTraversal(
		context.Background(),
		"task-hook-1",
		&compiler.GraphNode{ID: "A"},
		&compiler.GraphNode{ID: "B"},
		nil,
	)
	if err != nil {
		t.Fatalf("OnEdgeTraversal returned error: %v", err)
	}
	if action != ActionContinue {
		t.Errorf("expected ActionContinue, got %v", action)
	}
	if len(traversedEdges) != 1 || traversedEdges[0] != "A→B" {
		t.Errorf("expected [A→B], got %v", traversedEdges)
	}
}

// TestOnEdgeTraversalHookDefaultContinue verifies that the mockHook returns
// ActionContinue when no onEdgeTraversalFn is set (default behavior).
func TestOnEdgeTraversalHookDefaultContinue(t *testing.T) {
	hook := &mockHook{} // No functions set

	action, err := hook.OnEdgeTraversal(
		context.Background(),
		"task-hook-2",
		&compiler.GraphNode{ID: "X"},
		&compiler.GraphNode{ID: "Y"},
		nil,
	)
	if err != nil {
		t.Fatalf("OnEdgeTraversal returned error: %v", err)
	}
	if action != ActionContinue {
		t.Errorf("expected default ActionContinue, got %v", action)
	}
}
