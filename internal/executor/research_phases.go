package executor

// research_phases.go — Research Node phase template for the Phase Runner.
//
// New node type (type: "research") for web exploration tasks. Replaces
// the pattern of Probe nodes with web tools in allowedTools.
// 5-phase pipeline: Search → Rank → Deep-Read → Cross-Ref → Synthesize.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tzro/internal/compiler"
)

// RunResearchPhases executes a Research Node using the Phase Runner with the
// Research-specific 5-phase template: Search → Rank → Deep-Read → Cross-Ref → Synthesize.
func RunResearchPhases(
	ctx context.Context,
	taskID, probeID string,
	config compiler.ProbeConfig,
	engine ProbeInferenceEngine,
	synthesisEngine ProbeInferenceEngine,
	downstreamBindingKeys []string,
) (string, error) {
	runner := buildResearchPhaseRunner(config)

	results, err := runner.Run(ctx, taskID, probeID, engine, synthesisEngine)
	if err != nil {
		return "", fmt.Errorf("research phases failed: %w", err)
	}

	if len(results) == 0 {
		return "", fmt.Errorf("research phases produced no results")
	}

	manifest := runner.BuildManifest(results)
	finalSynthesis := results[len(results)-1].Summary

	fmt.Fprintf(os.Stderr, "[ResearchPhases] Completed %d phases, %d total steps, %d backtracks\n",
		len(manifest.Phases), manifest.TotalStepsUsed, manifest.TotalBacktracks)

	return finalSynthesis, nil
}

// buildResearchPhaseRunner constructs a PhaseRunner with the Research-specific template.
func buildResearchPhaseRunner(config compiler.ProbeConfig) *PhaseRunner {
	var searchHasURLs bool
	var urlsBrowsed int

	runner := &PhaseRunner{
		Phases: map[string]*Phase{
			"search": {
				Name:         "search",
				AllowedTools: []string{"web_search"},
				SystemPrompt: buildPhaseResearchPrompt("search", config.Goal, config.TaskContext),
				StepBudget:   3,
				Pass1Target:  TargetRouter,
				Recovery: PhaseRecovery{
					MaxRetries:   1,
					OnExhaustion: ExhaustionFail,
					OnError:      ErrorFail,
				},
				Transition: func(step int, result PhaseResult, err error) string {
					for _, tool := range result.ToolsCalled {
						if tool == "web_search" {
							searchHasURLs = true
							return "rank"
						}
					}
					return ""
				},
			},
			"rank": {
				Name:         "rank",
				AllowedTools: []string{}, // Synthesis only — no tools
				SystemPrompt: buildPhaseResearchPrompt("rank", config.Goal, config.TaskContext),
				StepBudget:   1,
				Pass1Target:  TargetWorker,
				Recovery: PhaseRecovery{
					OnExhaustion: ExhaustionSkip,
					OnError:      ErrorFail,
				},
				Transition: func(step int, result PhaseResult, err error) string {
					return "deep_read" // always transitions
				},
			},
			"deep_read": {
				Name:         "deep_read",
				AllowedTools: []string{"web_browse"},
				SystemPrompt: buildPhaseResearchPrompt("deep_read", config.Goal, config.TaskContext),
				StepBudget:   8,
				Pass1Target:  TargetWorker,
				Recovery: PhaseRecovery{
					MaxRetries:   1,
					OnExhaustion: ExhaustionSkip,
					OnError:      ErrorFail,
					BacktrackTo:  "search",
				},
				Transition: func(step int, result PhaseResult, err error) string {
					for _, tool := range result.ToolsCalled {
						if tool == "web_browse" {
							urlsBrowsed++
						}
					}
					if urlsBrowsed >= 3 {
						return "cross_ref"
					}
					return ""
				},
			},
			"cross_ref": {
				Name:         "cross_ref",
				AllowedTools: []string{"web_search", "web_browse"},
				SystemPrompt: buildPhaseResearchPrompt("cross_ref", config.Goal, config.TaskContext),
				StepBudget:   4,
				Pass1Target:  TargetWorker,
				Recovery: PhaseRecovery{
					OnExhaustion: ExhaustionSkip,
					OnError:      ErrorFail,
					BacktrackTo:  "search",
				},
				Transition: func(step int, result PhaseResult, err error) string {
					return "" // terminal — falls through to synthesize via budget
				},
			},
			"synthesize": {
				Name:         "synthesize",
				AllowedTools: []string{},
				SystemPrompt: buildPhaseResearchPrompt("synthesize", config.Goal, config.TaskContext),
				StepBudget:   1,
				Pass1Target:  TargetWorker,
				Recovery: PhaseRecovery{
					OnExhaustion: ExhaustionFail,
					OnError:      ErrorFail,
				},
				Transition: func(step int, result PhaseResult, err error) string { return "" },
			},
		},
		InitialPhase: "search",
		MaxCycles:    3,
	}

	_ = searchHasURLs

	return runner
}

// buildPhaseResearchPrompt constructs a phase-specific system prompt for Research nodes.
func buildPhaseResearchPrompt(phase, goal, taskContext string) string {
	var b strings.Builder

	switch phase {
	case "search":
		b.WriteString("You are researching a topic on the web. ")
		b.WriteString("PHASE: SEARCH — use web_search to find relevant sources and URLs. ")
		b.WriteString("Use web_search ONLY. Extract search queries from the goal. ")
	case "rank":
		b.WriteString("You are ranking search results by relevance. ")
		b.WriteString("PHASE: RANK — evaluate the discovered URLs and rank them by relevance to the goal. ")
		b.WriteString("You have NO tools. Produce a ranked list from the search results. ")
	case "deep_read":
		b.WriteString("You are reading web pages to extract detailed information. ")
		b.WriteString("PHASE: DEEP-READ — browse the top-ranked URLs and extract key content. ")
		b.WriteString("Use web_browse ONLY. Focus on extracting facts, not summarizing. ")
	case "cross_ref":
		b.WriteString("You are cross-referencing findings from multiple sources. ")
		b.WriteString("PHASE: CROSS-REF — verify claims by checking additional sources. ")
		b.WriteString("Use web_search and web_browse to validate and enrich findings. ")
	case "synthesize":
		b.WriteString("You are producing a final research synthesis. ")
		b.WriteString("PHASE: SYNTHESIZE — produce a comprehensive, well-sourced answer. ")
		b.WriteString("You have NO tools. Use the accumulated phase results as your evidence. ")
	}

	b.WriteString(fmt.Sprintf("\n\nGoal: %s", goal))
	if taskContext != "" {
		b.WriteString(fmt.Sprintf("\n\nTask Context: %s", taskContext))
	}

	return b.String()
}
