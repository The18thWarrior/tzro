package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"tzro/internal/inference"
)

// VerificationResult holds the outcome of the Verification Gate (ADR-0067).
// Populated by VerifyTaskOutput and persisted in the ExecutionEnvelope.
type VerificationResult struct {
	Accepted         bool    `json:"accepted"`
	GoalAlignment    float64 `json:"goalAlignment"`
	FactualGrounding float64 `json:"factualGrounding"`
	Coherence        float64 `json:"coherence"`
	Completeness     float64 `json:"completeness"`
	Reason           string  `json:"reason"`
	ReSynthesis      string  `json:"reSynthesis,omitempty"`
	PreCheckResult   string  `json:"structuralPreCheck"`          // "passed" | "failed"
	Source           string  `json:"source"`                      // "local_precheck" | "cloud_verification"
}

// verificationRubricSchema is the JSON schema passed to the cloud model's
// structured output mode (response_format: json_schema). Constrains token
// generation to valid JSON matching this schema, eliminating parse failures
// from unescaped characters in reSynthesis content.
const verificationRubricSchema = `{
  "type": "object",
  "properties": {
    "accepted": { "type": "boolean" },
    "goalAlignment": { "type": "number" },
    "factualGrounding": { "type": "number" },
    "coherence": { "type": "number" },
    "completeness": { "type": "number" },
    "reason": { "type": "string" },
    "reSynthesis": { "type": "string" }
  },
  "required": ["accepted", "goalAlignment", "factualGrounding", "coherence", "completeness", "reason"]
}`

// generationAbortedMarker is the marker emitted by the Generation Guard
// when output generation is terminated early.
const generationAbortedMarker = "[GENERATION_ABORTED]"

// StructuralPreCheck runs Stage 2 of Verified Task Execution: deterministic
// local checks on the synthesis output before sending to the cloud Verification Gate.
//
// Returns ("passed", "") if synthesis passes all structural checks.
// Returns ("failed", reason) if synthesis fails any check.
//
// Checks performed:
//   - Empty or too-short output (< 50 chars)
//   - Generation Guard marker detection ([GENERATION_ABORTED])
//   - Meta-commentary degeneration scoring (reuses existing validateSynthesisOutput)
//   - Repetitive content detection
func StructuralPreCheck(synthesis string) (result string, reason string) {
	trimmed := strings.TrimSpace(synthesis)

	// Check 1: Empty or too-short
	if len(trimmed) < 50 {
		if trimmed == "" {
			return "failed", "empty synthesis"
		}
		return "failed", "synthesis too short"
	}

	// Check 2: Generation Guard marker
	if strings.Contains(synthesis, generationAbortedMarker) {
		return "failed", "generation aborted"
	}

	// Check 3: Reuse existing validateSynthesisOutput for meta-commentary,
	// control token leaks, and repetitive content detection.
	if validationReason := validateSynthesisOutput(synthesis); validationReason != "" {
		return "failed", validationReason
	}

	return "passed", ""
}

// CloudVerifier abstracts the cloud inference call for testability.
// DefaultCloudVerifier is the production implementation; tests use mockCloudVerifier.
type CloudVerifier interface {
	Verify(ctx context.Context, goal, synthesis, refinedContext string) (*VerificationResult, error)
}

// DefaultCloudVerifier calls inference.CallCloudModel with the verification
// rubric schema for structured output.
type DefaultCloudVerifier struct{}

// verificationSystemPrompt is the system prompt for the cloud Verification Gate.
const verificationSystemPrompt = `You are the Verification Gate for a local AI model's output.

You will receive:
1. The GOAL the local model was trying to achieve
2. The EXPLORATION CONTEXT (facts discovered during research)
3. The LOCAL MODEL'S ATTEMPT at answering the goal

Your job:
- Evaluate the attempt against the goal and exploration context
- Score on four dimensions (0.0 to 1.0):
  - goalAlignment: Does the output address what was asked?
  - factualGrounding: Are claims supported by the exploration context?
  - coherence: Is the output well-structured and readable?
  - completeness: Does it cover all aspects of the goal?
- Set "accepted" to true if ALL scores >= 0.6
- If rejecting, produce a "reSynthesis" — a complete replacement answer using the exploration context

Be strict but fair. Accept well-structured output that addresses the goal with minor gaps. Reject output containing meta-commentary about the task, fabricated data, or missing key requirements.`

// Verify calls the cloud model to evaluate and optionally re-synthesize.
func (v *DefaultCloudVerifier) Verify(ctx context.Context, goal, synthesis, refinedContext string) (*VerificationResult, error) {
	userMessage := fmt.Sprintf(
		"## GOAL\n\n%s\n\n## EXPLORATION CONTEXT\n\n%s\n\n## LOCAL MODEL'S ATTEMPT\n\n%s",
		goal, refinedContext, synthesis,
	)

	messages := []inference.InferenceMessage{
		{Role: "system", Content: verificationSystemPrompt},
		{Role: "user", Content: userMessage},
	}

	response, err := inference.CallCloudModel(ctx, messages, verificationRubricSchema)
	if err != nil {
		return nil, fmt.Errorf("verification cloud call failed: %w", err)
	}

	var result VerificationResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("failed to parse verification response: %w", err)
	}

	result.Source = "cloud_verification"
	return &result, nil
}

// VerifyTaskOutput is the top-level entry point for Verified Task Execution (ADR-0067).
//
// Pipeline:
//  1. Stage 2: StructuralPreCheck (local, deterministic, <100ms)
//  2. Stage 3+4: CloudVerifier.Verify (cloud call for evaluation + re-synthesis)
//
// Returns the final synthesis text (original if accepted, reSynthesis if rejected)
// and the VerificationResult for Execution Envelope population.
//
// On cloud errors, degrades gracefully: returns the original synthesis with
// an error-indicating VerificationResult. Never returns an error to the caller.
func VerifyTaskOutput(ctx context.Context, verifier CloudVerifier, goal, synthesis, refinedContext string) (finalSynthesis string, result *VerificationResult, err error) {
	// ADR-0067: Audit log — VTE activates for every recall node unconditionally.
	// The only gate is privacy level (strict-local / local model mode).
	fmt.Fprintf(os.Stderr, "[VTE] Activating (goal=%d chars, synthesis=%d chars, context=%d chars)\n",
		len(goal), len(synthesis), len(refinedContext))

	// Privacy Level gate (ADR-0067): strict-local skips cloud verification.
	if isCloudEscalationBlocked() {
		preCheckResult, preCheckReason := StructuralPreCheck(synthesis)
		fmt.Fprintf(os.Stderr, "[VTE] Privacy level blocks cloud — Stage 2 only (result=%s)\n", preCheckResult)
		reason := "cloud verification skipped (privacy level)"
		if preCheckResult == "failed" {
			reason = fmt.Sprintf("structural pre-check failed (%s), cloud verification skipped (privacy level)", preCheckReason)
		}
		return synthesis, &VerificationResult{
			Accepted:       preCheckResult == "passed",
			PreCheckResult: preCheckResult,
			Source:         "local_precheck",
			Reason:         reason,
		}, nil
	}

	preCheckResult, preCheckReason := StructuralPreCheck(synthesis)

	if preCheckResult == "failed" {
		fmt.Fprintf(os.Stderr, "[VTE] Stage 2 pre-check FAILED: %s\n", preCheckReason)

		// Pre-check failed — call cloud for direct re-synthesis.
		// Pass the goal and refinedContext but note the synthesis was structurally invalid.
		vResult, cloudErr := verifier.Verify(ctx, goal, synthesis, refinedContext)
		if cloudErr != nil {
			fmt.Fprintf(os.Stderr, "[VTE] Cloud re-synthesis failed: %v\n", cloudErr)
			return synthesis, &VerificationResult{
				Accepted:       false,
				PreCheckResult: "failed",
				Source:         "local_precheck",
				Reason:         fmt.Sprintf("structural pre-check failed (%s), cloud re-synthesis failed: %v", preCheckReason, cloudErr),
			}, nil
		}

		vResult.PreCheckResult = "failed"
		if vResult.ReSynthesis != "" {
			return vResult.ReSynthesis, vResult, nil
		}
		return synthesis, vResult, nil
	}

	// Pre-check passed — run cloud verification
	fmt.Fprintf(os.Stderr, "[VTE] Stage 2 pre-check PASSED, calling cloud verification\n")

	vResult, cloudErr := verifier.Verify(ctx, goal, synthesis, refinedContext)
	if cloudErr != nil {
		fmt.Fprintf(os.Stderr, "[VTE] Cloud verification failed: %v — returning original synthesis\n", cloudErr)
		return synthesis, &VerificationResult{
			Accepted:       false,
			PreCheckResult: "passed",
			Source:         "cloud_verification",
			Reason:         fmt.Sprintf("cloud verification failed: %v", cloudErr),
		}, nil
	}

	vResult.PreCheckResult = "passed"
	vResult.Source = "cloud_verification"

	if vResult.Accepted {
		fmt.Fprintf(os.Stderr, "[VTE] ACCEPTED (goal=%.2f, fact=%.2f, cohr=%.2f, comp=%.2f)\n",
			vResult.GoalAlignment, vResult.FactualGrounding, vResult.Coherence, vResult.Completeness)
		return synthesis, vResult, nil
	}

	// Rejected — use re-synthesis if available
	fmt.Fprintf(os.Stderr, "[VTE] REJECTED: %s (goal=%.2f, fact=%.2f, cohr=%.2f, comp=%.2f)\n",
		vResult.Reason, vResult.GoalAlignment, vResult.FactualGrounding, vResult.Coherence, vResult.Completeness)

	if vResult.ReSynthesis != "" {
		fmt.Fprintf(os.Stderr, "[VTE] Using cloud re-synthesis (%d chars)\n", len(vResult.ReSynthesis))
		return vResult.ReSynthesis, vResult, nil
	}

	return synthesis, vResult, nil
}
