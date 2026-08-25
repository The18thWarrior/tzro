package executor

// probe_phases.go — Probe Node phase template for the Phase Runner.
//
// Replaces the flat Thought Chain loop for Probe Nodes with a structured
// 4-phase pipeline: Orient → Discover → Deep-Read → Synthesize.
//
// Each phase has scoped tools, a step budget, and deterministic transition
// triggers. See docs/superpowers/specs/2026-08-05-phase-runner-design.md.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tzro/internal/compiler"
)

// RunProbePhases executes a Probe Node using the Phase Runner with the
// Probe-specific 4-phase template: Orient → Discover → Deep-Read → Synthesize.
//
// This replaces the flat RunProbe() Thought Chain loop for Probe nodes.
func RunProbePhases(
	ctx context.Context,
	taskID, probeID string,
	config compiler.ProbeConfig,
	engine ProbeInferenceEngine,
	synthesisEngine ProbeInferenceEngine,
	downstreamBindingKeys []string,
) (string, error) {
	runner := buildProbePhaseRunner(config)

	results, err := runner.Run(ctx, taskID, probeID, engine, synthesisEngine)
	if err != nil {
		return "", fmt.Errorf("probe phases failed: %w", err)
	}

	if len(results) == 0 {
		return "", fmt.Errorf("probe phases produced no results")
	}

	// Build the Phase Manifest
	manifest := runner.BuildManifest(results)

	// The final synthesis is the last phase's summary
	finalSynthesis := results[len(results)-1].Summary

	if runner.SourceTracker != nil {
		finalSynthesis = runner.SourceTracker.InjectOrNormalizeReferences(finalSynthesis)
	}

	fmt.Fprintf(os.Stderr, "[ProbePhases] Completed %d phases, %d total steps, %d backtracks\n",
		len(manifest.Phases), manifest.TotalStepsUsed, manifest.TotalBacktracks)

	return finalSynthesis, nil
}

// buildProbePhaseRunner constructs a PhaseRunner with the Probe-specific
// phase template from the design spec.
func buildProbePhaseRunner(config compiler.ProbeConfig) *PhaseRunner {
	// Route to web-specific template when AllowedTools is web-only.
	// Detection: contains web_search or web_browse, and does NOT contain
	// any filesystem tools (read_file, list_dir, search_files).
	if isWebOnlyProbe(config.AllowedTools) {
		return buildWebProbePhaseRunner(config)
	}

	// Determine tool sets for each phase based on config.AllowedTools
	orientTools := filterTools(config.AllowedTools, []string{"list_dir", "search_files"})
	discoverTools := filterTools(config.AllowedTools, []string{"list_dir", "search_files", "read_file"})
	deepReadTools := filterTools(config.AllowedTools, []string{"read_file"})

	// ADR-0058 / ADR-0082 / ADR-0084: Initialize Exploration Queue from PreloadPaths.
	// For multi-file documentation goals (ADR-0084), route directly to the Goal-Specific
	// Inventory Extractor (Map-Reduce) to process all candidate files with zero omissions.
	//
	// EXCEPTION: When DirectSynthesis is true (Pre-Index has packed all context), skip
	// the inventory extractor and fall through to the normal 4-phase exploration path
	// with rich relevance scoring. The 4B model cannot synthesize from a massive system
	// prompt injection; it needs context delivered through tool call responses in the
	// chat history.
	var explorationQueue *ExplorationQueue
	if len(config.PreloadPaths) > 0 {
		queueFiles := collectPreloadFiles(config.PreloadPaths)
		if !config.DirectSynthesis && len(queueFiles) > 5 && IsInventoryGoal(context.Background(), config.Goal) {
			fmt.Fprintf(os.Stderr, "[ProbePhases] Multi-file documentation goal detected (%d files) — routing to Goal-Specific Inventory Extractor\n", len(queueFiles))
			return buildInventoryProbePhaseRunner(config, queueFiles)
		}
		if len(queueFiles) > 0 {
			explorationQueue = NewExplorationQueue(queueFiles)
			// ADR-0082 gap closure: Rich relevance scoring replaces single-signal ScoreAndPrune.
			// Uses AST symbols (code) + semantic content (text) + path embedding + import affinity.
			if config.Goal != "" {
				goalType := classifyProbeGoal(config.Goal)
				scored := RichScoreAndSelect(context.Background(), config.Goal, queueFiles, goalType)
				if len(scored) > 0 {
					selectedPaths := make([]string, len(scored))
					for i, s := range scored {
						selectedPaths[i] = s.Path
					}
					explorationQueue.ReplaceFiles(selectedPaths)
				}
			}
			fmt.Fprintf(os.Stderr, "[ProbePhases] Exploration Queue: %d files after rich scoring\n", len(explorationQueue.files))
		}
	}

	var initialFiles []QueueItem
	var deepReadFiles []QueueItem
	if explorationQueue != nil {
		allFiles := explorationQueue.files
		if len(allFiles) <= 3 {
			for _, f := range allFiles {
				initialFiles = append(initialFiles, QueueItem{
					Tool: "read_file",
					Args: map[string]interface{}{"path": f},
				})
			}
		} else {
			// Top-3 by relevance score → Discover phase
			for _, f := range allFiles[:3] {
				initialFiles = append(initialFiles, QueueItem{
					Tool: "read_file",
					Args: map[string]interface{}{"path": f},
				})
			}
			// Remaining scored files → Deep-Read phase (already capped by RichScoreAndSelect)
			for _, f := range allFiles[3:] {
				deepReadFiles = append(deepReadFiles, QueueItem{
					Tool: "read_file",
					Args: map[string]interface{}{"path": f},
				})
			}
		}
	}
	if len(initialFiles) == 0 && len(deepReadFiles) == 0 {
		initialFiles = append(initialFiles, QueueItem{
			Tool: "list_dir",
			Args: map[string]interface{}{"path": "."},
		})
	}

	discoverBudget := len(initialFiles)
	if discoverBudget < 1 {
		discoverBudget = 1
	}
	deepReadBudget := len(deepReadFiles)
	if deepReadBudget < 1 {
		deepReadBudget = 1
	}

	sourceTracker := NewSourceTracker()

	runner := &PhaseRunner{
		SourceTracker: sourceTracker,
		ToolFixup: func(phaseName, toolName string, args map[string]interface{}, reasoning string) (string, map[string]interface{}) {
			if toolName == "read_file" && explorationQueue != nil {
				path, _ := args["path"].(string)
				if path == "" || explorationQueue.visited[path] {
					if next, ok := explorationQueue.NextUnvisited(); ok {
						fmt.Fprintf(os.Stderr, "[ProbePhases] ToolFixup: redirecting read_file from %q to %q\n", path, next)
						args["path"] = next
					}
				}
			}
			return toolName, args
		},
		ToolPostProcess: func(phaseName, toolName string, args map[string]interface{}, output string, err error) {
			if toolName == "read_file" && err == nil {
				if path, ok := args["path"].(string); ok && path != "" {
					if explorationQueue != nil {
						explorationQueue.MarkVisited(path)
					}
					sourceTracker.AddFileSource(path, nil, strings.Count(output, "\n")+1, "")
				}
			}
		},
		Phases: map[string]*Phase{
			"orient": {
				Name:         "orient",
				AllowedTools: orientTools,
				SystemPrompt: buildPhaseProbePrompt("orient", config.Goal, config.TaskContext),
				StepBudget:   1,
				Driver:       NewDeterministicQueueDriver([]QueueItem{{Tool: "list_dir", Args: map[string]interface{}{"path": "."}}}),
				Transition: func(step int, result PhaseResult, err error) string {
					return "discover"
				},
			},
			"discover": {
				Name:         "discover",
				AllowedTools: discoverTools,
				SystemPrompt: buildPhaseProbePrompt("discover", config.Goal, config.TaskContext),
				StepBudget:   discoverBudget,
				MinToolCalls: 1,
				Driver:       NewDeterministicQueueDriver(initialFiles),
				Transition: func(step int, result PhaseResult, err error) string {
					return "deep_read"
				},
			},
			"deep_read": {
				Name:         "deep_read",
				AllowedTools: deepReadTools,
				SystemPrompt: buildPhaseProbePrompt("deep_read", config.Goal, config.TaskContext),
				StepBudget:   deepReadBudget,
				Pass1Target:  TargetWorker,
				Driver:       NewDeterministicQueueDriver(deepReadFiles),
				Transition: func(step int, result PhaseResult, err error) string {
					return "synthesize"
				},
			},
			"synthesize": {
				Name:         "synthesize",
				AllowedTools: []string{},
				SystemPrompt: buildPhaseProbePrompt("synthesize", config.Goal, config.TaskContext),
				StepBudget:   1,
				Pass1Target:  TargetWorker,
				Driver:       NewDeterministicQueueDriver(nil),
			},
		},
		InitialPhase: "orient",
		MaxCycles:    1,
		Goal:         config.Goal,
	}

	return runner
}

// buildWebProbePhaseRunner delegates directly to the Deterministic Web Research pipeline.
func buildWebProbePhaseRunner(config compiler.ProbeConfig) *PhaseRunner {
	fmt.Fprintf(os.Stderr, "[ProbePhases] Using web-specific phase template (search → browse)\n")
	queries := extractSearchQueryVariantsFromGoal(config.Goal)
	return buildResearchPhaseRunner(config, queries)
}


// buildPhaseProbePrompt constructs a phase-specific system prompt for Probe nodes.
func buildPhaseProbePrompt(phase, goal, taskContext string) string {
	var b strings.Builder

	switch phase {
	case "orient":
		b.WriteString("You are exploring a codebase to understand its structure. ")
		b.WriteString("PHASE: ORIENT — scan the top-level directory structure to identify key areas. ")
		b.WriteString("Use list_dir and search_files ONLY. Do NOT read files yet. ")
	case "discover":
		b.WriteString("You are exploring a codebase to discover key files and modules. ")
		b.WriteString("PHASE: DISCOVER — identify important files by scanning directories and reading headers. ")
		b.WriteString("Use list_dir, search_files, and read_file. ")
	case "deep_read":
		b.WriteString("You are performing deep reading of critical source files. ")
		b.WriteString("PHASE: DEEP-READ — read and analyze the most important files identified during discovery. ")
		b.WriteString("Use read_file ONLY. Focus on understanding implementations, not scanning. ")
	case "synthesize":
		b.WriteString("You are producing a final synthesis of your exploration findings. ")
		b.WriteString("PHASE: SYNTHESIZE — produce a comprehensive, accurate response. ")
		b.WriteString("You have NO tools. Use only the accumulated phase results. ")
	case "search":
		b.WriteString("You are conducting web research to find authoritative sources. ")
		b.WriteString("PHASE: SEARCH — issue diverse search queries to find primary sources, official documentation, and authoritative references. ")
		b.WriteString("Use web_search ONLY. Issue at least 2 different search queries covering different aspects of the topic. ")
		b.WriteString("Do NOT synthesize yet — focus on finding sources. ")
	case "browse":
		b.WriteString("You are extracting structured information from web sources. ")
		b.WriteString("PHASE: BROWSE — read the most relevant URLs from your search results and extract key facts, data, and quotes. ")
		b.WriteString("Use web_browse to read pages and web_search for follow-up queries. ")
		b.WriteString("Focus on extracting verifiable facts with source attribution. Do NOT fabricate information. ")
	}

	b.WriteString(fmt.Sprintf("\n\nGoal: %s", goal))
	if taskContext != "" {
		b.WriteString(fmt.Sprintf("\n\nTask Context: %s", taskContext))
	}

	return b.String()
}

// filterTools returns only the tools from available that are in the allowed set.
func filterTools(available []string, allowed []string) []string {
	allowedSet := make(map[string]bool)
	for _, t := range allowed {
		allowedSet[t] = true
	}
	var filtered []string
	for _, t := range available {
		if allowedSet[t] {
			filtered = append(filtered, t)
		}
	}
	// If no overlap, return the probe's configured tools (available) rather
	// than the phase's hardcoded preferences (allowed). The probe's AllowedTools
	// is the authoritative constraint — phase preferences are advisory.
	if len(filtered) == 0 {
		return available
	}
	return filtered
}

// buildInventoryProbePhaseRunner constructs a PhaseRunner for the Goal-Specific Inventory Extractor (ADR-0084).
func buildInventoryProbePhaseRunner(config compiler.ProbeConfig, queueFiles []string) *PhaseRunner {
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

