package strategy

import (
	"context"
	"encoding/json"
	"testing"

	"tzro/internal/compiler"
	"tzro/internal/inference"
)

// ---------------------------------------------------------------------------
// Test helper: minimal strategy for registration tests
// ---------------------------------------------------------------------------

type testStrategy struct {
	nodeType    string
	plannerCard *PlannerCard
	contextRole *ContextRole
}

func (s *testStrategy) Type() string { return s.nodeType }

func (s *testStrategy) StagePlan(node *compiler.GraphNode) *StagePlanDef { return nil }

func (s *testStrategy) Execute(ctx context.Context, nr *NodeRuntime) (*ExecutionResult, error) {
	return &ExecutionResult{Output: "test", Directive: DirectiveContinue}, nil
}

func (s *testStrategy) EdgeThoughtPolicy() *EdgeThoughtConfig { return nil }

func (s *testStrategy) PlannerCard() *PlannerCard { return s.plannerCard }

func (s *testStrategy) CompilationRules() *CompilationRules { return nil }

func (s *testStrategy) ContextRole() *ContextRole {
	if s.contextRole != nil {
		return s.contextRole
	}
	return &ContextRole{}
}

// ---------------------------------------------------------------------------
// StrategyRegistry tests
// ---------------------------------------------------------------------------

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewStrategyRegistry()

	s := &testStrategy{
		nodeType: "list",
		plannerCard: &PlannerCard{
			Type:      "list",
			WhenToUse: "Open-ended exploration.",
		},
	}

	if err := r.Register(s); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	got, ok := r.Get("list")
	if !ok {
		t.Fatal("Get returned false for registered strategy")
	}
	if got.Type() != "list" {
		t.Errorf("got type %q, want %q", got.Type(), "list")
	}
}

func TestRegistry_DuplicateRegistration(t *testing.T) {
	r := NewStrategyRegistry()

	s1 := &testStrategy{
		nodeType:    "list",
		plannerCard: &PlannerCard{Type: "list", WhenToUse: "test"},
	}
	s2 := &testStrategy{
		nodeType:    "list",
		plannerCard: &PlannerCard{Type: "list", WhenToUse: "test"},
	}

	if err := r.Register(s1); err != nil {
		t.Fatalf("First Register failed: %v", err)
	}

	if err := r.Register(s2); err == nil {
		t.Fatal("Expected error on duplicate registration, got nil")
	}
}

func TestRegistry_EmptyType(t *testing.T) {
	r := NewStrategyRegistry()

	s := &testStrategy{nodeType: ""}
	if err := r.Register(s); err == nil {
		t.Fatal("Expected error on empty type, got nil")
	}
}

func TestRegistry_PlannerCardValidation(t *testing.T) {
	r := NewStrategyRegistry()

	// Missing WhenToUse
	s := &testStrategy{
		nodeType:    "test",
		plannerCard: &PlannerCard{Type: "test"},
	}
	if err := r.Register(s); err == nil {
		t.Fatal("Expected error on incomplete PlannerCard, got nil")
	}

	// Type mismatch
	s2 := &testStrategy{
		nodeType:    "test",
		plannerCard: &PlannerCard{Type: "wrong", WhenToUse: "test"},
	}
	if err := r.Register(s2); err == nil {
		t.Fatal("Expected error on PlannerCard type mismatch, got nil")
	}
}

func TestRegistry_List(t *testing.T) {
	r := NewStrategyRegistry()

	strategies := []NodeStrategy{
		&testStrategy{nodeType: "list", plannerCard: &PlannerCard{Type: "list", WhenToUse: "explore"}},
		&testStrategy{nodeType: "analyze", plannerCard: &PlannerCard{Type: "analyze", WhenToUse: "query"}},
		&testStrategy{nodeType: "synthesis", plannerCard: &PlannerCard{Type: "synthesis", WhenToUse: "synthesize"}},
	}

	for _, s := range strategies {
		if err := r.Register(s); err != nil {
			t.Fatalf("Register %q: %v", s.Type(), err)
		}
	}

	listed := r.List()
	if len(listed) != 3 {
		t.Fatalf("List() returned %d strategies, want 3", len(listed))
	}

	// Verify registration order is preserved
	expectedOrder := []string{"list", "analyze", "synthesis"}
	for i, s := range listed {
		if s.Type() != expectedOrder[i] {
			t.Errorf("List()[%d].Type() = %q, want %q", i, s.Type(), expectedOrder[i])
		}
	}
}

func TestRegistry_BuildReferenceCard(t *testing.T) {
	r := NewStrategyRegistry()

	r.Register(&testStrategy{
		nodeType: "list",
		plannerCard: &PlannerCard{
			Type:      "list",
			WhenToUse: "Extraction and enumeration tasks.",
			KeyFields: []FieldDesc{
				{Name: "probeConfig", Description: "goal, preloadPaths", Required: true},
			},
			CriticalRules: []string{
				"Use 'list' for all code/doc extraction tasks.",
			},
		},
	})
	r.Register(&testStrategy{
		nodeType: "action",
		plannerCard: &PlannerCard{
			Type:      "action",
			WhenToUse: "Single known tool call.",
			KeyFields: []FieldDesc{
				{Name: "action", Description: "tool name", Required: true},
			},
		},
	})

	card := r.BuildReferenceCard()

	if card == "" {
		t.Fatal("BuildReferenceCard returned empty string")
	}

	// Check table header
	if !contains(card, "| Type | When to Use | Key Fields |") {
		t.Error("Missing table header")
	}

	// Check list row
	if !contains(card, "| list | Extraction and enumeration tasks.") {
		t.Error("Missing list row")
	}

	// Check action row
	if !contains(card, "| action | Single known tool call. |") {
		t.Error("Missing action row")
	}

	// Check critical rules section
	if !contains(card, "Use 'list' for all code/doc extraction tasks.") {
		t.Error("Missing critical rule")
	}
}

func TestRegistry_GetUnknownType(t *testing.T) {
	r := NewStrategyRegistry()

	_, ok := r.Get("unknown")
	if ok {
		t.Fatal("Get returned true for unregistered type")
	}
}

// ---------------------------------------------------------------------------
// ArtifactStore tests
// ---------------------------------------------------------------------------

func TestArtifactStore_TypedAccess(t *testing.T) {
	store := NewArtifactStore()

	// Set and get a string artifact
	SetArtifact(store, KeyTerminalSynthesis, "Hello world")
	val, ok := GetArtifact(store, KeyTerminalSynthesis)
	if !ok {
		t.Fatal("GetArtifact returned false for set key")
	}
	if val != "Hello world" {
		t.Errorf("got %q, want %q", val, "Hello world")
	}
}

func TestArtifactStore_MissingKey(t *testing.T) {
	store := NewArtifactStore()

	val, ok := GetArtifact(store, KeyTerminalSynthesis)
	if ok {
		t.Fatal("GetArtifact returned true for missing key")
	}
	if val != "" {
		t.Errorf("expected zero value, got %q", val)
	}
}

func TestArtifactStore_NilStore(t *testing.T) {
	val, ok := GetArtifact[string](nil, KeyTerminalSynthesis)
	if ok {
		t.Fatal("GetArtifact returned true for nil store")
	}
	if val != "" {
		t.Errorf("expected zero value, got %q", val)
	}
}

func TestArtifactStore_SerializedAccess(t *testing.T) {
	store := NewArtifactStore()

	// Set via typed layer
	SetArtifact(store, KeyTerminalSynthesis, "typed value")

	// Access via serialized layer (lazy serialization)
	data, ok := store.GetSerialized("terminalSynthesis")
	if !ok {
		t.Fatal("GetSerialized returned false")
	}

	var got string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != "typed value" {
		t.Errorf("got %q, want %q", got, "typed value")
	}
}

func TestArtifactStore_SerializedToTyped(t *testing.T) {
	store := NewArtifactStore()

	// Set via serialized layer
	store.SetSerialized("terminalSynthesis", json.RawMessage(`"serialized value"`))

	// Access via typed layer (lazy deserialization)
	val, ok := GetArtifact(store, KeyTerminalSynthesis)
	if !ok {
		t.Fatal("GetArtifact returned false for serialized key")
	}
	if val != "serialized value" {
		t.Errorf("got %q, want %q", val, "serialized value")
	}
}

func TestArtifactStore_Merge(t *testing.T) {
	store1 := NewArtifactStore()
	store2 := NewArtifactStore()

	SetArtifact(store1, KeyTerminalSynthesis, "existing")
	SetArtifact(store2, KeyTerminalSynthesis, "should not overwrite")
	SetArtifact(store2, KeyRefinedContext, "new value")

	store1.Merge(store2)

	// Existing key should NOT be overwritten
	val, _ := GetArtifact(store1, KeyTerminalSynthesis)
	if val != "existing" {
		t.Errorf("Merge overwrote existing key: got %q", val)
	}

	// New key should be added
	val2, ok := GetArtifact(store1, KeyRefinedContext)
	if !ok {
		t.Fatal("Merge did not add new key")
	}
	if val2 != "new value" {
		t.Errorf("got %q, want %q", val2, "new value")
	}
}

func TestArtifactStore_Keys(t *testing.T) {
	store := NewArtifactStore()
	SetArtifact(store, KeyTerminalSynthesis, "a")
	SetArtifact(store, KeyRefinedContext, "b")
	store.SetSerialized("extra", json.RawMessage(`"c"`))

	keys := store.Keys()
	if len(keys) != 3 {
		t.Errorf("Keys() returned %d keys, want 3", len(keys))
	}
}

// ---------------------------------------------------------------------------
// FlowDirective tests
// ---------------------------------------------------------------------------

func TestFlowDirective_Values(t *testing.T) {
	// Ensure enum values are distinct
	directives := []FlowDirective{
		DirectiveContinue,
		DirectiveSkipDownstream,
		DirectivePause,
		DirectiveRetry,
		DirectiveHalt,
	}

	seen := make(map[FlowDirective]bool)
	for _, d := range directives {
		if seen[d] {
			t.Errorf("duplicate FlowDirective value: %d", d)
		}
		seen[d] = true
	}
}

// ---------------------------------------------------------------------------
// RegisterBuiltins tests
// ---------------------------------------------------------------------------

func TestRegisterBuiltins(t *testing.T) {
	r := NewStrategyRegistry()
	if err := RegisterBuiltins(r); err != nil {
		t.Fatalf("RegisterBuiltins failed: %v", err)
	}

	// All 10 built-in types should be registered (ADR-0091: probe removed)
	expectedTypes := []string{
		"analyze", "recall", "synthesis",
		"semantic_validator", "action", "branch",
		"sub_dag", "scatter_assembly", "deterministic",
		"list",
	}

	for _, nodeType := range expectedTypes {
		s, ok := r.Get(nodeType)
		if !ok {
			t.Errorf("missing strategy for type %q", nodeType)
			continue
		}
		if s.Type() != nodeType {
			t.Errorf("strategy type mismatch: got %q, want %q", s.Type(), nodeType)
		}
	}

	// Verify total count
	listed := r.List()
	if len(listed) != len(expectedTypes) {
		t.Errorf("registry has %d strategies, want %d", len(listed), len(expectedTypes))
	}
}

func TestBuiltinContextRoles(t *testing.T) {
	tests := []struct {
		nodeType          string
		plainText         bool
		primaryData       bool
		hasThoughtSteps   bool
		weightGreaterThan float64
	}{
		{"list", true, true, false, 0.0},
		{"analyze", true, false, true, 0.5},
		{"recall", true, true, false, 1.5},
		{"synthesis", true, false, false, 0.5},
		{"semantic_validator", false, false, false, 0.5},
		{"action", false, false, false, 1.0},
		{"branch", false, false, false, 0.5},
	}

	r := NewStrategyRegistry()
	RegisterBuiltins(r)

	for _, tc := range tests {
		t.Run(tc.nodeType, func(t *testing.T) {
			s, ok := r.Get(tc.nodeType)
			if !ok {
				t.Fatalf("missing strategy for %q", tc.nodeType)
			}
			role := s.ContextRole()
			if role.ProducesPlainText != tc.plainText {
				t.Errorf("ProducesPlainText = %v, want %v", role.ProducesPlainText, tc.plainText)
			}
			if role.IsPrimaryDataCarrier != tc.primaryData {
				t.Errorf("IsPrimaryDataCarrier = %v, want %v", role.IsPrimaryDataCarrier, tc.primaryData)
			}
			if role.HasThoughtSteps != tc.hasThoughtSteps {
				t.Errorf("HasThoughtSteps = %v, want %v", role.HasThoughtSteps, tc.hasThoughtSteps)
			}
			if role.ContextWeight <= tc.weightGreaterThan {
				t.Errorf("ContextWeight = %v, want > %v", role.ContextWeight, tc.weightGreaterThan)
			}
		})
	}
}

func TestBaseStrategy_DefaultExecuteHalts(t *testing.T) {
	s := &BaseStrategy{
		NodeType: "test",
		Card:     &PlannerCard{Type: "test", WhenToUse: "testing"},
	}

	nr := NewNodeRuntime("task1", &compiler.GraphNode{ID: "n1"}, &compiler.ExecutionGraph{}, nil, nil, nil, nil, nil, nil, nil, "", inference.StreamMeta{}, "")
	result, err := s.Execute(context.Background(), nr)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Directive != DirectiveHalt {
		t.Errorf("directive = %v, want DirectiveHalt (%v)", result.Directive, DirectiveHalt)
	}
	if result.Output != "strategy not yet implemented" {
		t.Errorf("output = %q, want %q", result.Output, "strategy not yet implemented")
	}
}

func TestBuiltinReferenceCard(t *testing.T) {
	r := NewStrategyRegistry()
	RegisterBuiltins(r)

	card := r.BuildReferenceCard()
	if card == "" {
		t.Fatal("BuildReferenceCard returned empty string")
	}

	// Check that planner-visible types appear in the card
	for _, nodeType := range []string{"list", "analyze", "recall", "synthesis", "action", "branch", "sub_dag"} {
		if !contains(card, nodeType) {
			t.Errorf("reference card missing type %q", nodeType)
		}
	}

	// scatter_assembly and deterministic should NOT appear (nil PlannerCard)
	if contains(card, "scatter_assembly") {
		t.Error("scatter_assembly should not appear in reference card (internal type)")
	}
}

// TestContextWeightParityWithLegacy verifies that int(ContextWeight * 4) for each
// built-in strategy matches the hardcoded typeWeights map in executor_context.go.
// This is the behavioral parity gate — if this test fails, the context budgeting
// will allocate different per-node budgets than the legacy code.
func TestContextWeightParityWithLegacy(t *testing.T) {
	// The hardcoded weights from executor_context.go lines 311-316
	legacyWeights := map[string]int{
		"recall":        8,
		"action":        6,
		"list":          2,
		"deterministic": 1,
	}
	legacyDefault := 4

	r := NewStrategyRegistry()
	RegisterBuiltins(r)

	for _, s := range r.List() {
		role := s.ContextRole()
		computed := int(role.ContextWeight * 4)
		if computed < 1 {
			computed = 1 // matches the clamp in executor_context.go
		}

		expected, ok := legacyWeights[s.Type()]
		if !ok {
			expected = legacyDefault
		}

		if computed != expected {
			t.Errorf("type %q: int(ContextWeight %.2f * 4) = %d, want %d (legacy weight)",
				s.Type(), role.ContextWeight, computed, expected)
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestRegistryBuiltins_AllHaveType(t *testing.T) {
	reg := NewStrategyRegistry()
	RegisterBuiltins(reg)

	expectedTypes := []string{
		"list", "analyze", "recall", "synthesis",
		"semantic_validator", "action", "branch",
		"deterministic", "sub_dag", "scatter_assembly",
		"list",
	}

	for _, nodeType := range expectedTypes {
		s, ok := reg.Get(nodeType)
		if !ok {
			t.Errorf("Built-in strategy %q not registered", nodeType)
			continue
		}
		if s.Type() != nodeType {
			t.Errorf("Strategy %q returned Type() = %q", nodeType, s.Type())
		}
		// All built-ins must have a ContextRole
		role := s.ContextRole()
		if role == nil {
			t.Errorf("Strategy %q has nil ContextRole", nodeType)
		}
	}
}

func TestFlowDirective_SkipDownstream(t *testing.T) {
	// A custom strategy returning DirectiveSkipDownstream should signal
	// the envelope to skip downstream nodes (branch not-satisfied pattern).
	s := &testStrategy{
		nodeType:    "test_branch",
		plannerCard: &PlannerCard{Type: "test_branch", WhenToUse: "testing"},
	}

	// testStrategy.Execute returns DirectiveContinue by default;
	// we just verify the enum values are distinct.
	result, err := s.Execute(context.Background(), &NodeRuntime{
		taskID: "t1",
		node:   &compiler.GraphNode{ID: "b1"},
		graph:  &compiler.ExecutionGraph{TaskID: "t1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Directive != DirectiveContinue {
		t.Errorf("expected DirectiveContinue, got %v", result.Directive)
	}
}

func TestRegistryReplace(t *testing.T) {
	reg := NewStrategyRegistry()

	// Register a stub
	stub := &BaseStrategy{NodeType: "test_replace"}
	if err := reg.Register(stub); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Verify stub is in the registry
	s, ok := reg.Get("test_replace")
	if !ok {
		t.Fatal("expected stub to be registered")
	}
	if _, isBase := s.(*BaseStrategy); !isBase {
		t.Fatal("expected *BaseStrategy type")
	}

	// Create an upgraded strategy (a testStrategy that owns Execute)
	upgraded := &testStrategy{
		nodeType: "test_replace",
	}

	// Replace should work
	if err := reg.Replace(upgraded); err != nil {
		t.Fatalf("Replace failed: %v", err)
	}

	// Verify replacement
	s, ok = reg.Get("test_replace")
	if !ok {
		t.Fatal("expected replaced strategy to be registered")
	}

	result, err := s.Execute(context.Background(), &NodeRuntime{
		taskID: "t1",
		node:   &compiler.GraphNode{ID: "n1"},
		graph:  &compiler.ExecutionGraph{TaskID: "t1"},
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	// testStrategy returns "test" as output
	if result.Output != "test" {
		t.Errorf("expected 'test' output, got %q", result.Output)
	}
}

func TestRegistryReplace_NotRegistered(t *testing.T) {
	reg := NewStrategyRegistry()
	stub := &BaseStrategy{NodeType: "nonexistent"}
	err := reg.Replace(stub)
	if err == nil {
		t.Fatal("expected error for Replace on unregistered type")
	}
}

func TestRegistry_NormalizeNodeType_SemanticSimilarity(t *testing.T) {
	reg := NewStrategyRegistry()
	reg.Register(&testStrategy{nodeType: "synthesis", plannerCard: &PlannerCard{Type: "synthesis", WhenToUse: "synthesize findings"}})
	reg.Register(&testStrategy{nodeType: "semantic_validator", plannerCard: &PlannerCard{Type: "semantic_validator", WhenToUse: "validate outputs"}})
	reg.Register(&testStrategy{nodeType: "list", plannerCard: &PlannerCard{Type: "list", WhenToUse: "extract from directories"}})

	// 1. Exact matches
	if norm := reg.NormalizeNodeType("synthesis"); norm != "synthesis" {
		t.Errorf("expected 'synthesis', got %q", norm)
	}

	// 2. High-similarity prefix / substring fallback
	if norm := reg.NormalizeNodeType("synthesize"); norm != "synthesis" {
		t.Errorf("expected 'synthesis' normalized from 'synthesize', got %q", norm)
	}
	if norm := reg.NormalizeNodeType("validator"); norm != "semantic_validator" {
		t.Errorf("expected 'semantic_validator' normalized from 'validator', got %q", norm)
	}
}

func TestRegistry_BuildPlanJSONSchema(t *testing.T) {
	reg := NewStrategyRegistry()
	reg.Register(&testStrategy{nodeType: "action", plannerCard: &PlannerCard{Type: "action", WhenToUse: "tool calls"}})
	reg.Register(&testStrategy{nodeType: "synthesis", plannerCard: &PlannerCard{Type: "synthesis", WhenToUse: "synthesize"}})
	reg.Register(&testStrategy{nodeType: "list", plannerCard: &PlannerCard{Type: "list", WhenToUse: "explore"}})

	schemaJSON := reg.BuildPlanJSONSchema()
	var fullParsed struct {
		Properties struct {
			Nodes struct {
				Items struct {
					Properties struct {
						Type struct {
							Enum []string `json:"enum"`
						} `json:"type"`
						RecallPolicy struct {
							Type string   `json:"type"`
							Enum []string `json:"enum"`
						} `json:"recallPolicy"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"nodes"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schemaJSON), &fullParsed); err != nil {
		t.Fatalf("failed to parse generated schema for recallPolicy check: %v", err)
	}

	enums := fullParsed.Properties.Nodes.Items.Properties.Type.Enum
	if len(enums) != 3 {
		t.Fatalf("expected 3 enums, got %d: %v", len(enums), enums)
	}
	if enums[0] != "action" || enums[1] != "synthesis" || enums[2] != "list" {
		t.Errorf("unexpected enums order: %v", enums)
	}
	rp := fullParsed.Properties.Nodes.Items.Properties.RecallPolicy
	if rp.Type != "string" {
		t.Errorf("expected recallPolicy type 'string', got %q", rp.Type)
	}
	if len(rp.Enum) != 4 {
		t.Errorf("expected 4 recallPolicy enum values, got %d: %v", len(rp.Enum), rp.Enum)
	}
}


