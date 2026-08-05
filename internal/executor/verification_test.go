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

// ── Slice 2: CloudVerifier tests ─────────────────────────────────────────────

// mockCloudVerifier is a test double for CloudVerifier.
type mockCloudVerifier struct {
	callCount int
	lastGoal  string
	lastSynth string
	lastCtx   string
	result    *VerificationResult
	err       error
}

func (m *mockCloudVerifier) Verify(ctx context.Context, goal, synthesis, refinedContext string) (*VerificationResult, error) {
	m.callCount++
	m.lastGoal = goal
	m.lastSynth = synthesis
	m.lastCtx = refinedContext
	return m.result, m.err
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
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PreCheckResult != "failed" {
		t.Errorf("expected pre-check 'failed', got %q", result.PreCheckResult)
	}
	if mock.callCount != 1 {
		t.Errorf("expected cloud verifier called once for re-synthesis, got %d", mock.callCount)
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

	_, _, _ = VerifyTaskOutput(context.Background(), mock, goal, synthesis, refinedCtx)

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
