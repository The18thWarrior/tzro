package executor

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"tzro/internal/inference"
	"tzro/internal/strategy"
	"tzro/internal/stream"
)

// ---------------------------------------------------------------------------
// SynthesisStrategy — strategy-owned Execute for synthesis nodes (ADR-0069)
// ---------------------------------------------------------------------------

// SynthesisStrategy compiles upstream action outputs into a final cohesive
// response using the local model, with optional VTE verification for
// terminal synthesis nodes (ADR-0067).
type SynthesisStrategy struct {
	strategy.BaseStrategy
	publishState func(pub interface{ PublishStream(stream.StreamChunk) }, taskID, nodeID, status, output string)
}

// NewSynthesisStrategy creates a SynthesisStrategy.
func NewSynthesisStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *SynthesisStrategy {
	return &SynthesisStrategy{
		BaseStrategy: *base,
		publishState: publishNodeState,
	}
}

// Execute builds context, runs inference, optionally verifies via VTE, and
// returns the synthesis output. The dispatch envelope handles state
// persistence, AfterNode hooks, and events.
func (s *SynthesisStrategy) Execute(ctx context.Context, nr *strategy.NodeRuntime) (*strategy.ExecutionResult, error) {
	node := nr.Node()
	graph := nr.Graph()
	taskID := nr.TaskID()

	// Set initial running state
	_ = nr.State().SetNodeState("running", "")
	nr.Publisher().PublishEvent("node_started", taskID, node.ID, "Synthesizing final response")
	s.publishState(nr.Publisher(), taskID, node.ID, "running", "")

	systemPrompt := "You are the Local Tactician Node Executor. " +
		"Compile all prior action outputs and query results into a final cohesive response. " +
		"The accumulated context below contains data retrieved by prior nodes — use it directly. " +
		"If query results are provided, include the actual data values in your response."
	accumulatedCtx := buildAccumulatedContext(taskID, graph, "synthesis")
	goalPrompt := ""
	if graph != nil {
		goalPrompt = graph.GoalPrompt
	}
	userPrompt := buildContextAwareUserPromptWithGoal(goalPrompt, accumulatedCtx, "", nr.InterpolatedPrompt())

	req := inference.NewSimpleRequest(systemPrompt, userPrompt, "")
	// ADR-0045: Token-level streaming gated by compiler-set StreamOutput flag.
	meta := nr.Meta()
	if node.StreamOutput && meta != (inference.StreamMeta{}) {
		req.StreamMeta = &meta
	}
	req.TaskID = taskID

	// Full context window for synthesis generation.
	// Select GenerationGuard content mode based on output format —
	// code nodes use stricter compression-ratio thresholds (0.20)
	// while prose/text synthesis uses lenient thresholds (0.50).
	guardMode := inference.ContentModeProse
	if node.OutputFormat == "source_code" || node.OutputFormat == "code_validation" {
		guardMode = inference.ContentModeCode
	}
	synthCtx := context.WithValue(ctx, inference.MaxTokensKey, 65536)
	synthCtx = context.WithValue(synthCtx, inference.GenerationGuardKey,
		inference.NewRepetitionGuardWithMode(guardMode))

	var inferenceResult string
	var err error

	synthGoal := goalPrompt
	if synthGoal == "" {
		synthGoal = node.Instructions
	}

	// ADR-0084, ADR-0085, ADR-0094: Sectioned Map-Reduce synthesis for codebase docs & research
	if ShouldRunSectionedSynthesis(synthGoal, "", accumulatedCtx, 0, 0, strings.Contains(taskID, "codegen")) {
		fmt.Fprintf(os.Stderr, "[SynthesisStrategy] Sectioned Map-Reduce synthesis triggered (ADR-0084)\n")
		engine := &ProbeInference{}
		if IsDocGenGoal(synthGoal) || strings.Contains(taskID, "docgen") {
			outline, outErr := GenerateDocGenOutline(synthCtx, engine, synthGoal, accumulatedCtx, nil)
			if outErr == nil && outline != nil && len(outline.Sections) > 0 {
				docSynth, docErr := ExecuteDocGenSectionedSynthesis(synthCtx, synthGoal, accumulatedCtx, outline, nil, engine)
				if docErr == nil && len(docSynth) > 200 {
					inferenceResult = docSynth
					goto postInference
				}
				fmt.Fprintf(os.Stderr, "[SynthesisStrategy] DocGen sectioned synthesis failed (%v) — falling back to single-pass\n", docErr)
			}
		} else {
			secSections := DecomposeResearchGoalIntoSections(synthGoal)
			secSynth, secErr := ExecuteSectionedSynthesis(synthCtx, synthGoal, accumulatedCtx, secSections, engine)
			if secErr == nil && len(secSynth) > 200 {
				inferenceResult = secSynth
				goto postInference
			}
			fmt.Fprintf(os.Stderr, "[SynthesisStrategy] Research sectioned synthesis failed (%v) — falling back to single-pass\n", secErr)
		}
	}

	inferenceResult, err = inference.ExecuteWorkerStructured(synthCtx, req)
	if err != nil {
		return &strategy.ExecutionResult{
			Output:    fmt.Sprintf("synthesis node execution failed: %v", err),
			Directive: strategy.DirectiveHalt,
		}, nil
	}

postInference:
	// Strip thinking traces leaked by the local model (e.g. <thinking>, <tool_code> blocks).
	// At higher temperatures (0.6+), the 4B model occasionally emits reasoning traces
	// as output content. These are never valid in user-facing synthesis.
	inferenceResult = stripThinkingTraces(inferenceResult)

	// ADR-0067: Verified Task Execution for terminal synthesis nodes.
	// Skip VTE for source_code and code_validation nodes — the CompilationGateHook
	// handles quality validation for code. VTE text re-synthesis on code validation commentary
	// consumes cloud tokens that would block post-DAG compilation repair.
	isCodeNode := node.OutputFormat == "source_code" || node.OutputFormat == "code_validation" || node.ID == "validate_code"
	if graph.GoalPrompt != "" && !isCodeNode {
		finalSynthesis, _, vErr := VerifyTaskOutput(
			ctx,
			&DefaultCloudVerifier{},
			graph.GoalPrompt,
			inferenceResult,
			accumulatedCtx,
			false,
		)
		if vErr == nil {
			inferenceResult = finalSynthesis
		} else {
			fmt.Fprintf(os.Stderr, "[SynthesisStrategy] VTE error (non-fatal): %v\n", vErr)
		}
	}

	// Format output with execution tier prefix
	executionTier := "Local"
	if nr.ExecutionTier() != "" {
		executionTier = nr.ExecutionTier()
	}
	output := fmt.Sprintf("[%s] %s", executionTier, inferenceResult)

	// Return output — envelope handles hooks + state
	return &strategy.ExecutionResult{
		Output:    output,
		Directive: strategy.DirectiveContinue,
	}, nil
}

// Compile-time interface check.
var _ strategy.NodeStrategy = (*SynthesisStrategy)(nil)

// thinkingTracePatterns matches reasoning trace blocks leaked by the local model.
// These tags come from the model's instruction-tuning and should never appear
// in user-facing synthesis output.
var thinkingTracePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)<thinking>.*?</thinking>`),
	regexp.MustCompile(`(?s)<think>.*?</think>`),
	regexp.MustCompile(`(?s)<tool_code>.*?</tool_code>`),
	regexp.MustCompile(`(?s)<tool_output>.*?</tool_output>`),
}

// thinkingTraceTags are individual opening/closing tags to strip when they
// appear unpaired (e.g., opening tag without a closing tag, or vice versa).
var thinkingTraceTags = []string{
	"<thinking>", "</thinking>",
	"<think>", "</think>",
	"<tool_code>", "</tool_code>",
	"<tool_output>", "</tool_output>",
}

// stripThinkingTraces removes reasoning trace blocks and stray tags from
// synthesis output. The 4B local model occasionally emits <thinking>,
// <think>, or <tool_code> blocks as part of its output, especially at
// higher temperatures (0.6+). These are internal reasoning artifacts
// that should never appear in user-facing responses.
func stripThinkingTraces(s string) string {
	original := s
	for _, re := range thinkingTracePatterns {
		s = re.ReplaceAllString(s, "")
	}
	// Strip unpaired tags that regex didn't catch (e.g., unclosed <thinking>)
	for _, tag := range thinkingTraceTags {
		s = strings.ReplaceAll(s, tag, "")
	}
	s = strings.TrimSpace(s)
	if len(s) < len(original) {
		fmt.Fprintf(os.Stderr, "[SynthesisStrategy] Stripped thinking traces: %d→%d chars\n",
			len(original), len(s))
	}
	return s
}

