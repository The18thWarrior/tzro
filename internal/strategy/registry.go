package strategy

import (
	"fmt"
	"strings"
	"sync"
)

// StrategyRegistry maps node type strings to NodeStrategy implementations.
// Built-in strategies are registered at executor startup. Agent App strategies
// are registered at install time via the Package Manager.
type StrategyRegistry struct {
	strategies map[string]NodeStrategy
	order      []string // Preserves registration order for deterministic output
	mu         sync.RWMutex
}

// NewStrategyRegistry creates an empty registry.
func NewStrategyRegistry() *StrategyRegistry {
	return &StrategyRegistry{
		strategies: make(map[string]NodeStrategy),
	}
}

// Register adds a strategy to the registry. Returns an error if a strategy
// is already registered for the same type string, or if the strategy's
// Stage Plan contracts are invalid.
func (r *StrategyRegistry) Register(s NodeStrategy) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	nodeType := s.Type()
	if nodeType == "" {
		return fmt.Errorf("strategy has empty type identifier")
	}

	if _, exists := r.strategies[nodeType]; exists {
		return fmt.Errorf("strategy already registered for type %q", nodeType)
	}

	// Validate PlannerCard
	if card := s.PlannerCard(); card != nil {
		if card.Type == "" || card.WhenToUse == "" {
			return fmt.Errorf("strategy %q has incomplete PlannerCard (Type and WhenToUse required)", nodeType)
		}
		if card.Type != nodeType {
			return fmt.Errorf("strategy %q PlannerCard.Type mismatch: %q", nodeType, card.Type)
		}
	}

	r.strategies[nodeType] = s
	r.order = append(r.order, nodeType)
	return nil
}

// Replace replaces an existing strategy with a new implementation.
// Used during the Strangler Fig migration to upgrade builtin stubs with
// strategy-owned Execute implementations. The registration order is preserved.
// Returns an error if no strategy is registered for the given type.
func (r *StrategyRegistry) Replace(s NodeStrategy) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	nodeType := s.Type()
	if _, exists := r.strategies[nodeType]; !exists {
		return fmt.Errorf("no strategy registered for type %q; use Register instead", nodeType)
	}

	r.strategies[nodeType] = s
	return nil
}


// Get returns the strategy for the given node type, or nil and false if not found.
func (r *StrategyRegistry) Get(nodeType string) (NodeStrategy, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.strategies[nodeType]
	return s, ok
}

// List returns all registered strategies in registration order.
func (r *StrategyRegistry) List() []NodeStrategy {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]NodeStrategy, 0, len(r.order))
	for _, nodeType := range r.order {
		if s, ok := r.strategies[nodeType]; ok {
			result = append(result, s)
		}
	}
	return result
}

// Types returns all registered type strings in registration order.
func (r *StrategyRegistry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]string, len(r.order))
	copy(result, r.order)
	return result
}

// BuildReferenceCard generates a dynamic NodeTypeReferenceCard from all
// registered strategies' PlannerCards. Built-in types appear first in
// registration order, followed by custom types. The output format matches
// the existing reference card structure for 4B model compatibility.
func (r *StrategyRegistry) BuildReferenceCard() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("## Node Type Reference\n\n")
	sb.WriteString("### Node Types\n")
	sb.WriteString("| Type | When to Use | Key Fields |\n")
	sb.WriteString("|------|------------|------------|\n")

	var criticalRules []string

	for _, nodeType := range r.order {
		s, ok := r.strategies[nodeType]
		if !ok {
			continue
		}
		card := s.PlannerCard()
		if card == nil {
			continue
		}

		// Format key fields as a compact string
		fields := formatKeyFields(card.KeyFields)
		sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n", card.Type, card.WhenToUse, fields))

		// Collect critical rules
		criticalRules = append(criticalRules, card.CriticalRules...)
	}

	// Append shared schema field descriptions
	sb.WriteString("\n### Key Schema Fields\n")
	sb.WriteString("- instructions: Natural language goal for the node. Include ALL static values from the user's prompt.\n")
	sb.WriteString("- dynamicBindings: For values from upstream nodes, use {\"param\": \"upstream_node_id.output.property\"}. Do NOT bake upstream values into instructions.\n")
	sb.WriteString("- allowedTools: Restrict to only the 1-2 tools needed. Must reference tools from the inventory.\n")
	sb.WriteString("- activationThreshold: 0.0 = disabled (default). 0.7 = enable Edge Thoughts for neural traversal.\n")
	sb.WriteString("- probeConfig.sourceHint: \"web\" for internet research, \"filesystem\" for local files (default).\n")
	sb.WriteString("- probeConfig.stepBudget: Max exploration steps (default 20).\n")

	// Append critical rules
	if len(criticalRules) > 0 {
		sb.WriteString("\n### Critical Rules\n")
		for i, rule := range criticalRules {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, rule))
		}
	}

	return sb.String()
}

// formatKeyFields renders FieldDesc slice as a compact string for the
// reference card table.
func formatKeyFields(fields []FieldDesc) string {
	if len(fields) == 0 {
		return ""
	}

	var parts []string
	for _, f := range fields {
		desc := f.Name
		if f.Description != "" {
			desc += " (" + f.Description + ")"
		}
		parts = append(parts, desc)
	}
	return strings.Join(parts, ", ")
}
