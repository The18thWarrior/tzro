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
// Deterministic Web Research Pipeline (ADR-0078): Query Generation → Search → Deep-Read → Synthesize.
func RunResearchPhases(
	ctx context.Context,
	taskID, probeID string,
	config compiler.ProbeConfig,
	engine ProbeInferenceEngine,
	synthesisEngine ProbeInferenceEngine,
	downstreamBindingKeys []string,
) (string, error) {
	// Stage 1: Generate 2-3 diverse search queries using 1-shot Worker inference
	queries, err := GenerateSearchQueries(ctx, engine, config.Goal)
	if err != nil || len(queries) == 0 {
		queries = []string{extractSearchQueryFromGoal(config.Goal)}
	}

	runner := buildResearchPhaseRunner(config, queries)

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

// buildResearchPhaseRunner constructs a PhaseRunner with the Deterministic Web Research template.
func buildResearchPhaseRunner(config compiler.ProbeConfig, queries []string) *PhaseRunner {
	var searchItems []QueueItem
	for _, q := range queries {
		if strings.TrimSpace(q) != "" {
			searchItems = append(searchItems, QueueItem{
				Tool: "web_search",
				Args: map[string]interface{}{"query": strings.TrimSpace(q)},
			})
		}
	}
	if len(searchItems) == 0 {
		searchItems = append(searchItems, QueueItem{
			Tool: "web_search",
			Args: map[string]interface{}{"query": extractSearchQueryFromGoal(config.Goal)},
		})
	}

	var discoveredURLs []string
	visitedURLs := make(map[string]bool)

	runner := &PhaseRunner{
		ToolFixup: func(phaseName, toolName string, args map[string]interface{}, reasoning string) (string, map[string]interface{}) {
			switch toolName {
			case "web_search":
				query, _ := args["query"].(string)
				if strings.TrimSpace(query) == "" {
					args["query"] = extractSearchQueryFromGoal(config.Goal)
				}
			case "web_browse":
				url, _ := args["url"].(string)
				if strings.TrimSpace(url) == "" || visitedURLs[url] {
					for _, candidate := range discoveredURLs {
						if !visitedURLs[candidate] {
							args["url"] = candidate
							break
						}
					}
				}
			}
			return toolName, args
		},
		ToolPostProcess: func(phaseName, toolName string, args map[string]interface{}, output string, err error) {
			switch toolName {
			case "web_search":
				if err == nil {
					urls := extractURLsFromWebSearch(output)
					for _, u := range urls {
						if !visitedURLs[u] {
							discoveredURLs = append(discoveredURLs, u)
						}
					}
					if len(urls) > 0 {
						fmt.Fprintf(os.Stderr, "[ResearchPhases] ToolPostProcess: extracted %d URLs from web_search (total queue: %d)\n",
							len(urls), len(discoveredURLs))
					}
				}
			case "web_browse":
				if err == nil {
					if url, ok := args["url"].(string); ok && url != "" {
						visitedURLs[url] = true
					}
				}
			}
		},
		Phases: map[string]*Phase{
			"search": {
				Name:         "search",
				AllowedTools: []string{"web_search"},
				SystemPrompt: buildPhaseResearchPrompt("search", config.Goal, config.TaskContext),
				StepBudget:   len(searchItems),
				Driver:       NewDeterministicQueueDriver(searchItems),
				Transition: func(step int, result PhaseResult, err error) string {
					return "deep_read"
				},
			},
			"deep_read": {
				Name:         "deep_read",
				AllowedTools: []string{"web_browse"},
				SystemPrompt: buildPhaseResearchPrompt("deep_read", config.Goal, config.TaskContext),
				StepBudget:   5,
				Driver: NewDynamicQueueDriver(func() []QueueItem {
					var browseItems []QueueItem
					for _, u := range discoveredURLs {
						if !visitedURLs[u] {
							browseItems = append(browseItems, QueueItem{
								Tool: "web_browse",
								Args: map[string]interface{}{"url": u},
							})
						}
					}
					return browseItems
				}),
				Transition: func(step int, result PhaseResult, err error) string {
					return "synthesize"
				},
			},
			"synthesize": {
				Name:         "synthesize",
				AllowedTools: []string{},
				SystemPrompt: buildPhaseResearchPrompt("synthesize", config.Goal, config.TaskContext),
				StepBudget:   1,
				Pass1Target:  TargetWorker,
				Driver:       NewDeterministicQueueDriver(nil),
			},
		},
		PhaseOrder:   []string{"search", "deep_read", "synthesize"},
		InitialPhase: "search",
		MaxCycles:    1,
		Goal:         config.Goal,
	}

	runner.SynthesisPromptPrefix = func() string {
		var sources []ScrapedSource
		for url := range visitedURLs {
			sources = append(sources, ScrapedSource{URL: url})
		}
		return buildCitationPreamble(sources)
	}

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
		b.WriteString("Cite specific URLs and domain sources from the research findings for all key facts, benchmarks, and data points. ")
		b.WriteString("You have NO tools. Use the accumulated phase results as your evidence. ")
	}

	b.WriteString(fmt.Sprintf("\n\nGoal: %s", goal))
	if taskContext != "" {
		b.WriteString(fmt.Sprintf("\n\nTask Context: %s", taskContext))
	}

	return b.String()
}

// ScrapedSource is a URL with an optional title that was successfully read
// during a Research Node's deep-read phase.
type ScrapedSource struct {
	Title string
	URL   string
}

// buildCitationPreamble constructs a markdown ## Verified Sources block from
// a list of successfully scraped sources. Returns an empty string when no
// sources are provided (graceful no-op for tasks without web scraping).
//
// ADR-run32: Injected into the synthesis phase system prompt so the local
// model is anchored to verified URLs and cannot hallucinate citation links.
func buildCitationPreamble(sources []ScrapedSource) string {
	if len(sources) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Verified Sources\n\n")
	b.WriteString("The following URLs were successfully read during research. ")
	b.WriteString("IMPORTANT: You MUST cite these URLs when referencing their content. ")
	b.WriteString("Do NOT invent or hallucinate additional URLs.\n\n")
	for _, s := range sources {
		title := s.Title
		if title == "" {
			title = s.URL
		}
		b.WriteString(fmt.Sprintf("- [%s](%s)\n", title, s.URL))
	}
	b.WriteString("\n")
	return b.String()
}
