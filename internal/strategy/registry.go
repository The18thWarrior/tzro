package strategy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"tzro/internal/embeddings"
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
// Evaluates exact match first, falling back to high-threshold semantic similarity.
func (r *StrategyRegistry) Get(nodeType string) (NodeStrategy, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if s, ok := r.strategies[nodeType]; ok {
		return s, true
	}

	// Normalize via high-threshold semantic similarity (threshold >= 0.85)
	normalized := r.NormalizeNodeType(nodeType)
	s, ok := r.strategies[normalized]
	return s, ok
}

// NormalizeNodeType maps an unverified node type identifier to a registered strategy type
// using semantic embedding similarity (threshold >= 0.85) if no exact match exists.
func (r *StrategyRegistry) NormalizeNodeType(t string) string {
	cleaned := strings.ToLower(strings.TrimSpace(t))
	if cleaned == "" {
		return t
	}

	// 1. Exact match check
	if _, exists := r.strategies[cleaned]; exists {
		return cleaned
	}

	// 2. High-threshold semantic similarity check using neural embeddings
	if embeddings.DefaultEngine != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()

		inputVec, err := embeddings.DefaultEngine.Embed(ctx, cleaned)
		if err == nil && len(inputVec) > 0 {
			var bestType string
			var bestScore float32 = 0.0

			for _, candidate := range r.order {
				candVec, err := embeddings.DefaultEngine.Embed(ctx, candidate)
				if err == nil && len(candVec) > 0 {
					sim := embeddings.DefaultEngine.CosineSimilarity(inputVec, candVec)
					if sim > bestScore {
						bestScore = sim
						bestType = candidate
					}
				}
			}

			// Require very high confidence (>= 0.85) for semantic normalization
			if bestScore >= 0.85 && bestType != "" {
				return bestType
			}
		}
	}

	// 3. Fallback: string similarity / stem / suffix matching if embeddings are unavailable
	var bestCandidate string
	var bestSim float64 = 0.0
	for _, candidate := range r.order {
		sim := computeStringSimilarity(cleaned, candidate)
		if sim > bestSim {
			bestSim = sim
			bestCandidate = candidate
		}
	}
	if bestSim >= 0.70 && bestCandidate != "" {
		return bestCandidate
	}

	return t
}

func computeStringSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if strings.HasSuffix(b, "_"+a) || strings.HasPrefix(b, a+"_") || strings.HasSuffix(a, "_"+b) || strings.HasPrefix(a, b+"_") {
		return 0.95
	}
	// Common prefix length
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	if i >= 5 {
		maxLen := len(a)
		if len(b) > maxLen {
			maxLen = len(b)
		}
		return float64(i) / float64(maxLen)
	}
	return 0.0
}

// BuildPlanJSONSchema constructs a strict GBNF JSON Schema that locks the node 'type'
// BuildPlanJSONSchema constructs a strict GBNF JSON Schema that locks the node 'type'
// field to the exact enum of registered strategy types and strictly defines all valid
// properties on tasks, nodes, and edges to guarantee graph validity.
func (r *StrategyRegistry) BuildPlanJSONSchema() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	typeList := make([]string, len(r.order))
	copy(typeList, r.order)

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"taskId":         map[string]interface{}{"type": "string"},
			"maxCycles":      map[string]interface{}{"type": "integer"},
			"sourceModality": map[string]interface{}{"type": "string", "enum": []string{"local", "web", "hybrid", ""}},
			"nodes": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":                  map[string]interface{}{"type": "string"},
						"type":                map[string]interface{}{"type": "string", "enum": typeList},
						"action":              map[string]interface{}{"type": "string"},
						"instructions":        map[string]interface{}{"type": "string"},
						"allowedTools":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
						"staticArgs":          map[string]interface{}{"type": "string"},
						"condition":           map[string]interface{}{"type": "string"},
						"defaultTarget":       map[string]interface{}{"type": "string"},
						"outputFormat":        map[string]interface{}{"type": "string", "enum": []string{"source_code", "markdown", "json", "text", ""}},
						"outputLanguage":      map[string]interface{}{"type": "string"},
						"activationThreshold": map[string]interface{}{"type": "number"},
						"recallPolicy":        map[string]interface{}{"type": "string", "enum": []string{"auto", "always", "skip", ""}},
						"probeConfig": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"goal":            map[string]interface{}{"type": "string"},
								"allowedTools":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
								"stepBudget":      map[string]interface{}{"type": "integer"},
								"sourceHint":      map[string]interface{}{"type": "string", "enum": []string{"filesystem", "web", "cache", ""}},
								"compactEvery":    map[string]interface{}{"type": "integer"},
								"directSynthesis": map[string]interface{}{"type": "boolean"},
								"preloadPaths":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
							},
						},
					},
					"required": []string{"id", "type", "instructions"},
				},
			},
			"edges": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"source":   map[string]interface{}{"type": "string"},
						"target":   map[string]interface{}{"type": "string"},
						"sourceId": map[string]interface{}{"type": "string"},
						"targetId": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
		"required": []string{"taskId", "nodes", "edges"},
	}

	bytes, _ := json.Marshal(schema)
	return string(bytes)
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
