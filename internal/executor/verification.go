package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"tzro/internal/compactor"
	"tzro/internal/inference"
)

// ScatterSpec describes a single missing goal item to be addressed by
// a targeted scatter Probe Node (ADR-0071 Item-Level Scatter).
type ScatterSpec struct {
	GoalItem        string `json:"goalItem"`        // The missing item text
	ContextFilePath string `json:"contextFilePath"` // Temp file path containing refinedContext
}

// VerificationMode defines the operational mode of the Verification Gate (ADR-0079).
type VerificationMode string

const (
	// ModeTerminal evaluates full end-to-end task completeness against the global goal.
	ModeTerminal VerificationMode = "terminal"
	// ModeMilestone evaluates intermediate step alignment and downstream viability without global completeness penalties.
	ModeMilestone VerificationMode = "milestone"
)

// VerificationOpts specifies configuration for VerifyTaskOutputWithOptions (ADR-0079).
type VerificationOpts struct {
	Mode             VerificationMode
	FeedsToolSink    bool // When true in Milestone mode, rejection triggers immediate Cloud Re-Synthesis
	ScatterAttempted bool
}

// VerificationResult holds the outcome of the Verification Gate (ADR-0067, ADR-0079).
// Populated by VerifyTaskOutput / VerifyTaskOutputWithOptions and persisted in the ExecutionEnvelope.
type VerificationResult struct {
	Accepted            bool             `json:"accepted"`
	GoalAlignment       float64          `json:"goalAlignment,omitempty"`
	FactualGrounding    float64          `json:"factualGrounding"`
	Coherence           float64          `json:"coherence,omitempty"`
	Completeness        float64          `json:"completeness,omitempty"`
	StepAlignment       float64          `json:"stepAlignment,omitempty"`
	DownstreamViability float64          `json:"downstreamViability,omitempty"`
	Reason              string           `json:"reason"`
	ReSynthesis         string           `json:"reSynthesis,omitempty"`
	PreCheckResult      string           `json:"structuralPreCheck"`          // "passed" | "failed"
	Source              string           `json:"source"`                      // "local_precheck" | "cloud_verification"
	Mode                VerificationMode `json:"mode,omitempty"`              // "terminal" | "milestone"
	ScatterItems        []ScatterSpec    `json:"scatterItems,omitempty"`      // ADR-0071: missing items needing scatter probes
	ReExplore           bool             `json:"reExplore,omitempty"`         // When true, upstream data collection was insufficient — re-run exploration
	ReExploreHint       string           `json:"reExploreHint,omitempty"`     // Guidance for the re-explore phase (what data to collect)
}

// verificationEvaluateSchema is the JSON schema passed to the cloud model's
// structured output mode for Tier 1 Terminal evaluation.
const verificationEvaluateSchema = `{
  "type": "object",
  "properties": {
    "accepted": { "type": "boolean" },
    "goalAlignment": { "type": "number" },
    "factualGrounding": { "type": "number" },
    "coherence": { "type": "number" },
    "completeness": { "type": "number" },
    "reason": { "type": "string" },
    "reExplore": { "type": "boolean" },
    "reExploreHint": { "type": "string" }
  },
  "required": ["accepted", "goalAlignment", "factualGrounding", "coherence", "completeness", "reason"]
}`

// milestoneEvaluateSchema is the JSON schema for Tier 1 Milestone evaluation (ADR-0079).
const milestoneEvaluateSchema = `{
  "type": "object",
  "properties": {
    "accepted": { "type": "boolean" },
    "stepAlignment": { "type": "number" },
    "factualGrounding": { "type": "number" },
    "downstreamViability": { "type": "number" },
    "reason": { "type": "string" },
    "reExplore": { "type": "boolean" },
    "reExploreHint": { "type": "string" }
  },
  "required": ["accepted", "stepAlignment", "factualGrounding", "downstreamViability", "reason"]
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
	// Skip for structured data passthrough (analyze node v3) — raw JSON/tabular
	// query results would false-positive on meta-response and repetition checks.
	isStructuredData := strings.Contains(trimmed, "## Query Result") ||
		strings.Contains(trimmed, "## Cache Reference") ||
		strings.Contains(trimmed, "## Data Cache Reference") ||
		strings.Contains(trimmed, "cacheId:")
	if !isStructuredData {
		if metaReason := detectMetaResponse(trimmed); metaReason != "" {
			return "failed", metaReason
		}
	}

	// Check 4: Reuse existing validateSynthesisOutput for meta-commentary,
	// control token leaks, and repetitive content detection.
	// Pass isAnalyzeNode for structured data to skip n-gram repetition checks.
	if isStructuredData {
		if validationReason := validateSynthesisOutput(synthesis, WithAnalyzeNode()); validationReason != "" {
			return "failed", validationReason
		}
	} else {
		if validationReason := validateSynthesisOutput(synthesis); validationReason != "" {
			return "failed", validationReason
		}
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

// CloudVerifier abstracts the cloud inference calls for testability.
// DefaultCloudVerifier is the production implementation; tests use mockCloudVerifier.
type CloudVerifier interface {
	Verify(ctx context.Context, goal, synthesis, prunedContext string) (*VerificationResult, error)
	VerifyMilestone(ctx context.Context, stepObjective, synthesis, prunedContext string) (*VerificationResult, error)
	ReSynthesize(ctx context.Context, goal, fullContext, synthesis, reason string) (string, error)
}

// verificationEvaluateSystemPrompt is the system prompt for the Tier 1 Terminal evaluation gate.
const verificationEvaluateSystemPrompt = `You are the Verification Gate for a local AI model's output.

You will receive:
1. The GOAL the local model was trying to achieve
2. The EXPLORATION CONTEXT (key facts and signatures discovered during research)
3. The LOCAL MODEL'S ATTEMPT at answering the goal

Your job:
- Evaluate the attempt against the goal and exploration context
- Score on four dimensions (0.0 to 1.0):
  - goalAlignment: Does the output address what was asked? Verify that explicit constraints (e.g. selecting "top N" frameworks, specific metrics, comparison dimensions) are directly fulfilled rather than producing an unranked general list.
  - factualGrounding: Are claims supported by the exploration context? Check whether technical claims, identifiers (e.g. CVE-YYYY-NNNN, version numbers, package names), dates, and quantitative values are explicitly corroborated by the exploration context. If the attempt presents fabricated identifiers, dates outside the goal range, unverifiable numbers, or duplicate hallucinated rows as facts, factualGrounding MUST be < 0.5.
  - coherence: Is the output well-structured and readable?
  - completeness: Does it cover all aspects of the goal? If required sections, rankings, or comparison tables are missing, completeness MUST be < 0.60.
- Set "accepted" to true if ALL scores >= 0.65
- Set "reason" explaining the score justification and noting any gaps.

Be strict but fair. Accept well-structured output that addresses the goal with minor gaps. Reject output containing meta-commentary about the task, fabricated data, or missing key requirements.

RE-EXPLORE DETECTION: If the output reports tool failures or query errors, OR if the exploration context completely lacks primary entity records, security advisories, or quantitative data points needed to answer the goal (e.g. only high-level landing pages were fetched and 0 specific CVE records or benchmark numbers exist in context), set "reExplore" to true and provide a "reExploreHint" describing specific queries or sources to investigate. Re-synthesis cannot fabricate missing primary evidence.`

// milestoneEvaluateSystemPrompt is the system prompt for the Tier 1 Milestone evaluation gate (ADR-0079).
const milestoneEvaluateSystemPrompt = `You are the Milestone Verification Gate for an intermediate step in a multi-step workflow.

You will receive:
1. The STEP OBJECTIVE (the local sub-goal assigned to this step)
2. The EXPLORATION CONTEXT (evidence gathered by this step)
3. The LOCAL MODEL'S MILESTONE OUTPUT

Your job:
- Evaluate whether this milestone accomplished its specific step objective.
- Score on three dimensions (0.0 to 1.0):
  - stepAlignment: Did the output address its specific step objective?
  - factualGrounding: Are claims supported by the gathered context?
  - downstreamViability: Did it produce usable, non-empty, actionable data for downstream steps?
- Set "accepted" to true if ALL scores >= 0.60
- Set "reason" explaining the score justification.
- CRITICAL INVARIANT: Do NOT penalize or reject this output for failing to satisfy the entire final task. This is an INTERMEDIATE MILESTONE. As long as this milestone fulfilled its local step contract, accept it.

RE-EXPLORE DETECTION: If the step failed to collect data due to tool errors, 0 search results, or wrong directory exploration, set "reExplore" to true and provide "reExploreHint".`

// DefaultCloudVerifier implements CloudVerifier using the configured Cloud LLM.
type DefaultCloudVerifier struct{}

// Verify calls the cloud model to evaluate local output against goal and context (Tier 1 Terminal).
func (v *DefaultCloudVerifier) Verify(ctx context.Context, goal, synthesis, prunedContext string) (*VerificationResult, error) {
	userMessage := fmt.Sprintf(
		"## GOAL\n\n%s\n\n## EXPLORATION CONTEXT\n\n%s\n\n## LOCAL MODEL'S ATTEMPT\n\n%s",
		goal, prunedContext, synthesis,
	)

	messages := []inference.InferenceMessage{
		{Role: "system", Content: verificationEvaluateSystemPrompt},
		{Role: "user", Content: userMessage},
	}

	var response string
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		verifyCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
		response, err = inference.CallCloudModel(verifyCtx, messages, verificationEvaluateSchema)
		cancel()
		if err == nil {
			break
		}
		if attempt == 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("verification cloud call failed: %w", err)
	}

	var result VerificationResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("failed to parse verification response: %w", err)
	}

	result.Source = "cloud_verification"
	result.Mode = ModeTerminal
	return &result, nil
}

// VerifyMilestone calls the cloud model to evaluate intermediate milestone output (Tier 1 Milestone, ADR-0079).
func (v *DefaultCloudVerifier) VerifyMilestone(ctx context.Context, stepObjective, synthesis, prunedContext string) (*VerificationResult, error) {
	userMessage := fmt.Sprintf(
		"## STEP OBJECTIVE\n\n%s\n\n## EXPLORATION CONTEXT\n\n%s\n\n## LOCAL MODEL'S MILESTONE OUTPUT\n\n%s",
		stepObjective, prunedContext, synthesis,
	)

	messages := []inference.InferenceMessage{
		{Role: "system", Content: milestoneEvaluateSystemPrompt},
		{Role: "user", Content: userMessage},
	}

	var response string
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		verifyCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
		response, err = inference.CallCloudModel(verifyCtx, messages, milestoneEvaluateSchema)
		cancel()
		if err == nil {
			break
		}
		if attempt == 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("milestone verification cloud call failed: %w", err)
	}

	var result VerificationResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("failed to parse milestone verification response: %w", err)
	}

	result.Source = "cloud_verification"
	result.Mode = ModeMilestone
	return &result, nil
}

// ReSynthesize calls the cloud model to generate a complete replacement document (Tier 2).
func (v *DefaultCloudVerifier) ReSynthesize(ctx context.Context, goal, fullContext, synthesis, reason string) (string, error) {
	fallbackPrompt := fmt.Sprintf(
		"The following synthesis was REJECTED for this reason: %s\n\n"+
			"Using ONLY the exploration context below, write a complete replacement that addresses the original goal.\n\n"+
			"## GOAL\n\n%s\n\n## EXPLORATION CONTEXT\n\n%s",
		reason, goal, fullContext,
	)

	messages := []inference.InferenceMessage{
		{Role: "system", Content: "You are an expert technical writer. Produce a comprehensive, well-structured replacement document fulfilling the goal. Cite verified sources and provide complete data points. CRITICAL GROUNDING INVARIANT: If specific records, CVE IDs, or quantitative metrics are absent from the exploration context, explicitly state 'Data not found in source evidence' or 'Not reported in sources'. Do NOT estimate, invent, or hallucinate identifiers, CVEs, or numbers."},
		{Role: "user", Content: fallbackPrompt},
	}

	var response string
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		synthCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		response, err = inference.CallCloudModel(synthCtx, messages, "")
		cancel()
		if err == nil {
			break
		}
		if attempt == 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if err != nil {
		return "", fmt.Errorf("cloud re-synthesis failed: %w", err)
	}

	return response, nil
}

var codeBlockRegex = regexp.MustCompile("(?s)```([a-zA-Z0-9_-]*)\n(.*?)```")

// PruneContextForVerification reduces exploration context to high-signal facts,
// type declarations, and signatures to minimize Tier 1 cloud verification tokens.
func PruneContextForVerification(rawContext string, targetMaxChars int) string {
	if targetMaxChars <= 0 {
		targetMaxChars = 6000
	}

	// 1. Process code blocks within markdown fences (strip function bodies into signatures)
	pruned := codeBlockRegex.ReplaceAllStringFunc(rawContext, func(match string) string {
		sub := codeBlockRegex.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		lang := sub[1]
		code := sub[2]
		if len(code) > 200 {
			skeleton := compactor.ExtractSkeleton(code, 600)
			return fmt.Sprintf("```%s\n%s\n```", lang, strings.TrimSpace(skeleton))
		}
		return match
	})

	if len(pruned) <= targetMaxChars {
		return pruned
	}

	// 2. For research/evidence context: extract all evidence card lines and bullets across all URLs
	if strings.Contains(pruned, "## Evidence Card:") || strings.Contains(pruned, "## Scraped Sources") {
		var evidenceLines []string
		lines := strings.Split(pruned, "\n")
		currLen := 0
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "## Evidence Card:") || strings.HasPrefix(trimmed, "## Scraped Sources") ||
				strings.HasPrefix(trimmed, "- [") || strings.HasPrefix(trimmed, "- ") {
				if currLen+len(trimmed)+1 <= targetMaxChars {
					evidenceLines = append(evidenceLines, trimmed)
					currLen += len(trimmed) + 1
				}
			}
		}
		if len(evidenceLines) >= 5 && currLen >= 500 {
			return strings.Join(evidenceLines, "\n")
		}
	}

	// 3. Compact tool outputs / content if structured
	compacted := compactor.CompactContent(pruned, targetMaxChars)
	if len(compacted) > 0 && len(compacted) <= targetMaxChars {
		return compacted
	}
	if len(compacted) > targetMaxChars {
		pruned = compacted
	}

	// 3. Fallback: deterministic head/tail budget preservation
	headBudget := (targetMaxChars * 2) / 3
	tailBudget := targetMaxChars - headBudget - 60
	if tailBudget > 0 && len(pruned) > headBudget+tailBudget {
		return pruned[:headBudget] + "\n\n... [intermediate context pruned for verification] ...\n\n" + pruned[len(pruned)-tailBudget:]
	}
	if len(pruned) > targetMaxChars {
		return pruned[:targetMaxChars-25] + "\n... [truncated] ..."
	}
	if len(pruned) == 0 && len(rawContext) > 0 {
		if len(rawContext) > targetMaxChars {
			return rawContext[:targetMaxChars-25] + "\n... [truncated] ..."
		}
		return rawContext
	}

	return pruned
}

// VerifyTaskOutput is the legacy entry point for Verified Task Execution (ADR-0067).
// Runs in ModeTerminal mode.
func VerifyTaskOutput(ctx context.Context, verifier CloudVerifier, goal, synthesis, refinedContext string, scatterAttempted bool) (finalSynthesis string, result *VerificationResult, err error) {
	return VerifyTaskOutputWithOptions(ctx, verifier, goal, synthesis, refinedContext, VerificationOpts{
		Mode:             ModeTerminal,
		ScatterAttempted: scatterAttempted,
	})
}

// VerifyTaskOutputWithOptions is the top-level entry point for dual-mode Verified Task Execution (ADR-0079).
//
// Pipeline:
//  1. Stage 2: RunUnifiedValidation (local checks: structural + FM1/FM3/FM5)
//  2. Tier 1 (Stage 3): CloudVerifier.Verify / VerifyMilestone on pruned context (fast pass)
//  3. Tier 2 (Stage 4): CloudVerifier.ReSynthesize on full context (only on rejection with ToolSink or Terminal mode)
//
// Returns the final synthesis text (original if accepted, reSynthesis if rejected and sink-aware)
// and the VerificationResult for Execution Envelope population.
func VerifyTaskOutputWithOptions(ctx context.Context, verifier CloudVerifier, goal, synthesis, refinedContext string, opts VerificationOpts) (finalSynthesis string, result *VerificationResult, err error) {
	if opts.Mode == "" {
		opts.Mode = ModeTerminal
	}

	fmt.Fprintf(os.Stderr, "[VTE] Activating mode=%s (goal=%d chars, synthesis=%d chars, context=%d chars, feedsToolSink=%v)\n",
		opts.Mode, len(goal), len(synthesis), len(refinedContext), opts.FeedsToolSink)

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
			Mode:           opts.Mode,
			Reason:         reason,
		}, nil
	}

	// Run unified validation (FM1 meta-response + FM3 content + FM5 coverage).
	unified := RunUnifiedValidation(ctx, goal, synthesis, refinedContext)

	if unified.StructuralPreCheck == "failed" {
		fmt.Fprintf(os.Stderr, "[VTE] Stage 2 pre-check FAILED: %s\n", unified.StructuralReason)

		// In Milestone mode without a Tool Sink, skip cloud re-synthesis to save tokens/latency (ADR-0079).
		if opts.Mode == ModeMilestone && !opts.FeedsToolSink {
			fmt.Fprintf(os.Stderr, "[VTE] Milestone mode without tool sink: skipping cloud re-synthesis on pre-check failure\n")
			return synthesis, &VerificationResult{
				Accepted:       false,
				PreCheckResult: "failed",
				Source:         "local_precheck",
				Mode:           opts.Mode,
				Reason:         unified.StructuralReason,
			}, nil
		}

		// Pre-check failed — call cloud for direct re-synthesis (Tier 2).
		reSynth, synthErr := verifier.ReSynthesize(ctx, goal, refinedContext, synthesis, unified.StructuralReason)
		if synthErr != nil || reSynth == "" {
			fmt.Fprintf(os.Stderr, "[VTE] Cloud re-synthesis failed: %v\n", synthErr)
			return synthesis, &VerificationResult{
				Accepted:       false,
				PreCheckResult: "failed",
				Source:         "local_precheck",
				Mode:           opts.Mode,
				Reason:         fmt.Sprintf("structural pre-check failed (%s), cloud re-synthesis failed: %v", unified.StructuralReason, synthErr),
			}, nil
		}

		st := ExtractSourcesFromRefinedContext(refinedContext)
		if st.HasSources() {
			reSynth = st.InjectOrNormalizeReferences(reSynth)
		}

		vResult := &VerificationResult{
			Accepted:       false,
			PreCheckResult: "failed",
			Source:         "cloud_verification",
			Mode:           opts.Mode,
			ReSynthesis:    reSynth,
			Reason:         unified.StructuralReason,
		}
		return reSynth, vResult, nil
	}

	// Pre-check passed — check for coverage-based scatter opportunity (ADR-0071) in Terminal mode
	if opts.Mode == ModeTerminal && unified.CoverageResult != nil && len(unified.CoverageResult.Missing) > 0 {
		fmt.Fprintf(os.Stderr, "[VTE] Coverage advisory: %d/%d items missing\n",
			len(unified.CoverageResult.Missing), unified.CoverageResult.TotalRequired)

		if !opts.ScatterAttempted {
			var specs []ScatterSpec
			for _, item := range unified.CoverageResult.Missing {
				specs = append(specs, ScatterSpec{GoalItem: item})
			}
			fmt.Fprintf(os.Stderr, "[VTE] Scatter requested: %d missing items\n", len(specs))
			return synthesis, &VerificationResult{
				Accepted:       false,
				PreCheckResult: "passed",
				Source:         "scatter_needed",
				Mode:           opts.Mode,
				Reason:         fmt.Sprintf("coverage check found %d missing items, scatter requested", len(specs)),
				ScatterItems:   specs,
			}, nil
		}
	}
	if len(unified.ContentIssues) > 0 {
		fmt.Fprintf(os.Stderr, "[VTE] Content advisory: %d issues (dead URLs, fabricated quotes)\n",
			len(unified.ContentIssues))
	}

	// Tier 1: Run fast cloud evaluation on PRUNED context
	prunedContext := PruneContextForVerification(refinedContext, 6000)
	fmt.Fprintf(os.Stderr, "[VTE] Stage 2 pre-check PASSED, calling Tier 1 verification (%s mode, pruned context: %d -> %d chars)\n",
		opts.Mode, len(refinedContext), len(prunedContext))

	var vResult *VerificationResult
	var cloudErr error

	if opts.Mode == ModeMilestone {
		vResult, cloudErr = verifier.VerifyMilestone(ctx, goal, synthesis, prunedContext)
	} else {
		vResult, cloudErr = verifier.Verify(ctx, goal, synthesis, prunedContext)
	}

	if cloudErr != nil {
		fmt.Fprintf(os.Stderr, "[VTE] Cloud verification failed: %v — returning original synthesis\n", cloudErr)
		return synthesis, &VerificationResult{
			Accepted:       false,
			PreCheckResult: "passed",
			Source:         "cloud_verification",
			Mode:           opts.Mode,
			Reason:         fmt.Sprintf("cloud verification failed: %v", cloudErr),
		}, nil
	}

	vResult.PreCheckResult = "passed"
	vResult.Source = "cloud_verification"
	vResult.Mode = opts.Mode

	if vResult.Accepted {
		if opts.Mode == ModeMilestone {
			fmt.Fprintf(os.Stderr, "[VTE] MILESTONE ACCEPTED (step=%.2f, fact=%.2f, viability=%.2f)\n",
				vResult.StepAlignment, vResult.FactualGrounding, vResult.DownstreamViability)
		} else {
			fmt.Fprintf(os.Stderr, "[VTE] TERMINAL ACCEPTED (goal=%.2f, fact=%.2f, cohr=%.2f, comp=%.2f)\n",
				vResult.GoalAlignment, vResult.FactualGrounding, vResult.Coherence, vResult.Completeness)
		}
		return synthesis, vResult, nil
	}

	// Rejected — check for Re-Explore signal
	if opts.Mode == ModeMilestone {
		fmt.Fprintf(os.Stderr, "[VTE] MILESTONE REJECTED: %s (step=%.2f, fact=%.2f, viability=%.2f)\n",
			vResult.Reason, vResult.StepAlignment, vResult.FactualGrounding, vResult.DownstreamViability)
	} else {
		fmt.Fprintf(os.Stderr, "[VTE] TERMINAL REJECTED: %s (goal=%.2f, fact=%.2f, cohr=%.2f, comp=%.2f)\n",
			vResult.Reason, vResult.GoalAlignment, vResult.FactualGrounding, vResult.Coherence, vResult.Completeness)
	}

	if vResult.ReExplore {
		fmt.Fprintf(os.Stderr, "[VTE] Re-explore signaled: %s\n", vResult.ReExploreHint)
		return synthesis, vResult, nil
	}

	// Sink-Aware Re-Synthesis check (ADR-0079):
	// If this is a Milestone node that does NOT feed a Tool Sink, skip cloud re-synthesis.
	if opts.Mode == ModeMilestone && !opts.FeedsToolSink {
		fmt.Fprintf(os.Stderr, "[VTE] Milestone rejected without tool sink — skipping cloud re-synthesis, forwarding original synthesis\n")
		return synthesis, vResult, nil
	}

	// Tier 2: Targeted Re-Synthesis using FULL unpruned context
	fmt.Fprintf(os.Stderr, "[VTE] Firing Tier 2 re-synthesis call with full context (%d chars)\n", len(refinedContext))
	reSynth, synthErr := verifier.ReSynthesize(ctx, goal, refinedContext, synthesis, vResult.Reason)
	if synthErr != nil || reSynth == "" {
		fmt.Fprintf(os.Stderr, "[VTE] Re-synthesis failed: %v — returning original synthesis\n", synthErr)
		return synthesis, vResult, nil
	}

	st := ExtractSourcesFromRefinedContext(refinedContext)
	if st.HasSources() {
		reSynth = st.InjectOrNormalizeReferences(reSynth)
	}

	vResult.ReSynthesis = reSynth
	fmt.Fprintf(os.Stderr, "[VTE] Using cloud re-synthesis (%d chars)\n", len(reSynth))
	return reSynth, vResult, nil
}

