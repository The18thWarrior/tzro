package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"tzro/internal/config"
)

// ── Slice 1: StructuralPreCheck tests ────────────────────────────────────────

func TestStructuralPreCheck_EmptySynthesis(t *testing.T) {
	result, reason := StructuralPreCheck("")
	if result != "failed" {
		t.Errorf("expected 'failed', got %q", result)
	}
	if reason == "" {
		t.Error("expected non-empty reason for empty synthesis")
	}
}

func TestStructuralPreCheck_GenerationAborted(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"marker at start", "[GENERATION_ABORTED] Output was cut short due to token limit"},
		{"marker in middle", "Some preamble\n[GENERATION_ABORTED]\nMore text"},
		{"marker only", "[GENERATION_ABORTED]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, reason := StructuralPreCheck(tt.input)
			if result != "failed" {
				t.Errorf("expected 'failed', got %q", result)
			}
			if reason == "" {
				t.Error("expected non-empty reason for aborted generation")
			}
		})
	}
}

func TestStructuralPreCheck_DegenerateRepetition(t *testing.T) {
	repeated := ""
	for i := 0; i < 20; i++ {
		repeated += "the quick brown fox jumps over the lazy dog. "
	}

	result, reason := StructuralPreCheck(repeated)
	if result != "failed" {
		t.Errorf("expected 'failed' for repetitive content, got %q", result)
	}
	if reason == "" {
		t.Error("expected non-empty reason for repetitive content")
	}
}

func TestStructuralPreCheck_TooShort(t *testing.T) {
	result, reason := StructuralPreCheck("Hi")
	if result != "failed" {
		t.Errorf("expected 'failed' for too-short synthesis, got %q", result)
	}
	if reason == "" {
		t.Error("expected non-empty reason for too-short synthesis")
	}
}

func TestStructuralPreCheck_ValidSynthesis(t *testing.T) {
	valid := `## Security Advisory Analysis

The following CVEs affect Go standard library packages in 2024:

1. **CVE-2024-24790** - net/netip: Unexpected behavior with IPv4-mapped IPv6 addresses
   - Severity: HIGH (CVSS 9.8)
   - Affected versions: Go 1.21.x before 1.21.11, Go 1.22.x before 1.22.4
   - Fix: Upgrade to Go 1.21.11 or 1.22.4

2. **CVE-2024-24789** - archive/zip: Incorrect handling of certain ZIP files
   - Severity: MEDIUM (CVSS 5.5)
   - Affected versions: All Go versions before 1.21.11 and 1.22.4
   - Fix: Upgrade to Go 1.21.11 or 1.22.4`

	result, reason := StructuralPreCheck(valid)
	if result != "passed" {
		t.Errorf("expected 'passed', got %q (reason: %s)", result, reason)
	}
	if reason != "" {
		t.Errorf("expected empty reason for valid synthesis, got %q", reason)
	}
}

func TestStructuralPreCheck_MetaCommentaryDegeneration(t *testing.T) {
	meta := "The synthesis is complete. The answer is done. The engine is finished. " +
		"The task is complete. The goal has been met. The synthesis is final. " +
		"The answer is ready. The engine is closed. The synthesis is done. " +
		"The answer is finished. The engine has completed. The goal has been fulfilled."

	result, reason := StructuralPreCheck(meta)
	if result != "failed" {
		t.Errorf("expected 'failed' for degenerate meta-commentary, got %q", result)
	}
	if reason == "" {
		t.Error("expected non-empty reason for degenerate meta-commentary")
	}
}

func TestStructuralPreCheck_PlanningMetaCommentaryPassesPreCheck(t *testing.T) {
	meta := `I'll now analyze the codebase to find the relevant information.
Let me search through the files to understand the architecture.
I need to look at the internal packages to map dependencies.
First, I'll read the main entry point to understand the flow.
Then I'll examine the compiler and executor for DAG compilation logic.`

	result, _ := StructuralPreCheck(meta)
	if result != "passed" {
		t.Errorf("planning meta-commentary should pass structural pre-check (caught by cloud), got %q", result)
	}
}

func TestVerificationResult_ZeroValue(t *testing.T) {
	var r VerificationResult
	if r.Accepted {
		t.Error("zero-value VerificationResult should not be accepted")
	}
	if r.PreCheckResult != "" {
		t.Error("zero-value PreCheckResult should be empty")
	}
}

// ── FM1 Meta-Response Detection tests ────────────────────────────────────────

func TestStructuralPreCheck_MetaResponseDetection(t *testing.T) {
	// FM1: Output dominated by meta-response patterns should fail
	tests := []struct {
		name  string
		input string
	}{
		{
			"sure_here_is",
			"Sure! Here is the documentation you requested. I have carefully analyzed the codebase and prepared a comprehensive overview. " +
				"I generated the documentation covering all the key areas. I have also included examples for each section. " +
				"I have prepared detailed explanations of the architecture. I have compiled the relevant information from multiple sources. " +
				"The documentation is ready for your review. I have written the analysis as requested.",
		},
		{
			"i_generated_the_report",
			"I generated the report as requested. I have analyzed the data and created a summary. " +
				"I have prepared the market analysis document. I created the overview of trends. " +
				"I have compiled the findings from multiple sources. I have written the conclusions. " +
				"I prepared the final recommendations section. I have generated all sections.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, reason := StructuralPreCheck(tt.input)
			if result != "failed" {
				t.Errorf("expected 'failed' for meta-response output, got %q (reason: %s)", result, reason)
			}
			if !strings.Contains(reason, "meta_response") {
				t.Errorf("expected reason to contain 'meta_response', got %q", reason)
			}
		})
	}
}

func TestStructuralPreCheck_MetaResponseWithContent(t *testing.T) {
	// FM1: Meta-response preamble followed by real content → pre-check should still pass
	// because the existing validateSynthesisOutput doesn't catch this pattern,
	// and real content is present
	input := "Sure! Here is the documentation you requested.\n\n" +
		"# Architecture Overview\n\n" +
		"The system uses a DAG-based execution model with three primary components:\n" +
		"the compiler transforms abstract graphs into topologically-sorted layers,\n" +
		"the executor processes each layer dispatching tool calls, and the inference\n" +
		"engine provides both local and cloud model access for reasoning tasks.\n\n" +
		"## Compiler\n\nThe Kahn Compiler performs topological sorting of the abstract graph."

	result, reason := StructuralPreCheck(input)
	if result != "passed" {
		t.Errorf("meta-response with real content should pass (content dominates), got %q (reason: %s)", result, reason)
	}
}

func TestStructuralPreCheck_NormalOutputNoRegression(t *testing.T) {
	// Verify that normal synthesis content with no meta-response patterns still passes
	normal := `# Security Advisory Analysis

The following vulnerabilities were identified in the Go standard library:

1. **CVE-2024-24790** - net/netip: Unexpected behavior with IPv4-mapped IPv6 addresses
   - Severity: HIGH (CVSS 9.8)
   - Affected: Go 1.21.x before 1.21.11

2. **CVE-2024-24789** - archive/zip: Incorrect handling of certain ZIP files
   - Severity: MEDIUM (CVSS 5.5)
   - Affected: All Go versions before 1.21.11

3. **CVE-2024-24791** - net/http: HTTP/2 flow control vulnerability
   - Severity: HIGH (CVSS 7.5)
   - Affected: Go 1.22.x before 1.22.5`

	result, reason := StructuralPreCheck(normal)
	if result != "passed" {
		t.Errorf("normal synthesis should pass, got %q (reason: %s)", result, reason)
	}
}

// ── Slice 2: CloudVerifier tests ─────────────────────────────────────────────

// mockCloudVerifier is a test double for CloudVerifier.
type mockCloudVerifier struct {
	callCount     int
	reSynthCount  int
	lastGoal      string
	lastSynth     string
	lastCtx       string
	reSynthGoal   string
	reSynthCtx    string
	reSynthReason string
	result        *VerificationResult
	reSynthesis   string
	err           error
	reSynthErr    error
}

func (m *mockCloudVerifier) Verify(ctx context.Context, goal, synthesis, refinedContext string) (*VerificationResult, error) {
	m.callCount++
	m.lastGoal = goal
	m.lastSynth = synthesis
	m.lastCtx = refinedContext
	return m.result, m.err
}

func (m *mockCloudVerifier) VerifyMilestone(ctx context.Context, stepObjective, synthesis, refinedContext string) (*VerificationResult, error) {
	m.callCount++
	m.lastGoal = stepObjective
	m.lastSynth = synthesis
	m.lastCtx = refinedContext
	return m.result, m.err
}

func (m *mockCloudVerifier) ReSynthesize(ctx context.Context, goal, fullContext, synthesis, reason string) (string, error) {
	m.reSynthCount++
	m.reSynthGoal = goal
	m.reSynthCtx = fullContext
	m.reSynthReason = reason
	if m.reSynthErr != nil {
		return "", m.reSynthErr
	}
	if m.reSynthesis != "" {
		return m.reSynthesis, nil
	}
	if m.result != nil && m.result.ReSynthesis != "" {
		return m.result.ReSynthesis, nil
	}
	return "Mock cloud re-synthesis from full context", nil
}

func TestCloudVerifier_AcceptReturnsResult(t *testing.T) {
	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:         true,
			GoalAlignment:    0.95,
			FactualGrounding: 0.90,
			Coherence:        0.95,
			Completeness:     0.90,
			Reason:           "Output addresses all aspects of the goal",
		},
	}

	result, err := mock.Verify(context.Background(), "test goal", "test synthesis", "test context")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Accepted {
		t.Error("expected accepted result")
	}
	if result.GoalAlignment != 0.95 {
		t.Errorf("expected goalAlignment 0.95, got %f", result.GoalAlignment)
	}
}

func TestCloudVerifier_RejectReturnsReSynthesis(t *testing.T) {
	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:         false,
			GoalAlignment:    0.3,
			FactualGrounding: 0.2,
			Coherence:        0.8,
			Completeness:     0.4,
			Reason:           "Output contains hallucinated URLs",
			ReSynthesis:      "## Corrected Security Advisory\n\nThe correct CVEs are...",
		},
	}

	result, err := mock.Verify(context.Background(), "test goal", "bad synthesis", "test context")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Accepted {
		t.Error("expected rejected result")
	}
	if result.ReSynthesis == "" {
		t.Error("expected non-empty reSynthesis on rejection")
	}
}

// ── Slice 3: VerifyTaskOutput pipeline tests ─────────────────────────────────

const validTestSynthesis = `## Architecture Overview

The system follows a DAG-based execution model with three primary components:
compiler, executor, and inference engine. The compiler transforms abstract
graphs into topologically-sorted execution layers. The executor processes
each layer, dispatching tool calls and collecting results. The inference
engine provides both local and cloud model access for reasoning tasks.`

// enableCloudForTest sets ModelMode to "cooperative" so cloud verification is not blocked.
// Returns a cleanup function to restore the original value.
func enableCloudForTest(t *testing.T) {
	t.Helper()
	oldMode := config.GlobalConfig.ModelMode
	config.GlobalConfig.ModelMode = "cooperative"
	t.Cleanup(func() { config.GlobalConfig.ModelMode = oldMode })
}

func TestVerifyTaskOutput_PreCheckFail_CallsCloudForReSynthesis(t *testing.T) {
	enableCloudForTest(t)
	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:    false,
			ReSynthesis: "Cloud re-synthesis from refinedContext",
		},
	}

	finalSynthesis, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		"Explore the architecture",
		"", // empty synthesis = pre-check fail
		"The system has three modules: compiler, executor, inference.",
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PreCheckResult != "failed" {
		t.Errorf("expected pre-check 'failed', got %q", result.PreCheckResult)
	}
	if mock.reSynthCount != 1 {
		t.Errorf("expected re-synthesis called once on pre-check failure, got %d", mock.reSynthCount)
	}
	if finalSynthesis == "" {
		t.Error("expected non-empty final synthesis from cloud re-synthesis")
	}
}

func TestVerifyTaskOutput_Accepted_ReturnsOriginal(t *testing.T) {
	enableCloudForTest(t)
	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:         true,
			GoalAlignment:    0.95,
			FactualGrounding: 0.90,
			Coherence:        0.95,
			Completeness:     0.90,
			Reason:           "Output is comprehensive and accurate",
		},
	}

	finalSynthesis, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		"Explore the architecture",
		validTestSynthesis,
		"refined context here",
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Accepted {
		t.Error("expected accepted")
	}
	if result.PreCheckResult != "passed" {
		t.Errorf("expected pre-check 'passed', got %q", result.PreCheckResult)
	}
	if finalSynthesis != validTestSynthesis {
		t.Error("expected original synthesis returned on accept")
	}
}

func TestVerifyTaskOutput_Rejected_ReturnsReSynthesis(t *testing.T) {
	enableCloudForTest(t)
	reSynth := "## Corrected Architecture\n\nThe system actually uses a four-stage pipeline..."
	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:         false,
			GoalAlignment:    0.3,
			FactualGrounding: 0.2,
			Reason:           "Output contains meta-commentary instead of analysis",
			ReSynthesis:      reSynth,
		},
	}

	finalSynthesis, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		"Explore the architecture",
		validTestSynthesis,
		"refined context here",
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Accepted {
		t.Error("expected rejected")
	}
	if finalSynthesis != reSynth {
		t.Errorf("expected reSynthesis returned, got %q", finalSynthesis)
	}
	if result.Source != "cloud_verification" {
		t.Errorf("expected source 'cloud_verification', got %q", result.Source)
	}
}

// TestVerifyTaskOutput_Rejected_EmptyReSynthesis_TriggersFallback validates
// that when the cloud verifier rejects but omits reSynthesis, the fallback
// re-synthesis call is attempted. In test (no API key), the fallback fails
// gracefully and the original synthesis is returned.
func TestVerifyTaskOutput_Rejected_EmptyReSynthesis_TriggersFallback(t *testing.T) {
	enableCloudForTest(t)
	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:         false,
			GoalAlignment:    0.4,
			FactualGrounding: 0.8,
			Coherence:        0.85,
			Completeness:     0.35,
			Reason:           "Missing coverage of required dimensions",
			ReSynthesis:      "", // Empty — should trigger fallback
		},
	}

	finalSynthesis, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		"Research durable execution engines",
		validTestSynthesis,
		"Temporal uses event sourcing. Restate uses journaled execution.",
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Accepted {
		t.Error("expected rejected")
	}
	// In test env (no API key), the fallback cloud call fails and we get the original back.
	// The important thing is it doesn't crash and returns gracefully.
	if finalSynthesis == "" {
		t.Error("expected non-empty finalSynthesis (should be original or fallback)")
	}
}

// TestVerifyTaskOutput_Rejected_WithReSynthesis_NoFallback confirms that when
// reSynthesis is populated, it is used directly and no fallback call is made.
func TestVerifyTaskOutput_Rejected_WithReSynthesis_NoFallback(t *testing.T) {
	enableCloudForTest(t)
	expectedReSynth := "## Complete replacement answer with all required dimensions"
	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:         false,
			GoalAlignment:    0.3,
			FactualGrounding: 0.2,
			Reason:           "Output is meta-commentary",
			ReSynthesis:      expectedReSynth,
		},
	}

	finalSynthesis, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		"Analyze the codebase",
		validTestSynthesis,
		"refined context",
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if finalSynthesis != expectedReSynth {
		t.Errorf("expected reSynthesis to be used directly, got %q", finalSynthesis)
	}
	if result.ReSynthesis != expectedReSynth {
		t.Errorf("expected result.ReSynthesis = %q, got %q", expectedReSynth, result.ReSynthesis)
	}
}

func TestVerifyTaskOutput_CloudError_ReturnsOriginalGracefully(t *testing.T) {
	enableCloudForTest(t)
	mock := &mockCloudVerifier{
		err: fmt.Errorf("cloud API returned status 500"),
	}

	finalSynthesis, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		"Explore the architecture",
		validTestSynthesis,
		"refined context here",
		false,
	)
	if err != nil {
		t.Fatalf("expected graceful degradation, got error: %v", err)
	}
	if finalSynthesis != validTestSynthesis {
		t.Error("expected original synthesis returned on cloud error")
	}
	if result.Accepted {
		t.Error("expected not-accepted on cloud error")
	}
	if !strings.Contains(result.Reason, "cloud") {
		t.Errorf("expected reason to mention cloud failure, got %q", result.Reason)
	}
}

func TestVerifyTaskOutput_PromptContainsAllSections(t *testing.T) {
	enableCloudForTest(t)
	goal := "Research AI orchestration trends"
	synthesis := validTestSynthesis
	refinedCtx := "Fact 1: Temporal has 10k GitHub stars\nFact 2: Inngest raised Series A"

	mock := &mockCloudVerifier{
		result: &VerificationResult{Accepted: true, GoalAlignment: 1.0, FactualGrounding: 1.0, Coherence: 1.0, Completeness: 1.0, Reason: "ok"},
	}

	_, _, _ = VerifyTaskOutput(context.Background(), mock, goal, synthesis, refinedCtx, false)

	if mock.lastGoal != goal {
		t.Errorf("verifier did not receive goal, got %q", mock.lastGoal)
	}
	if mock.lastSynth != synthesis {
		t.Errorf("verifier did not receive synthesis")
	}
	if mock.lastCtx != refinedCtx {
		t.Errorf("verifier did not receive refinedContext")
	}
}

// ── Slice 4: Privacy Level Gating tests ──────────────────────────────────────

func TestVerifyTaskOutput_StrictLocal_SkipsCloud(t *testing.T) {
	old := config.GlobalConfig.PrivacyLevel
	config.GlobalConfig.PrivacyLevel = "strict-local"
	defer func() { config.GlobalConfig.PrivacyLevel = old }()

	mock := &mockCloudVerifier{
		result: &VerificationResult{Accepted: true},
	}

	finalSynthesis, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		"Explore the architecture",
		validTestSynthesis,
		"refined context",
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Cloud should NOT be called
	if mock.callCount != 0 {
		t.Errorf("expected cloud verifier not called in strict-local, got %d calls", mock.callCount)
	}
	if result.Source != "local_precheck" {
		t.Errorf("expected source 'local_precheck', got %q", result.Source)
	}
	// Should return original synthesis
	if finalSynthesis != validTestSynthesis {
		t.Error("expected original synthesis returned in strict-local mode")
	}
	// Valid synthesis should pass pre-check → accepted
	if !result.Accepted {
		t.Error("valid synthesis should be accepted in strict-local mode (pre-check passes)")
	}
	if !strings.Contains(result.Reason, "privacy") {
		t.Errorf("expected reason to mention privacy, got %q", result.Reason)
	}
}

func TestVerifyTaskOutput_Hybrid_CallsCloud(t *testing.T) {
	oldPrivacy := config.GlobalConfig.PrivacyLevel
	oldMode := config.GlobalConfig.ModelMode
	config.GlobalConfig.PrivacyLevel = "hybrid"
	config.GlobalConfig.ModelMode = "cooperative"
	defer func() {
		config.GlobalConfig.PrivacyLevel = oldPrivacy
		config.GlobalConfig.ModelMode = oldMode
	}()

	mock := &mockCloudVerifier{
		result: &VerificationResult{Accepted: true, GoalAlignment: 1.0, FactualGrounding: 1.0, Coherence: 1.0, Completeness: 1.0, Reason: "ok"},
	}

	_, _, err := VerifyTaskOutput(
		context.Background(),
		mock,
		"Explore the architecture",
		validTestSynthesis,
		"refined context",
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.callCount != 1 {
		t.Errorf("expected cloud verifier called once in hybrid mode, got %d", mock.callCount)
	}
}

func TestVerifyTaskOutput_StrictLocal_FailedPreCheck_StillReturnsOriginal(t *testing.T) {
	old := config.GlobalConfig.PrivacyLevel
	config.GlobalConfig.PrivacyLevel = "strict-local"
	defer func() { config.GlobalConfig.PrivacyLevel = old }()

	mock := &mockCloudVerifier{
		result: &VerificationResult{Accepted: false, ReSynthesis: "should not appear"},
	}

	finalSynthesis, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		"Explore the architecture",
		"", // empty = pre-check fail
		"refined context",
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.callCount != 0 {
		t.Errorf("expected cloud verifier NOT called in strict-local even on pre-check fail, got %d", mock.callCount)
	}
	if result.Accepted {
		t.Error("expected not-accepted when pre-check fails in strict-local")
	}
	if finalSynthesis != "" {
		t.Errorf("expected original (empty) synthesis returned, got %q", finalSynthesis)
	}
}

// ── Slice 5: Envelope Verification field tests ──────────────────────────────

func TestEnvelope_NilVerification_OmitsKey(t *testing.T) {
	env := ExecutionEnvelope{
		Synthesis:  "test",
		TaskID:     "task-1",
		GoalPrompt: "test goal",
		Status:     "completed",
		ToolsUsed:  []string{},
		FilesRead:  []string{},
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if _, exists := parsed["verification"]; exists {
		t.Error("nil Verification should be omitted from JSON")
	}
}

func TestEnvelope_PopulatedVerification_IncludesRubric(t *testing.T) {
	env := ExecutionEnvelope{
		Synthesis:  "test synthesis",
		TaskID:     "task-2",
		GoalPrompt: "Explore architecture",
		Status:     "completed",
		ToolsUsed:  []string{},
		FilesRead:  []string{},
		Verification: &VerificationResult{
			Accepted:         true,
			GoalAlignment:    0.95,
			FactualGrounding: 0.90,
			Coherence:        0.85,
			Completeness:     0.92,
			Reason:           "Output addresses all aspects",
			PreCheckResult:   "passed",
			Source:           "cloud_verification",
		},
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	v, exists := parsed["verification"]
	if !exists {
		t.Fatal("populated Verification should be present in JSON")
	}

	vMap, ok := v.(map[string]interface{})
	if !ok {
		t.Fatal("verification should be a JSON object")
	}

	if accepted, ok := vMap["accepted"].(bool); !ok || !accepted {
		t.Error("expected accepted=true in verification JSON")
	}
	if ga, ok := vMap["goalAlignment"].(float64); !ok || ga != 0.95 {
		t.Errorf("expected goalAlignment=0.95, got %v", vMap["goalAlignment"])
	}
	if source, ok := vMap["source"].(string); !ok || source != "cloud_verification" {
		t.Errorf("expected source='cloud_verification', got %v", vMap["source"])
	}
}

// ── Slice: Item-Level Scatter signal tests (ADR-0071) ──────────────────────────

func TestVerifyTaskOutput_PopulatesScatterItems_WhenCoverageMissing(t *testing.T) {
	enableCloudForTest(t)
	goal := `Research these topics:
1. Kubernetes deployment patterns
2. Service mesh architectures
3. Container orchestration strategies`

	// Synthesis only covers Kubernetes, missing the other two
	synthesis := `## Research Findings

### Kubernetes Deployment Patterns
Kubernetes supports multiple deployment strategies including rolling updates,
blue-green deployments, and canary releases. Rolling updates are the default
strategy and gradually replace old pods with new ones.`

	mock := &mockCloudVerifier{
		result: &VerificationResult{Accepted: true},
	}

	_, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		goal,
		synthesis,
		"refined context with all three topics",
		false, // scatterAttempted=false
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT call cloud — scatter returns early
	if mock.callCount != 0 {
		t.Errorf("expected cloud verifier not called when scatter requested, got %d", mock.callCount)
	}

	// Should have scatter items for the missing topics
	if len(result.ScatterItems) == 0 {
		t.Fatal("expected non-empty ScatterItems")
	}
	if result.Source != "scatter_needed" {
		t.Errorf("expected source 'scatter_needed', got %q", result.Source)
	}

	// Verify the missing items are the right ones
	missingGoals := make(map[string]bool)
	for _, spec := range result.ScatterItems {
		missingGoals[spec.GoalItem] = true
	}
	if !missingGoals["Service mesh architectures"] {
		t.Error("expected 'Service mesh architectures' in scatter items")
	}
	if !missingGoals["Container orchestration strategies"] {
		t.Error("expected 'Container orchestration strategies' in scatter items")
	}
}

func TestVerifyTaskOutput_SkipsScatter_WhenScatterAttempted(t *testing.T) {
	enableCloudForTest(t)
	goal := `Research these topics:
1. Kubernetes deployment patterns
2. Service mesh architectures
3. Container orchestration strategies`

	// Same synthesis that only covers Kubernetes
	synthesis := `## Research Findings

### Kubernetes Deployment Patterns
Kubernetes supports multiple deployment strategies including rolling updates,
blue-green deployments, and canary releases. Rolling updates are the default
strategy and gradually replace old pods with new ones.`

	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:         true,
			GoalAlignment:    0.7,
			FactualGrounding: 0.9,
			Coherence:        0.9,
			Completeness:     0.5,
			Reason:           "partially complete",
		},
	}

	_, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		goal,
		synthesis,
		"refined context with all three topics",
		true, // scatterAttempted=true
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should call cloud — scatter already attempted, proceed normally
	if mock.callCount != 1 {
		t.Errorf("expected cloud verifier called once when scatter already attempted, got %d", mock.callCount)
	}

	// Should NOT have scatter items
	if len(result.ScatterItems) != 0 {
		t.Errorf("expected empty ScatterItems when scatter already attempted, got %d", len(result.ScatterItems))
	}
}

func TestVerifyTaskOutput_NoScatter_WhenStructuralFails(t *testing.T) {
	enableCloudForTest(t)
	goal := `Research these topics:
1. Kubernetes deployment patterns
2. Service mesh architectures`

	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:    false,
			ReSynthesis: "Cloud re-synthesis",
		},
	}

	// Empty synthesis → structural pre-check fails → no scatter
	_, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		goal,
		"", // empty = structural fail
		"refined context",
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT have scatter items — structural failure bypasses coverage
	if len(result.ScatterItems) != 0 {
		t.Errorf("expected no scatter when structural pre-check fails, got %d items", len(result.ScatterItems))
	}
	if result.PreCheckResult != "failed" {
		t.Errorf("expected pre-check 'failed', got %q", result.PreCheckResult)
	}
}

func TestVerifyTaskOutput_NoScatter_WhenNoItemList(t *testing.T) {
	enableCloudForTest(t)
	// Free-form goal with no numbered/bulleted items
	goal := "Explain the overall architecture of the system"

	synthesis := `## Architecture Overview

The system follows a DAG-based execution model with three primary components:
compiler, executor, and inference engine. The compiler transforms abstract
graphs into topologically-sorted execution layers.`

	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:         true,
			GoalAlignment:    0.95,
			FactualGrounding: 0.90,
			Coherence:        0.95,
			Completeness:     0.90,
			Reason:           "Output is comprehensive",
		},
	}

	_, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		goal,
		synthesis,
		"refined context",
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No item list → no coverage check → no scatter
	if len(result.ScatterItems) != 0 {
		t.Errorf("expected no scatter for free-form goal, got %d items", len(result.ScatterItems))
	}
}

// ── Slice 4: Two-Tier VTE & Context Pruning tests ───────────────────────────

func TestPruneContextForVerification_GoCodeSkeleton(t *testing.T) {
	raw := "### File: internal/cache/cache.go\n\n" +
		"```go\n" +
		"package cache\n\n" +
		"import (\n\t\"context\"\n\t\"fmt\"\n)\n\n" +
		"// CacheStore manages caching.\ntype CacheStore interface {\n\tStore(ctx context.Context, key string) error\n}\n\n" +
		"// Process processes the payload.\nfunc Process(ctx context.Context, payload string) (string, error) {\n" +
		"\t// Long implementation body that should be pruned\n" +
		"\tfor i := 0; i < 100; i++ {\n\t\tfmt.Println(i)\n\t}\n\treturn payload, nil\n}\n" +
		"```\n"

	pruned := PruneContextForVerification(raw, 500)
	if len(pruned) >= len(raw) {
		t.Errorf("expected pruned context to be smaller than raw (raw=%d, pruned=%d)", len(raw), len(pruned))
	}
	if !strings.Contains(pruned, "CacheStore") {
		t.Error("expected type declaration CacheStore to be retained")
	}
	if !strings.Contains(pruned, "Process") {
		t.Error("expected exported function Process signature to be retained")
	}
}

func TestPruneContextForVerification_HardBudget(t *testing.T) {
	huge := strings.Repeat("Some long analytical observation line with facts. ", 500) // ~25,000 chars
	pruned := PruneContextForVerification(huge, 1000)
	if len(pruned) > 1050 {
		t.Errorf("expected pruned context <= ~1000 chars, got %d", len(pruned))
	}
}

func TestVerifyTaskOutput_TwoTier_AcceptedSkipsReSynthesis(t *testing.T) {
	enableCloudForTest(t)
	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:         true,
			GoalAlignment:    0.95,
			FactualGrounding: 0.90,
			Coherence:        0.95,
			Completeness:     0.90,
			Reason:           "All requirements satisfied",
		},
	}

	finalSynth, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		"Test Goal",
		validTestSynthesis,
		"Full exploration context with lots of facts",
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Accepted {
		t.Error("expected accepted")
	}
	if mock.callCount != 1 {
		t.Errorf("expected 1 Verify call, got %d", mock.callCount)
	}
	if mock.reSynthCount != 0 {
		t.Errorf("expected 0 ReSynthesize calls when accepted, got %d", mock.reSynthCount)
	}
	if finalSynth != validTestSynthesis {
		t.Errorf("expected original synthesis, got %q", finalSynth)
	}
}

func TestVerifyTaskOutput_TwoTier_RejectedCallsReSynthesis(t *testing.T) {
	enableCloudForTest(t)
	expectedReplacement := "## Replacement Document Generated from Full Context"
	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:         false,
			GoalAlignment:    0.3,
			FactualGrounding: 0.2,
			Reason:           "Quality failure: missing symbols",
		},
		reSynthesis: expectedReplacement,
	}

	fullContext := "Full exploration context with all function definitions"
	finalSynth, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		"Document the functions",
		validTestSynthesis,
		fullContext,
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Accepted {
		t.Error("expected rejected")
	}
	if mock.callCount != 1 {
		t.Errorf("expected 1 Verify call, got %d", mock.callCount)
	}
	if mock.reSynthCount != 1 {
		t.Errorf("expected 1 ReSynthesize call on rejection, got %d", mock.reSynthCount)
	}
	if mock.reSynthCtx != fullContext {
		t.Errorf("expected ReSynthesize to receive full unpruned context, got %q", mock.reSynthCtx)
	}
	if finalSynth != expectedReplacement {
		t.Errorf("expected final synthesis to be re-synthesis output, got %q", finalSynth)
	}
}

func TestVerifyTaskOutput_TwoTier_ReExploreSkipsReSynthesis(t *testing.T) {
	enableCloudForTest(t)
	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:      false,
			ReExplore:     true,
			ReExploreHint: "Read query.go to find missing query methods",
			Reason:        "Missing query functions",
		},
	}

	_, result, err := VerifyTaskOutput(
		context.Background(),
		mock,
		"Document the functions",
		validTestSynthesis,
		"Exploration context",
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.ReExplore {
		t.Error("expected re-explore signaled")
	}
	if mock.reSynthCount != 0 {
		t.Errorf("expected ReSynthesize skipped when reExplore is signaled, got %d calls", mock.reSynthCount)
	}
}

// ── Slice 5: Milestone Verification & Sink-Aware Re-Synthesis (ADR-0079) ───

func TestVerifyTaskOutput_MilestoneMode_AcceptsValidSubGoal(t *testing.T) {
	enableCloudForTest(t)
	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:            true,
			StepAlignment:       0.95,
			FactualGrounding:    0.90,
			DownstreamViability: 0.90,
			Reason:              "Core layer findings fully extracted",
		},
	}

	finalSynth, result, err := VerifyTaskOutputWithOptions(
		context.Background(),
		mock,
		"Explore core layer files",
		validTestSynthesis,
		"Core layer context",
		VerificationOpts{
			Mode: ModeMilestone,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Accepted {
		t.Errorf("expected milestone to be accepted, got false (reason: %s)", result.Reason)
	}
	if mock.callCount != 1 {
		t.Errorf("expected VerifyMilestone to be called once, got %d", mock.callCount)
	}
	if finalSynth != validTestSynthesis {
		t.Errorf("expected accepted synthesis returned as-is")
	}
}

func TestVerifyTaskOutput_MilestoneMode_SkipsReSynthesis_WhenNoToolSink(t *testing.T) {
	enableCloudForTest(t)
	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:            false,
			StepAlignment:       0.40,
			FactualGrounding:    0.70,
			DownstreamViability: 0.50,
			Reason:              "Imperfect phrasing",
		},
	}

	finalSynth, result, err := VerifyTaskOutputWithOptions(
		context.Background(),
		mock,
		"Explore core layer files",
		validTestSynthesis,
		"Core layer context",
		VerificationOpts{
			Mode:          ModeMilestone,
			FeedsToolSink: false, // Pure exploration fan-out!
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Accepted {
		t.Error("expected milestone to be rejected")
	}
	if mock.reSynthCount != 0 {
		t.Errorf("expected ReSynthesize SKIPPED when FeedsToolSink is false, got %d calls", mock.reSynthCount)
	}
	if finalSynth != validTestSynthesis {
		t.Errorf("expected original synthesis returned when ReSynthesize is skipped, got %q", finalSynth)
	}
}

func TestVerifyTaskOutput_MilestoneMode_CallsReSynthesis_WhenFeedsToolSink(t *testing.T) {
	enableCloudForTest(t)
	expectedCloudRewrite := "# Rewritten High Fidelity Function Index\n\nFunc A, Func B..."
	mock := &mockCloudVerifier{
		result: &VerificationResult{
			Accepted:            false,
			StepAlignment:       0.40,
			FactualGrounding:    0.70,
			DownstreamViability: 0.50,
			Reason:              "Corrupted intermediate draft",
		},
		reSynthesis: expectedCloudRewrite,
	}

	finalSynth, result, err := VerifyTaskOutputWithOptions(
		context.Background(),
		mock,
		"Extract functions for write_file",
		validTestSynthesis,
		"Core layer context",
		VerificationOpts{
			Mode:          ModeMilestone,
			FeedsToolSink: true, // Feeds write_file!
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Accepted {
		t.Error("expected milestone to be rejected")
	}
	if mock.reSynthCount != 1 {
		t.Errorf("expected ReSynthesize CALLED when FeedsToolSink is true, got %d calls", mock.reSynthCount)
	}
	if finalSynth != expectedCloudRewrite {
		t.Errorf("expected cloud rewrite returned when FeedsToolSink is true, got %q", finalSynth)
	}
}

func TestVerification_SystemPromptConstraints(t *testing.T) {
	if !strings.Contains(verificationEvaluateSystemPrompt, "top N") {
		t.Errorf("expected verification system prompt to mention 'top N' constraints")
	}
	if !strings.Contains(verificationEvaluateSystemPrompt, "lacks primary entity records") {
		t.Errorf("expected verification system prompt to mention evidence void detection")
	}
}


