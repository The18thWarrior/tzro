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

	fmt.Fprintf(os.Stderr, "[ProbePhases] Completed %d phases, %d total steps, %d backtracks\n",
		len(manifest.Phases), manifest.TotalStepsUsed, manifest.TotalBacktracks)

	return finalSynthesis, nil
}

// buildProbePhaseRunner constructs a PhaseRunner with the Probe-specific
// phase template from the design spec.
func buildProbePhaseRunner(config compiler.ProbeConfig) *PhaseRunner {
	// Determine tool sets for each phase based on config.AllowedTools
	orientTools := filterTools(config.AllowedTools, []string{"list_dir", "search_files"})
	discoverTools := filterTools(config.AllowedTools, []string{"list_dir", "search_files", "read_file"})
	deepReadTools := filterTools(config.AllowedTools, []string{"read_file"})

	// Track file discovery state for transition decisions
	var filesWithSymbols int
	var deepReadDepth int

	// ADR-0058 port: Initialize Exploration Queue from PreloadPaths for
	// deterministic loop-breaking. When a duplicate read_file is detected,
	// redirect to the next unvisited file via ToolFixup.
	var explorationQueue *ExplorationQueue
	if len(config.PreloadPaths) > 0 {
		queueFiles := collectPreloadFiles(config.PreloadPaths)
		if len(queueFiles) > 0 {
			explorationQueue = NewExplorationQueue(queueFiles)
			fmt.Fprintf(os.Stderr, "[ProbePhases] Exploration Queue initialized with %d files\n", len(queueFiles))
		}
	}

	runner := &PhaseRunner{
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
			if toolName == "read_file" && err == nil && explorationQueue != nil {
				if path, ok := args["path"].(string); ok && path != "" {
					explorationQueue.MarkVisited(path)
				}
			}
		},
		Phases: map[string]*Phase{
			"orient": {
				Name:         "orient",
				AllowedTools: orientTools,
				SystemPrompt: buildPhaseProbePrompt("orient", config.Goal, config.TaskContext),
				StepBudget:   3,
				Pass1Target:  TargetRouter,
				Recovery: PhaseRecovery{
					MaxRetries:   0,
					OnExhaustion: ExhaustionSkip,
					OnError:      ErrorFail,
				},
				Transition: func(step int, result PhaseResult, err error) string {
					// Transition: first list_dir at depth ≥2 OR budget exhausted
					for _, tool := range result.ToolsCalled {
						if tool == "list_dir" {
							return "discover"
						}
					}
					return ""
				},
			},
			"discover": {
				Name:         "discover",
				AllowedTools: discoverTools,
				SystemPrompt: buildPhaseProbePrompt("discover", config.Goal, config.TaskContext),
				StepBudget:   8,
				Pass1Target:  TargetRouter,
				Recovery: PhaseRecovery{
					MaxRetries:   1,
					OnExhaustion: ExhaustionSkip,
					OnError:      ErrorFail,
					BacktrackTo:  "orient",
				},
				Transition: func(step int, result PhaseResult, err error) string {
					// Transition: ≥3 files with symbols OR budget exhausted
					for _, tool := range result.ToolsCalled {
						if tool == "read_file" {
							filesWithSymbols++
						}
					}
					if filesWithSymbols >= 3 {
						return "deep_read"
					}
					return ""
				},
			},
			"deep_read": {
				Name:         "deep_read",
				AllowedTools: deepReadTools,
				SystemPrompt: buildPhaseProbePrompt("deep_read", config.Goal, config.TaskContext),
				StepBudget:   10,
				Pass1Target:  TargetWorker,
				Recovery: PhaseRecovery{
					MaxRetries:   1,
					OnExhaustion: ExhaustionSkip,
					OnError:      ErrorFail,
					BacktrackTo:  "discover",
				},
				Transition: func(step int, result PhaseResult, err error) string {
					// Transition: diminishing returns OR budget exhausted
					deepReadDepth++
					if deepReadDepth >= 5 {
						return "synthesize"
					}
					return ""
				},
			},
			"synthesize": {
				Name:         "synthesize",
				AllowedTools: []string{}, // No tools — pure synthesis
				SystemPrompt: buildPhaseProbePrompt("synthesize", config.Goal, config.TaskContext),
				StepBudget:   1,
				Pass1Target:  TargetWorker,
				Recovery: PhaseRecovery{
					MaxRetries:   0,
					OnExhaustion: ExhaustionFail,
					OnError:      ErrorFail,
				},
				Transition: func(step int, result PhaseResult, err error) string {
					return "" // terminal
				},
			},
		},
		InitialPhase: "orient",
		MaxCycles:    3,
		Goal:         config.Goal,
	}

	return runner
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
	// If no overlap, return the allowed set as default
	if len(filtered) == 0 {
		return allowed
	}
	return filtered
}
