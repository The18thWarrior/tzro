package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"tzro/internal/compiler"
	"tzro/internal/inference"
)

// stubToolDispatcher returns a ToolDispatcher that echoes tool args as JSON,
// matching the old stub behavior. Tests that construct PhaseRunner directly
// set this on the ToolDispatcher field.
func stubToolDispatcher() func(context.Context, string, map[string]interface{}) (string, error) {
	return func(_ context.Context, toolName string, args map[string]interface{}) (string, error) {
		argsJSON, _ := json.Marshal(args)
		return fmt.Sprintf("[%s result: args=%s]", toolName, string(argsJSON)), nil
	}
}

// stubDispatchCtx returns a context with a stub ToolDispatcherKey injected.
// Tests that call RunProbePhases/RunAnalyzePhases/RunResearchPhases use this
// to avoid requiring real tool registration.
func stubDispatchCtx() context.Context {
	return context.WithValue(context.Background(), ToolDispatcherKey, stubToolDispatcher())
}

// MockPhaseEngine is a test double for Phase Runner step execution.
// It returns pre-configured responses per phase, allowing tests to
// control phase behavior deterministically.
type MockPhaseEngine struct {
	// PhaseResponses maps phase name → ordered list of steps.
	// Each step is consumed by Pass 1. GBNF extraction (Pass 2) reads
	// the last consumed step's tool data.
	PhaseResponses map[string][]MockPhaseStep
	phaseCursors   map[string]int
	lastConsumed   map[string]int // tracks which step was last consumed by Pass 1
	CallLog        []MockPhaseCall
}

type MockPhaseStep struct {
	Reasoning  string // Pass 1 output (free-text)
	ToolName   string // Tool to call (empty → synthesize)
	ToolArgs   map[string]interface{}
	ToolResult string // Simulated tool output
	Error      error  // Simulated tool error
}

type MockPhaseCall struct {
	PhaseName string
	StepNum   int
	Schema    string
}

func NewMockPhaseEngine() *MockPhaseEngine {
	return &MockPhaseEngine{
		PhaseResponses: make(map[string][]MockPhaseStep),
		phaseCursors:   make(map[string]int),
		lastConsumed:   make(map[string]int),
	}
}

func (m *MockPhaseEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, target ModelTarget) (string, error) {
	messages := []inference.InferenceMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
	return m.InferMessages(ctx, messages, jsonSchema, target)
}

func (m *MockPhaseEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error) {
	// Determine which phase we're in from context or system prompt
	phaseName := ctx.Value(phaseContextKey)
	phase := ""
	if phaseName != nil {
		phase = phaseName.(string)
	}

	m.CallLog = append(m.CallLog, MockPhaseCall{
		PhaseName: phase,
		Schema:    jsonSchema,
	})

	// Pass 2 (GBNF extraction): return structured action from the step
	// that Pass 1 just consumed (lastConsumed tracks this).
	if jsonSchema != "" && strings.Contains(jsonSchema, `"tool_call"`) {
		steps := m.PhaseResponses[phase]
		consumed, ok := m.lastConsumed[phase]
		if !ok || consumed >= len(steps) {
			return `{"action":"synthesize","tool":"","arguments":{}}`, nil
		}
		step := steps[consumed]
		if step.ToolName == "" {
			return `{"action":"synthesize","tool":"","arguments":{}}`, nil
		}
		argsJSON, _ := json.Marshal(step.ToolArgs)
		return fmt.Sprintf(`{"action":"tool_call","tool":"%s","arguments":%s}`, step.ToolName, string(argsJSON)), nil
	}

	// Pass 3 Synthesis Validation Gate
	if jsonSchema != "" && strings.Contains(jsonSchema, `"ready"`) {
		return `{"ready": true}`, nil
	}

	// Pass 1 (reasoning): consume from phase-specific responses and track
	// which step was consumed so Pass 2 can extract from the same step.
	steps := m.PhaseResponses[phase]
	cursor := m.phaseCursors[phase]
	if cursor >= len(steps) {
		m.lastConsumed[phase] = cursor // out-of-range → GBNF returns synthesize
		return "<SYNTHESIZE_READY>", nil
	}
	step := steps[cursor]
	m.lastConsumed[phase] = cursor
	m.phaseCursors[phase] = cursor + 1
	return step.Reasoning, nil
}

// --- Slice 1: Tracer Bullet ---

func TestPhaseRunner_SinglePhase_ExecutesAndReturnsResult(t *testing.T) {
	engine := NewMockPhaseEngine()
	engine.PhaseResponses["test_phase"] = []MockPhaseStep{
		{Reasoning: "Final synthesis report"},
	}

	items := []QueueItem{
		{Tool: "list_dir", Args: map[string]interface{}{"path": "/tmp"}},
		{Tool: "read_file", Args: map[string]interface{}{"path": "/tmp/file1.go"}},
	}

	runner := &PhaseRunner{
		Phases: map[string]*Phase{
			"test_phase": {
				Name:         "test_phase",
				AllowedTools: []string{"list_dir", "read_file"},
				SystemPrompt: "You are exploring a codebase.",
				StepBudget:   3,
				Pass1Target:  TargetWorker,
				Driver:       NewDeterministicQueueDriver(items),
				Recovery: PhaseRecovery{
					MaxRetries:   0,
					OnExhaustion: ExhaustionSkip,
					OnError:      ErrorFail,
				},
				Transition: func(step int, result PhaseResult, err error) string {
					return ""
				},
			},
		},
		InitialPhase:   "test_phase",
		MaxCycles:      3,
		ToolDispatcher: stubToolDispatcher(),
	}

	results, err := runner.Run(context.Background(), "task_test", "probe_test", engine, engine)
	if err != nil {
		t.Fatalf("PhaseRunner.Run failed: %v", err)
	}

	// Should produce exactly 1 PhaseResult
	if len(results) != 1 {
		t.Fatalf("expected 1 PhaseResult, got %d", len(results))
	}

	result := results[0]

	// Phase name should match
	if result.PhaseName != "test_phase" {
		t.Errorf("expected PhaseName='test_phase', got %q", result.PhaseName)
	}

	// Should have used 2 steps
	if result.StepsUsed != 2 {
		t.Errorf("expected StepsUsed=2, got %d", result.StepsUsed)
	}

	// ToolsCalled should contain the tools we used
	if len(result.ToolsCalled) != 2 {
		t.Errorf("expected 2 tools called, got %d: %v", len(result.ToolsCalled), result.ToolsCalled)
	}

	// Summary should be non-empty (synthesis output)
	if result.Summary == "" {
		t.Error("expected non-empty Summary (synthesis output)")
	}
}

// TestPhaseRunner_SinglePhase_RespectsStepBudget verifies that a phase
// terminates when its step budget is exhausted, even if the engine keeps
// producing tool calls.
func TestPhaseRunner_SinglePhase_RespectsStepBudget(t *testing.T) {
	engine := NewMockPhaseEngine()
	items := []QueueItem{
		{Tool: "list_dir", Args: map[string]interface{}{"path": "/"}},
		{Tool: "list_dir", Args: map[string]interface{}{"path": "/a"}},
		{Tool: "list_dir", Args: map[string]interface{}{"path": "/a/b"}},
		{Tool: "list_dir", Args: map[string]interface{}{"path": "/a/b/c"}},
		{Tool: "list_dir", Args: map[string]interface{}{"path": "/a/b/c/d"}},
	}

	runner := &PhaseRunner{
		Phases: map[string]*Phase{
			"bounded": {
				Name:         "bounded",
				AllowedTools: []string{"list_dir"},
				SystemPrompt: "Explore.",
				StepBudget:   2, // Only allow 2 steps
				Pass1Target:  TargetRouter,
				Driver:       NewDeterministicQueueDriver(items),
				Recovery: PhaseRecovery{
					OnExhaustion: ExhaustionSkip,
					OnError:      ErrorFail,
				},
				Transition: func(step int, result PhaseResult, err error) string { return "" },
			},
		},
		InitialPhase:   "bounded",
		MaxCycles:      3,
		ToolDispatcher: stubToolDispatcher(),
	}

	results, err := runner.Run(context.Background(), "task_test", "probe_budget_test", engine, engine)
	if err != nil {
		t.Fatalf("PhaseRunner.Run failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 PhaseResult, got %d", len(results))
	}

	// Should stop at budget (2)
	if results[0].StepsUsed > 2 {
		t.Errorf("expected StepsUsed ≤ 2, got %d", results[0].StepsUsed)
	}
}

// --- Slice 2: Phase Transitions ---

func TestPhaseRunner_TwoPhaseTransition_CarriesContext(t *testing.T) {
	engine := NewMockPhaseEngine()

	orientItems := []QueueItem{
		{Tool: "list_dir", Args: map[string]interface{}{"path": "/project"}},
	}
	discoverItems := []QueueItem{
		{Tool: "read_file", Args: map[string]interface{}{"path": "/project/README.md"}},
	}

	orientTransitioned := false
	runner := &PhaseRunner{
		Phases: map[string]*Phase{
			"orient": {
				Name:         "orient",
				AllowedTools: []string{"list_dir", "search_files"},
				SystemPrompt: "Orient: scan top-level structure.",
				StepBudget:   3,
				Pass1Target:  TargetRouter,
				Driver:       NewDeterministicQueueDriver(orientItems),
				Recovery: PhaseRecovery{
					OnExhaustion: ExhaustionSkip,
					OnError:      ErrorFail,
				},
				Transition: func(step int, result PhaseResult, err error) string {
					for _, tool := range result.ToolsCalled {
						if tool == "list_dir" {
							orientTransitioned = true
							return "discover"
						}
					}
					return ""
				},
			},
			"discover": {
				Name:         "discover",
				AllowedTools: []string{"list_dir", "search_files", "read_file"},
				SystemPrompt: "Discover: read key files.",
				StepBudget:   5,
				Pass1Target:  TargetRouter,
				Driver:       NewDeterministicQueueDriver(discoverItems),
				Recovery: PhaseRecovery{
					OnExhaustion: ExhaustionSkip,
					OnError:      ErrorFail,
				},
				Transition: func(step int, result PhaseResult, err error) string {
					return "" // terminal
				},
			},
		},
		InitialPhase:   "orient",
		MaxCycles:      3,
		ToolDispatcher: stubToolDispatcher(),
	}

	results, err := runner.Run(stubDispatchCtx(), "task_test", "probe_transition_test", engine, engine)
	if err != nil {
		t.Fatalf("PhaseRunner.Run failed: %v", err)
	}

	// Should produce 2 PhaseResults (orient + discover)
	if len(results) != 2 {
		t.Fatalf("expected 2 PhaseResults, got %d", len(results))
	}

	// Verify orient transitioned
	if !orientTransitioned {
		t.Error("expected orient phase to trigger transition to discover")
	}

	// Verify phase names
	if results[0].PhaseName != "orient" {
		t.Errorf("expected first phase='orient', got %q", results[0].PhaseName)
	}
	if results[1].PhaseName != "discover" {
		t.Errorf("expected second phase='discover', got %q", results[1].PhaseName)
	}

	// Orient's summary should be non-empty
	if results[0].Summary == "" {
		t.Error("expected orient phase summary to be non-empty")
	}

	// Discover's summary should be non-empty (it received orient's context)
	if results[1].Summary == "" {
		t.Error("expected discover phase summary to be non-empty")
	}
}

// TestPhaseRunner_ThreePhaseSequential tests a simple linear pipeline
// with no backtracking: orient → discover → synthesize.
func TestPhaseRunner_ThreePhaseSequential(t *testing.T) {
	engine := NewMockPhaseEngine()

	orientItems := []QueueItem{
		{Tool: "list_dir", Args: map[string]interface{}{"path": "/"}},
	}
	discoverItems := []QueueItem{
		{Tool: "read_file", Args: map[string]interface{}{"path": "/a"}},
	}

	runner := &PhaseRunner{
		Phases: map[string]*Phase{
			"orient": {
				Name: "orient", AllowedTools: []string{"list_dir"}, SystemPrompt: "Orient.",
				StepBudget: 2, Pass1Target: TargetRouter,
				Driver:     NewDeterministicQueueDriver(orientItems),
				Recovery:   PhaseRecovery{OnExhaustion: ExhaustionSkip, OnError: ErrorFail},
				Transition: func(step int, result PhaseResult, err error) string {
					if len(result.ToolsCalled) > 0 {
						return "discover"
					}
					return ""
				},
			},
			"discover": {
				Name: "discover", AllowedTools: []string{"read_file"}, SystemPrompt: "Discover.",
				StepBudget: 3, Pass1Target: TargetRouter,
				Driver:     NewDeterministicQueueDriver(discoverItems),
				Recovery:   PhaseRecovery{OnExhaustion: ExhaustionSkip, OnError: ErrorFail},
				Transition: func(step int, result PhaseResult, err error) string {
					if len(result.ToolsCalled) > 0 {
						return "synthesize"
					}
					return ""
				},
			},
			"synthesize": {
				Name: "synthesize", AllowedTools: []string{}, SystemPrompt: "Synthesize findings.",
				StepBudget: 1, Pass1Target: TargetWorker,
				Driver:     NewDeterministicQueueDriver(nil),
				Recovery:   PhaseRecovery{OnExhaustion: ExhaustionSkip, OnError: ErrorFail},
				Transition: func(step int, result PhaseResult, err error) string { return "" },
			},
		},
		InitialPhase:   "orient",
		MaxCycles:      3,
		ToolDispatcher: stubToolDispatcher(),
	}

	results, err := runner.Run(stubDispatchCtx(), "task_test", "probe_3phase_test", engine, engine)
	if err != nil {
		t.Fatalf("PhaseRunner.Run failed: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 PhaseResults, got %d", len(results))
	}

	names := []string{results[0].PhaseName, results[1].PhaseName, results[2].PhaseName}
	expected := []string{"orient", "discover", "synthesize"}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("phase %d: expected %q, got %q", i, expected[i], name)
		}
	}
}

// --- Slice 3: Error Recovery ---

// TestPhaseRunner_ExhaustionSkip_SkipsPhaseOnBudgetExhaustion verifies that
// a phase with OnExhaustion=Skip produces a result and the runner terminates
// cleanly (no error) since there's no transition to a next phase.
func TestPhaseRunner_ExhaustionSkip_SkipsPhaseOnBudgetExhaustion(t *testing.T) {
	engine := NewMockPhaseEngine()
	items := []QueueItem{
		{Tool: "list_dir", Args: map[string]interface{}{"path": "/"}},
		{Tool: "list_dir", Args: map[string]interface{}{"path": "/a"}},
		{Tool: "list_dir", Args: map[string]interface{}{"path": "/b"}},
	}

	runner := &PhaseRunner{
		Phases: map[string]*Phase{
			"orient": {
				Name: "orient", AllowedTools: []string{"list_dir"}, SystemPrompt: "Orient.",
				StepBudget: 2, Pass1Target: TargetRouter,
				Driver:     NewDeterministicQueueDriver(items),
				Recovery:   PhaseRecovery{OnExhaustion: ExhaustionSkip, OnError: ErrorFail},
				Transition: func(step int, result PhaseResult, err error) string {
					// Never triggers — budget exhausts first
					if len(result.ToolsCalled) >= 5 {
						return "done"
					}
					return ""
				},
			},
		},
		InitialPhase:   "orient",
		MaxCycles:      3,
		ToolDispatcher: stubToolDispatcher(),
	}

	results, err := runner.Run(stubDispatchCtx(), "task_test", "probe_skip_test", engine, engine)
	if err != nil {
		t.Fatalf("PhaseRunner.Run failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 PhaseResult, got %d", len(results))
	}

	if results[0].StepsUsed != 2 {
		t.Errorf("expected StepsUsed=2, got %d", results[0].StepsUsed)
	}
}

// TestPhaseRunner_ExhaustionFail_ReturnsError verifies that a phase with
// OnExhaustion=Fail returns an error when budget is exhausted.
func TestPhaseRunner_ExhaustionFail_ReturnsError(t *testing.T) {
	engine := NewMockPhaseEngine()
	items := []QueueItem{
		{Tool: "list_dir", Args: map[string]interface{}{"path": "/"}},
		{Tool: "list_dir", Args: map[string]interface{}{"path": "/a"}},
	}

	runner := &PhaseRunner{
		Phases: map[string]*Phase{
			"critical": {
				Name: "critical", AllowedTools: []string{"list_dir"}, SystemPrompt: "Must complete.",
				StepBudget: 1, Pass1Target: TargetRouter,
				Driver:     NewDeterministicQueueDriver(items),
				Recovery: PhaseRecovery{
					OnExhaustion: ExhaustionFail,
					OnError:      ErrorFail,
				},
				Transition: func(step int, result PhaseResult, err error) string { return "" },
			},
		},
		InitialPhase: "critical",
		MaxCycles:    3,
	}

	_, err := runner.Run(context.Background(), "task_test", "probe_fail_test", engine, engine)
	if err == nil {
		t.Fatal("expected error from ExhaustionFail, got nil")
	}
	if !strings.Contains(err.Error(), "exhausted") {
		t.Errorf("expected error about budget exhaustion, got: %v", err)
	}
}

// --- Slice 4: Phase Manifest ---

func TestPhaseManifest_AssemblesFromResults(t *testing.T) {
	results := []PhaseResult{
		{PhaseName: "orient", Summary: "Found 3 dirs", ToolsCalled: []string{"list_dir"}, StepsUsed: 2, Backtracks: 0},
		{PhaseName: "discover", Summary: "Read 5 files", ToolsCalled: []string{"read_file", "read_file"}, StepsUsed: 4, Backtracks: 1},
		{PhaseName: "synthesize", Summary: "Final report", ToolsCalled: []string{}, StepsUsed: 1, Backtracks: 0},
	}

	runner := &PhaseRunner{}
	manifest := runner.BuildManifest(results)

	if len(manifest.Phases) != 3 {
		t.Fatalf("expected 3 phases in manifest, got %d", len(manifest.Phases))
	}

	if manifest.TotalStepsUsed != 7 {
		t.Errorf("expected TotalStepsUsed=7, got %d", manifest.TotalStepsUsed)
	}

	if manifest.TotalBacktracks != 1 {
		t.Errorf("expected TotalBacktracks=1, got %d", manifest.TotalBacktracks)
	}

	// Verify JSON serialization
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	if !strings.Contains(string(data), `"totalBacktracks":1`) {
		t.Errorf("manifest JSON missing totalBacktracks: %s", string(data))
	}
	if !strings.Contains(string(data), `"orient"`) {
		t.Errorf("manifest JSON missing phase name 'orient': %s", string(data))
	}
}



// --- Slice 6: Analyze Phase Template ---

func TestRunAnalyzePhases_SchemaOrientToQuery(t *testing.T) {
	engine := NewMockPhaseEngine()

	engine.PhaseResponses["schema_orient"] = []MockPhaseStep{
		{
			Reasoning:  "Introspecting cache schema.\n<ACTION>{\"tool\":\"introspect_cache\",\"arguments\":{\"cacheId\":\"cache_123\"}}</ACTION>",
			ToolName:   "introspect_cache",
			ToolArgs:   map[string]interface{}{"cacheId": "cache_123"},
			ToolResult: `{"columns": ["id", "name", "Country", "value"], "rowCount": 100}`,
		},
	}

	engine.PhaseResponses["query"] = []MockPhaseStep{
		{Reasoning: "q1", ToolName: "query_builder", ToolArgs: map[string]interface{}{
			"cacheId": "cache_123",
			"operations": []interface{}{
				map[string]interface{}{"type": "group_by", "column": "Country"},
				map[string]interface{}{"type": "aggregate", "function": "COUNT", "alias": "count"},
			},
		}, ToolResult: `[{"Country":"USA","count":42},{"Country":"UK","count":18}]`},
	}

	engine.PhaseResponses["synthesize"] = []MockPhaseStep{}

	config := compiler.ProbeConfig{
		Goal:         "Count leads by country",
		AllowedTools: []string{"introspect_cache", "query_builder"},
		StepBudget:   15,
		SourceHint:   "cache",
	}

	result, err := RunAnalyzePhases(stubDispatchCtx(), "task_analyze", "analyze_test", config, engine, engine, nil)
	if err != nil {
		t.Fatalf("RunAnalyzePhases failed: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty synthesis")
	}
}


// --- Slice 7: Research Phase Template ---

func TestRunResearchPhases_SearchToRankToDeepRead(t *testing.T) {
	engine := NewMockPhaseEngine()

	engine.PhaseResponses["search"] = []MockPhaseStep{
		{
			Reasoning:  "Searching for AI frameworks.\n<ACTION>{\"tool\":\"web_search\",\"arguments\":{\"query\":\"best AI frameworks 2026\"}}</ACTION>",
			ToolName:   "web_search",
			ToolArgs:   map[string]interface{}{"query": "best AI frameworks 2026"},
			ToolResult: `[{"url":"https://example.com/ai","title":"AI Guide"}]`,
		},
	}

	engine.PhaseResponses["rank"] = []MockPhaseStep{
		// No tools — rank phase is synthesis-only
	}

	engine.PhaseResponses["deep_read"] = []MockPhaseStep{
		{Reasoning: "b1", ToolName: "web_browse", ToolArgs: map[string]interface{}{"url": "https://example.com/ai"}, ToolResult: "AI frameworks article content"},
		{Reasoning: "b2", ToolName: "web_browse", ToolArgs: map[string]interface{}{"url": "https://example.com/ml"}, ToolResult: "ML frameworks comparison"},
		{Reasoning: "b3", ToolName: "web_browse", ToolArgs: map[string]interface{}{"url": "https://example.com/dl"}, ToolResult: "Deep learning overview"},
	}

	engine.PhaseResponses["cross_ref"] = []MockPhaseStep{
		{Reasoning: "cr1", ToolName: "web_search", ToolArgs: map[string]interface{}{"query": "verify AI claim"}, ToolResult: "verification results"},
	}

	engine.PhaseResponses["synthesize"] = []MockPhaseStep{}

	config := compiler.ProbeConfig{
		Goal:         "Research the latest AI framework trends and compare top options",
		AllowedTools: []string{"web_search", "web_browse"},
		StepBudget:   20,
		SourceHint:   "web",
	}

	result, err := RunResearchPhases(stubDispatchCtx(), "task_research", "research_test", config, engine, engine, nil)
	if err != nil {
		t.Fatalf("RunResearchPhases failed: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty research synthesis")
	}
}
