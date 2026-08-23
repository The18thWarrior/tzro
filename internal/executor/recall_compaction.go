package executor

// recall_compaction.go — Deterministic baseline context for Recall Node
// Refinement Pass (ADR-0064 Mechanism C: Recall Loop Inversion).
//
// buildCompactedRecallContext reads ThoughtSteps from upstream probes and
// compacts them using content-aware CompactToolOutputs. This provides a
// guaranteed-quality floor that the agentic loop can optionally enhance.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tzro/internal/compactor"
	cfgpkg "tzro/internal/config"
	"tzro/internal/memory"
)

// buildCompactedRecallContext reads ThoughtSteps from upstream probes and
// compacts their tool outputs using content-aware strategies (ADR-0064).
//
// Returns a deterministic baseline context string within the configured budget.
// Code outputs → skeleton, text → LLM fact-extraction, tabular → sample rows.
// Analyze Node tools (sql_cached_data, introspect_cache) are exempt.
func buildCompactedRecallContext(
	ctx context.Context,
	taskID string,
	upstreamNodeIDs []string,
	engine compactor.CompactEngine,
	goal string,
) (string, error) {
	if goal != "" {
		ctx = context.WithValue(ctx, compactor.CompactorGoalKey, goal)
	}
	budget := cfgpkg.GetRecallCompactionBudgetChars()

	var allSteps []compactor.ToolOutputStep
	for _, nodeID := range upstreamNodeIDs {
		probeID := taskID + "_" + nodeID
		steps, err := memory.DB.GetThoughtSteps(probeID)
		if err != nil {
			continue
		}

		// Two-pass approach: first collect all successful outputs as upstream
		// context, then classify errors against that context.
		var successOutputs []string
		var rawSteps []memory.ThoughtStep
		for _, s := range steps {
			if s.ToolName == "" || s.ToolOutput == "" {
				continue
			}
			rawSteps = append(rawSteps, s)
			if !isToolError(s.ToolOutput) {
				successOutputs = append(successOutputs, s.ToolOutput)
			}
		}

		upstreamContext := strings.Join(successOutputs, "\n")
		prunedCount := 0

		// Window rawSteps to at most maxStepsPerProbe before adding to allSteps.
		// ADR-run32: Long probes (60+ steps) overflow the recall context budget;
		// stratified windowing preserves head (orientation), tail (final state),
		// and evenly-spaced middle steps while capping total at 25.
		rawSteps = windowThoughtSteps(rawSteps, 25)

		for _, s := range rawSteps {
			// Prune uninformative TOOL_ERRORs before they enter synthesis context
			if isToolError(s.ToolOutput) && IsUninformativeToolError(s.ToolName, s.ToolArgs, s.ToolOutput, upstreamContext) {
				prunedCount++
				continue
			}
			allSteps = append(allSteps, compactor.ToolOutputStep{
				StepIndex:  s.StepIndex,
				ToolName:   s.ToolName,
				ToolArgs:   s.ToolArgs,
				ToolOutput: s.ToolOutput,
			})
		}

		if prunedCount > 0 {
			fmt.Fprintf(os.Stderr, "[EdgeEntry] Pruned %d uninformative TOOL_ERRORs from probe %s (hallucinated parameters)\n", prunedCount, probeID)
		}
	}

	if len(allSteps) == 0 {
		// Fallback: check node_states for upstream direct synthesis output (ADR-0086/0088)
		for _, nodeID := range upstreamNodeIDs {
			if state, ok := memory.DB.GetNodeState(taskID, nodeID); ok {
				out := state.RawOutput
				if out == "" {
					out = state.Output
				}
				if out != "" {
					return out, nil
				}
			}
		}
		return "", nil
	}

	result, err := compactor.CompactToolOutputs(ctx, allSteps, budget, engine)
	if err != nil {
		return "", fmt.Errorf("recall compaction failed: %w", err)
	}

	fmt.Fprintf(
		os.Stderr,
		"[Recall] Compacted baseline context: %d\u2192%d chars (%d LLM calls)\n",
		result.InputChars, result.OutputChars, result.LLMCalls,
	)

	return result.Output, nil
}

// windowThoughtSteps applies stratified sampling to cap a step slice at max.
// When len(steps) <= max, all steps are returned unchanged.
// When len(steps) > max, the result contains:
//   - Head: the first 5 steps (orientation context)
//   - Tail: the last 2 steps (final state)
//   - Middle: evenly-spaced steps from the remainder, up to max-7 slots
//
// The returned slice is always in ascending StepIndex order.
// ADR-run32: Prevents ThoughtStep token overflow in buildCompactedRecallContext.
func windowThoughtSteps(steps []memory.ThoughtStep, max int) []memory.ThoughtStep {
	if len(steps) <= max {
		return steps
	}

	const headCount = 5
	const tailCount = 2
	middleCount := max - headCount - tailCount
	if middleCount < 0 {
		middleCount = 0
	}

	head := steps[:headCount]
	tail := steps[len(steps)-tailCount:]
	middle := steps[headCount : len(steps)-tailCount]

	// Evenly sample middleCount steps from middle using stride.
	var sampled []memory.ThoughtStep
	if middleCount > 0 && len(middle) > 0 {
		if len(middle) <= middleCount {
			sampled = middle
		} else {
			stride := float64(len(middle)) / float64(middleCount)
			for i := 0; i < middleCount; i++ {
				idx := int(float64(i) * stride)
				if idx >= len(middle) {
					idx = len(middle) - 1
				}
				sampled = append(sampled, middle[idx])
			}
		}
	}

	result := make([]memory.ThoughtStep, 0, max)
	result = append(result, head...)
	result = append(result, sampled...)
	result = append(result, tail...)
	return result
}
