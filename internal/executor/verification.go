package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"tzro/internal/inference"
)

// ScatterSpec describes a single missing goal item to be addressed by
// a targeted scatter Probe Node (ADR-0071 Item-Level Scatter).
type ScatterSpec struct {
	GoalItem        string `json:"goalItem"`        // The missing item text
	ContextFilePath string `json:"contextFilePath"` // Temp file path containing refinedContext
}

// VerificationResult holds the outcome of the Verification Gate (ADR-0067).
// Populated by VerifyTaskOutput and persisted in the ExecutionEnvelope.
type VerificationResult struct {
	Accepted         bool          `json:"accepted"`
	GoalAlignment    float64       `json:"goalAlignment"`
	FactualGrounding float64       `json:"factualGrounding"`
	Coherence        float64       `json:"coherence"`
	Completeness     float64       `json:"completeness"`
	Reason           string        `json:"reason"`
	ReSynthesis      string        `json:"reSynthesis,omitempty"`
	PreCheckResult   string        `json:"structuralPreCheck"`          // "passed" | "failed"
	Source           string        `json:"source"`                      // "local_precheck" | "cloud_verification"
	ScatterItems     []ScatterSpec `json:"scatterItems,omitempty"`      // ADR-0071: missing items needing scatter probes
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
  "required": ["accepted", "goalAlignment", "factualGrounding", "coherence", "completeness", "reason", "reSynthesis"]
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
//   - Meta-response framing detection (FM1: "Sure! Here is...", "I have generated...")
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

	// Check 3 (FM1): Meta-response framing detection.
	// The 4B model's instruction-tuning creates a strong prior toward "helpful assistant"
	// responses. At 4B scale, this prior is stronger relative to the task instruction than
	// at frontier scale. Detect outputs dominated by meta-response patterns.
	if metaReason := detectMetaResponse(trimmed); metaReason != "" {
		return "failed", metaReason
	}

	// Check 4: Reuse existing validateSynthesisOutput for meta-commentary,
	// control token leaks, and repetitive content detection.
	if validationReason := validateSynthesisOutput(synthesis); validationReason != "" {
		return "failed", validationReason
	}

	return "passed", ""
}

// metaResponsePatterns are sentence-level patterns that indicate the model is
// describing what it did rather than producing the requested output (FM1).
// Distinct from metaPatterns in validateSynthesisOutput which catch completion-state
// phrases ("synthesis is complete", "engine is done").
var metaResponsePatterns = []string{
	// "I did X" patterns
	"i have generated", "i have created", "i have prepared",
	"i have analyzed", "i have compiled", "i have written",
	"i generated", "i created", "i prepared", "i analyzed",
	"i compiled", "i wrote", "i have also included",
	"i have included", "i have provided", "i provided",
	// "Here is X" patterns
	"here is the", "here are the", "here's the",
	"sure! here is", "sure, here is", "sure! here are",
	// "The X is ready" patterns
	"the documentation is ready", "the report is ready",
	"the analysis is ready", "the documentation is complete",
	"the report is complete", "the analysis is complete",
	"ready for your review", "as you requested",
}

// detectMetaResponse checks if the synthesis is dominated by meta-response framing
// patterns (FM1). Returns a reason string if detected, empty string if clean.
//
// Only triggers when meta-response sentences dominate the output (>50% and ≥4 matches).
// A single "Here is the documentation" followed by real content does NOT trigger this.
func detectMetaResponse(synthesis string) string {
	lower := strings.ToLower(synthesis)
	sentences := strings.Split(lower, ". ")
	if len(sentences) < 4 {
		return ""
	}

	metaCount := 0
	for _, sentence := range sentences {
		s := strings.TrimSpace(sentence)
		if s == "" {
			continue
		}
		for _, pattern := range metaResponsePatterns {
			if strings.Contains(s, pattern) {
				metaCount++
				break
			}
		}
	}

	metaRatio := float64(metaCount) / float64(len(sentences))
	if metaRatio > 0.5 && metaCount >= 4 {
		return fmt.Sprintf("meta_response_detected: %d/%d sentences are meta-response framing (ratio=%.0f%%)",
			metaCount, len(sentences), metaRatio*100)
	}

	return ""
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
- IMPORTANT: When setting "accepted" to false, you MUST provide a "reSynthesis" field containing a COMPLETE, COMPREHENSIVE replacement answer synthesized from the exploration context. The reSynthesis must be a full document that directly fulfills the goal — not a summary or brief note. It should be at least as long and detailed as the exploration context warrants. Never produce a short reSynthesis — the rejected output will be discarded and your reSynthesis will be used as the final output. When accepting, set reSynthesis to an empty string.

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
//  1. Stage 2: RunUnifiedValidation (local checks: structural + FM1/FM3/FM5)
//  2. Stage 3+4: CloudVerifier.Verify (cloud call for evaluation + re-synthesis)
//
// Returns the final synthesis text (original if accepted, reSynthesis if rejected)
// and the VerificationResult for Execution Envelope population.
//
// On cloud errors, degrades gracefully: returns the original synthesis with
// an error-indicating VerificationResult. Never returns an error to the caller.
func VerifyTaskOutput(ctx context.Context, verifier CloudVerifier, goal, synthesis, refinedContext string, scatterAttempted bool) (finalSynthesis string, result *VerificationResult, err error) {
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

	// Run unified validation (FM1 meta-response + FM3 content + FM5 coverage).
	unified := RunUnifiedValidation(ctx, goal, synthesis, refinedContext)

	if unified.StructuralPreCheck == "failed" {
		fmt.Fprintf(os.Stderr, "[VTE] Stage 2 pre-check FAILED: %s\n", unified.StructuralReason)

		// Pre-check failed — call cloud for direct re-synthesis.
		vResult, cloudErr := verifier.Verify(ctx, goal, synthesis, refinedContext)
		if cloudErr != nil {
			fmt.Fprintf(os.Stderr, "[VTE] Cloud re-synthesis failed: %v\n", cloudErr)
			return synthesis, &VerificationResult{
				Accepted:       false,
				PreCheckResult: "failed",
				Source:         "local_precheck",
				Reason:         fmt.Sprintf("structural pre-check failed (%s), cloud re-synthesis failed: %v", unified.StructuralReason, cloudErr),
			}, nil
		}

		vResult.PreCheckResult = "failed"
		if vResult.ReSynthesis != "" {
			return vResult.ReSynthesis, vResult, nil
		}
		return synthesis, vResult, nil
	}

	// Pre-check passed — check for coverage-based scatter opportunity (ADR-0071)
	if unified.CoverageResult != nil && len(unified.CoverageResult.Missing) > 0 {
		fmt.Fprintf(os.Stderr, "[VTE] Coverage advisory: %d/%d items missing\n",
			len(unified.CoverageResult.Missing), unified.CoverageResult.TotalRequired)

		// Item-Level Scatter: if scatter hasn't been attempted yet, signal
		// the executor to spawn targeted probes for missing items.
		if !scatterAttempted {
			var specs []ScatterSpec
			for _, item := range unified.CoverageResult.Missing {
				specs = append(specs, ScatterSpec{GoalItem: item})
			}
			fmt.Fprintf(os.Stderr, "[VTE] Scatter requested: %d missing items\n", len(specs))
			return synthesis, &VerificationResult{
				Accepted:       false,
				PreCheckResult: "passed",
				Source:         "scatter_needed",
				Reason:         fmt.Sprintf("coverage check found %d missing items, scatter requested", len(specs)),
				ScatterItems:   specs,
			}, nil
		}
	}
	if len(unified.ContentIssues) > 0 {
		fmt.Fprintf(os.Stderr, "[VTE] Content advisory: %d issues (dead URLs, fabricated quotes)\n",
			len(unified.ContentIssues))
	}

	// Run cloud verification
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

	// Defensive fallback: the cloud model rejected but did not produce a
	// reSynthesis (it omitted the optional field). Fire a dedicated second
	// call with explicit re-synthesis instructions rather than returning the
	// rejected output. This is a robustness guard, not an architectural change
	// to the single-call VTE design.
	fmt.Fprintf(os.Stderr, "[VTE] WARNING: Rejection without reSynthesis — firing fallback re-synthesis call\n")
	fallbackPrompt := fmt.Sprintf(
		"The following synthesis was REJECTED for this reason: %s\n\n"+
			"Using ONLY the exploration context below, write a complete replacement that addresses the original goal.\n\n"+
			"## GOAL\n\n%s\n\n## EXPLORATION CONTEXT\n\n%s",
		vResult.Reason, goal, refinedContext,
	)

	fallbackMessages := []inference.InferenceMessage{
		{Role: "system", Content: "You are a technical writer. Produce a comprehensive, well-structured response to the goal using only the provided exploration context. Output the response directly with no meta-commentary."},
		{Role: "user", Content: fallbackPrompt},
	}

	fallbackResponse, fallbackErr := inference.CallCloudModel(ctx, fallbackMessages, "")
	if fallbackErr != nil {
		fmt.Fprintf(os.Stderr, "[VTE] Fallback re-synthesis failed: %v — returning original rejected synthesis\n", fallbackErr)
		return synthesis, vResult, nil
	}

	if fallbackResponse != "" {
		fmt.Fprintf(os.Stderr, "[VTE] Fallback re-synthesis succeeded (%d chars)\n", len(fallbackResponse))
		vResult.ReSynthesis = fallbackResponse
		return fallbackResponse, vResult, nil
	}

	return synthesis, vResult, nil
}

