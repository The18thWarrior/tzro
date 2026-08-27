package executor

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tzro/internal/compiler"
)

// RunInventoryPhases executes an inventory extraction pipeline using the Phase Runner:
// Derive Schema → Map Inventory across candidate files → Synthesize.
func RunInventoryPhases(
	ctx context.Context,
	taskID, nodeID string,
	config compiler.ProbeConfig,
	engine ProbeInferenceEngine,
	synthesisEngine ProbeInferenceEngine,
	downstreamBindingKeys []string,
) (string, error) {
	queueFiles := collectPreloadFiles(config.PreloadPaths)
	if len(queueFiles) == 0 {
		return "", fmt.Errorf("no candidate files found for inventory extraction in %v", config.PreloadPaths)
	}

	runner := buildInventoryPhaseRunner(config, queueFiles)

	results, err := runner.Run(ctx, taskID, nodeID, engine, synthesisEngine)
	if err != nil {
		return "", fmt.Errorf("inventory phases failed: %w", err)
	}

	if len(results) == 0 {
		return "", fmt.Errorf("inventory phases produced no results")
	}

	manifest := runner.BuildManifest(results)
	finalSynthesis := results[len(results)-1].Summary

	if runner.SourceTracker != nil {
		finalSynthesis = runner.SourceTracker.InjectOrNormalizeReferences(finalSynthesis)
	}

	fmt.Fprintf(os.Stderr, "[InventoryPhases] Completed %d phases, %d total steps, %d files\n",
		len(manifest.Phases), manifest.TotalStepsUsed, len(queueFiles))

	return finalSynthesis, nil
}

// buildInventoryPhaseRunner constructs a PhaseRunner for the Goal-Specific Inventory Extractor (ADR-0084).
func buildInventoryPhaseRunner(config compiler.ProbeConfig, queueFiles []string) *PhaseRunner {
	sourceTracker := NewSourceTracker()
	mapDriver := &InventoryMapDriver{
		Files: queueFiles,
	}

	runner := &PhaseRunner{
		SourceTracker: sourceTracker,
		Phases: map[string]*Phase{
			"derive_schema": {
				Name:         "derive_schema",
				AllowedTools: []string{},
				SystemPrompt: "Deriving extraction schema from task goal...",
				StepBudget:   1,
				Driver: &DynamicSchemaDriver{
					Goal: config.Goal,
					OnSchemaDerived: func(s *InventorySchema) {
						mapDriver.Schema = s
					},
				},
				Transition: func(step int, result PhaseResult, err error) string {
					return "map_inventory"
				},
			},
			"map_inventory": {
				Name:         "map_inventory",
				AllowedTools: []string{"read_file"},
				SystemPrompt: "Extracting structured file inventory rows...",
				StepBudget:   len(queueFiles),
				Driver:       mapDriver,
				Transition: func(step int, result PhaseResult, err error) string {
					return "synthesize"
				},
			},
			"synthesize": {
				Name:         "synthesize",
				AllowedTools: []string{},
				SystemPrompt: buildInventorySynthesisPrompt(config.Goal),
				StepBudget:   1,
				Pass1Target:  TargetWorker,
				Driver:       NewDeterministicQueueDriver(nil),
			},
		},
		InitialPhase: "derive_schema",
		MaxCycles:    1,
		Goal:         config.Goal,
	}

	return runner
}

func buildInventorySynthesisPrompt(goal string) string {
	var b strings.Builder
	b.WriteString("You are synthesizing a complete, authoritative, and structured technical document based on the verified file inventory.\n")
	b.WriteString("Every relevant file and component discovered in the inventory MUST be represented in your synthesis.\n")
	b.WriteString("Do NOT omit, truncate, or hallucinate components.\n\n")
	b.WriteString(fmt.Sprintf("Goal: %s", goal))
	return b.String()
}
