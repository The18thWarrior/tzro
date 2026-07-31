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
) (string, error) {
	budget := cfgpkg.GetRecallCompactionBudgetChars()

	var allSteps []compactor.ToolOutputStep
	for _, nodeID := range upstreamNodeIDs {
		probeID := taskID + "_" + nodeID
		steps, err := memory.DB.GetThoughtSteps(probeID)
		if err != nil {
			continue
		}
		for _, s := range steps {
			if s.ToolName == "" || s.ToolOutput == "" {
				continue
			}
			allSteps = append(allSteps, compactor.ToolOutputStep{
				StepIndex:  s.StepIndex,
				ToolName:   s.ToolName,
				ToolArgs:   s.ToolArgs,
				ToolOutput: s.ToolOutput,
			})
		}
	}

	if len(allSteps) == 0 {
		return "", nil
	}

	result, err := compactor.CompactToolOutputs(ctx, allSteps, budget, engine)
	if err != nil {
		return "", fmt.Errorf("recall compaction failed: %w", err)
	}

	fmt.Fprintf(
		os.Stderr,
		"[Recall] Compacted baseline context: %d→%d chars (%d LLM calls)\n",
		result.InputChars, result.OutputChars, result.LLMCalls,
	)

	return result.Output, nil
}
