package executor

import (
	"testing"

	"tzro/internal/compiler"
	"tzro/internal/strategy"
)

// ---------------------------------------------------------------------------
// Slice 3: ListStrategy — Node Strategy with deterministic discovery
// ---------------------------------------------------------------------------

func TestListStrategy_Type(t *testing.T) {
	base := &strategy.BaseStrategy{NodeType: "list"}
	s := NewListStrategy(nil, base)

	if s.Type() != "list" {
		t.Errorf("ListStrategy.Type() = %q, want %q", s.Type(), "list")
	}
}

func TestListStrategy_Registration(t *testing.T) {
	reg := strategy.NewStrategyRegistry()
	err := strategy.RegisterBuiltins(reg)
	if err != nil {
		t.Fatalf("RegisterBuiltins failed: %v", err)
	}

	// Verify "list" is registered
	s, ok := reg.Get("list")
	if !ok || s == nil {
		t.Fatalf("Strategy 'list' not found in registry after RegisterBuiltins")
	}
	if s.Type() != "list" {
		t.Errorf("Strategy type = %q, want %q", s.Type(), "list")
	}
}

func TestListStrategy_PlannerCard(t *testing.T) {
	base := &strategy.BaseStrategy{NodeType: "list"}
	s := NewListStrategy(nil, base)

	card := s.PlannerCard()
	if card == nil {
		t.Fatal("ListStrategy.PlannerCard() returned nil")
	}
	if card.Type != "list" {
		t.Errorf("PlannerCard.Type = %q, want %q", card.Type, "list")
	}
	if card.WhenToUse == "" {
		t.Error("PlannerCard.WhenToUse is empty")
	}
}

func TestListStrategy_CompilationRulesSkipRecall(t *testing.T) {
	base := &strategy.BaseStrategy{NodeType: "list"}
	s := NewListStrategy(nil, base)

	rules := s.CompilationRules()
	if rules == nil {
		t.Fatal("ListStrategy.CompilationRules() returned nil")
	}

	// Create a graph with a list node and verify the expansion skips Recall
	graph := &compiler.ExecutionGraph{
		Nodes: []compiler.GraphNode{
			{
				ID:           "list_extract",
				Type:         "list",
				Instructions: "Extract all exported functions",
				Status:       "pending",
			},
		},
	}

	result, err := rules.Expand(&graph.Nodes[0], graph)
	if err != nil {
		t.Fatalf("CompilationRules.Expand failed: %v", err)
	}

	// List nodes should return nil expansion — no Recall/Validator injection
	if result != nil {
		t.Errorf("CompilationRules.Expand should return nil for list nodes (no injection), got %+v", result)
	}
}

func TestListStrategy_NoEdgeThought(t *testing.T) {
	base := &strategy.BaseStrategy{NodeType: "list"}
	s := NewListStrategy(nil, base)

	policy := s.EdgeThoughtPolicy()
	if policy != nil {
		t.Error("ListStrategy should not participate in Edge Thought (deterministic extraction)")
	}
}
