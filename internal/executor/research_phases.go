package executor

// research_phases.go — Research Node phase template for the Phase Runner (ADR-0078, ADR-0080).
//
// New node type (type: "research") for web exploration tasks. Replaces
// the pattern of Probe nodes with web tools in allowedTools.
// 4-phase pipeline: Search → Refine → Deep-Read → Synthesize.

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"tzro/internal/compiler"
	"tzro/internal/inference"
)

// RunResearchPhases executes a Research Node using the Phase Runner with the
// Deterministic Web Research Pipeline (ADR-0078, ADR-0080): Query Generation → Two-Stage Search → Deep-Read → Synthesize.
func RunResearchPhases(
	ctx context.Context,
	taskID, probeID string,
	config compiler.ProbeConfig,
	engine ProbeInferenceEngine,
	synthesisEngine ProbeInferenceEngine,
	downstreamBindingKeys []string,
) (string, error) {
	// ADR-0080: Inject DRY Sampling and Presence Penalty on Research synthesis context
	// to eliminate 4B token attractor repetition loops.
	ctx = context.WithValue(ctx, inference.DRYSamplingKey, inference.DRYSamplingConfig{
		Multiplier:    0.8,
		Base:          1.75,
		AllowedLength: 2,
	})
	ctx = context.WithValue(ctx, inference.PresencePenaltyKey, 0.2)

	// Stage 1: Generate 2-4 diverse search queries using 1-shot Worker inference
	// Use TaskContext if present to capture all required entities, dimensions, and constraints.
	queryGoal := config.TaskContext
	if queryGoal == "" {
		queryGoal = config.Goal
	} else if config.Goal != "" && !strings.Contains(queryGoal, config.Goal) {
		queryGoal = config.Goal + "\n" + queryGoal
	}

	queries, err := GenerateSearchQueries(ctx, engine, queryGoal)
	if err != nil || len(queries) == 0 {
		queries = []string{extractSearchQueryFromGoal(queryGoal)}
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
	var evidenceCards []EvidenceCard
	var secondaryQueries []string
	refinedSearchDispatched := false

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
					// ADR-0080: Extract secondary search hints from first-phase search results
					if !refinedSearchDispatched && len(secondaryQueries) < 2 {
						followUps := extractSecondaryQueriesFromOutput(config.Goal, output)
						for _, fq := range followUps {
							if len(secondaryQueries) < 2 {
								secondaryQueries = append(secondaryQueries, fq)
							}
						}
					}
				}
			case "web_browse":
				if err == nil {
					if url, ok := args["url"].(string); ok && url != "" {
						visitedURLs[url] = true
						card := extractEvidenceCardFromPage(url, output)
						evidenceCards = append(evidenceCards, card)
						fmt.Fprintf(os.Stderr, "[ResearchPhases] Ingested EvidenceCard for %s (%d facts extracted)\n", url, len(card.KeyFacts))
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
					if len(secondaryQueries) > 0 && !refinedSearchDispatched {
						return "refine"
					}
					return "deep_read"
				},
			},
			"refine": {
				Name:         "refine",
				AllowedTools: []string{"web_search"},
				SystemPrompt: buildPhaseResearchPrompt("refine", config.Goal, config.TaskContext),
				StepBudget:   2,
				Driver: NewDynamicQueueDriver(func() []QueueItem {
					refinedSearchDispatched = true
					var items []QueueItem
					for _, q := range secondaryQueries {
						items = append(items, QueueItem{
							Tool: "web_search",
							Args: map[string]interface{}{"query": q},
						})
					}
					return items
				}),
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
				Grammar:      buildResearchMarkdownGrammar(config.Goal),
				Driver:       NewDeterministicQueueDriver(nil),
			},
		},
		PhaseOrder:   []string{"search", "refine", "deep_read", "synthesize"},
		InitialPhase: "search",
		MaxCycles:    1,
		Goal:         config.Goal,
	}

	runner.SynthesisPromptPrefix = func() string {
		var sources []ScrapedSource
		for url := range visitedURLs {
			sources = append(sources, ScrapedSource{URL: url})
		}
		return buildCitationPreamble(sources, evidenceCards)
	}

	return runner
}

// extractSecondaryQueriesFromOutput generates targeted second-order queries from search snippets (ADR-0080).
func extractSecondaryQueriesFromOutput(goal, toolOutput string) []string {
	var queries []string
	lowerGoal := strings.ToLower(goal)

	// If goal mentions vulnerabilities/CVEs, target official vulnerability database
	if strings.Contains(lowerGoal, "vulnerabilit") || strings.Contains(lowerGoal, "cve") || strings.Contains(lowerGoal, "advisory") {
		if strings.Contains(lowerGoal, "go") || strings.Contains(lowerGoal, "golang") {
			queries = append(queries, "site:pkg.go.dev/vuln Go standard library security")
		} else {
			queries = append(queries, "site:nvd.nist.gov "+extractSearchQueryFromGoal(goal))
		}
	}

	// If goal mentions frameworks/comparisons, target benchmark & pricing metrics
	if strings.Contains(lowerGoal, "compare") || strings.Contains(lowerGoal, "framework") || strings.Contains(lowerGoal, "vs") {
		base := extractSearchQueryFromGoal(goal)
		queries = append(queries, base+" benchmarks latency throughput")
	}

	// Extract domain-level search targets from discovered URLs in output
	urlRe := regexp.MustCompile(`https?://([^/\s]+)`)
	matches := urlRe.FindAllStringSubmatch(toolOutput, 5)
	for _, m := range matches {
		if len(m) > 1 {
			domain := m[1]
			if !strings.Contains(domain, "google") && !strings.Contains(domain, "bing") && !strings.Contains(domain, "duckduckgo") {
				targetQuery := fmt.Sprintf("site:%s %s", domain, extractSearchQueryFromGoal(goal))
				queries = append(queries, targetQuery)
				break
			}
		}
	}

	return queries
}

// buildPhaseResearchPrompt constructs a phase-specific system prompt for Research nodes.
func buildPhaseResearchPrompt(phase, goal, taskContext string) string {
	var b strings.Builder

	switch phase {
	case "search":
		b.WriteString("You are researching a topic on the web. ")
		b.WriteString("PHASE: SEARCH — use web_search to find relevant sources and URLs. ")
		b.WriteString("Use web_search ONLY. Extract search queries from the goal. ")
	case "refine":
		b.WriteString("You are refining search queries to target specific authoritative sources. ")
		b.WriteString("PHASE: REFINE — execute targeted second-order searches. ")
		b.WriteString("Use web_search ONLY. ")
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
		b.WriteString("Format citations inline using markdown links: [Source Title](URL). ")
		b.WriteString("You MUST include a '# ' main title, '## ' section analyses, a '## Comparative Overview' table, and a '## Sources & Citations' list. ")
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

// EvidenceCard holds structured findings extracted from a single verified web page.
type EvidenceCard struct {
	URL      string
	Title    string
	KeyFacts []string
}

// extractEvidenceCardFromPage parses scraped page content into concise bullet points
// and structured data using deterministic structural parsing and entity density scoring (ADR-0080).
func extractEvidenceCardFromPage(url, content string) EvidenceCard {
	card := EvidenceCard{
		URL: url,
	}

	lines := strings.Split(content, "\n")
	var rawCandidates []string
	var tableRows []string

	// 1. Extract Page Title
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if len(trimmed) < 2 {
			continue
		}
		if card.Title == "" {
			if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") {
				card.Title = strings.TrimSpace(strings.TrimLeft(trimmed, "# "))
				continue
			}
			if strings.HasPrefix(trimmed, "<title>") && strings.Contains(trimmed, "</title>") {
				card.Title = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "<title>"), "</title>"))
				continue
			}
			if strings.HasPrefix(strings.ToLower(trimmed), "title:") {
				card.Title = strings.TrimSpace(trimmed[6:])
				continue
			}
		}
	}
	if card.Title == "" {
		card.Title = url
	}

	// 2. Structural Parsing: Table rows & Key-Value / Bullet points
	tableDividerRe := regexp.MustCompile(`^\|\s*[-:]+\s*\|`)
	kvRe := regexp.MustCompile(`(?i)^\s*(\*\*|\*|__)?([a-zA-Z0-9_\-\s]{2,30})(\*\*|\*|__)?\s*[:=]\s*(.+)$`)
	cveRe := regexp.MustCompile(`(?i)\bCVE-\d{4}-\d{4,7}\b`)
	metricRe := regexp.MustCompile(`(?i)\b\d+(\.\d+)?(%|ms|s|t/s|x|gb|mb|kb|tokens|req/s|\$)?\b`)
	acronymRe := regexp.MustCompile(`\b[A-Z]{2,}\b`)
	keywordRe := regexp.MustCompile(`(?i)\b(cve|vulnerability|benchmark|version|throughput|latency|release|support|feature|architecture|pricing|performance|method|sdk|framework|loader|quantization|model)\b`)
	noiseRe := regexp.MustCompile(`(?i)\b(cookie|subscribe|privacy policy|all rights reserved|sign up|terms of service|click here|javascript|404 not found|login|menu|navigation)\b`)

	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if len(trimmed) < 15 || noiseRe.MatchString(trimmed) {
			continue
		}

		// Markdown Table Row
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			if !tableDividerRe.MatchString(trimmed) && len(tableRows) < 4 {
				cleanRow := strings.TrimSpace(trimmed)
				tableRows = append(tableRows, cleanRow)
			}
			continue
		}

		// Key-Value Definition
		if m := kvRe.FindStringSubmatch(trimmed); len(m) >= 5 {
			k := strings.TrimSpace(m[2])
			v := strings.TrimSpace(m[4])
			if len(v) > 5 && !noiseRe.MatchString(k) && !noiseRe.MatchString(v) {
				rawCandidates = append(rawCandidates, fmt.Sprintf("**%s:** %s", k, v))
				continue
			}
		}

		// Markdown Bullet or Factual Sentence
		cleanText := strings.TrimLeft(trimmed, "-*# \t")
		if len(cleanText) >= 20 {
			rawCandidates = append(rawCandidates, cleanText)
		}
	}

	// 3. Density-Score Sentence Candidates
	type scoredFact struct {
		text  string
		score float64
	}
	var scored []scoredFact
	seen := make(map[string]bool)

	for _, text := range rawCandidates {
		norm := strings.ToLower(text)
		if seen[norm] {
			continue
		}
		seen[norm] = true

		score := 0.0
		// Score CVEs highly
		cveCount := len(cveRe.FindAllString(text, -1))
		score += float64(cveCount) * 6.0

		// Score numbers/metrics
		metricCount := len(metricRe.FindAllString(text, -1))
		score += float64(metricCount) * 2.0

		// Score uppercase acronyms
		acronymCount := len(acronymRe.FindAllString(text, -1))
		score += float64(acronymCount) * 1.5

		// Score technical keywords
		keywordCount := len(keywordRe.FindAllString(text, -1))
		score += float64(keywordCount) * 2.0

		// Penalize very long run-on sentences (> 300 chars)
		if len(text) > 300 {
			score -= 2.0
		}

		if score >= 2.0 {
			scored = append(scored, scoredFact{text: text, score: score})
		}
	}

	// Sort candidates by descending density score
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	var facts []string
	for _, s := range scored {
		facts = append(facts, s.text)
		if len(facts) >= 6 {
			break
		}
	}

	// Append table rows if present to preserve comparison structure
	if len(tableRows) > 0 {
		facts = append(facts, tableRows...)
	}

	// Fallback: If no dense facts found, use top non-empty lines
	if len(facts) == 0 && len(rawCandidates) > 0 {
		for _, c := range rawCandidates {
			facts = append(facts, c)
			if len(facts) >= 3 {
				break
			}
		}
	}

	card.KeyFacts = facts
	return card
}

// buildCitationPreamble constructs a markdown ## Verified Sources block from
// a list of successfully scraped sources and evidence cards.
func buildCitationPreamble(sources []ScrapedSource, cards []EvidenceCard) string {
	if len(sources) == 0 && len(cards) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Verified Research Evidence & Sources\n\n")
	b.WriteString("The following URLs were successfully read during research. ")
	b.WriteString("IMPORTANT: You MUST cite these exact URLs when referencing facts or data points.\n")
	b.WriteString("Do NOT invent or hallucinate additional URLs, future CVE IDs, or unvisited sources.\n\n")

	if len(cards) > 0 {
		for _, card := range cards {
			b.WriteString(fmt.Sprintf("### [%s](%s)\n", card.Title, card.URL))
			if len(card.KeyFacts) > 0 {
				b.WriteString("Key Evidence:\n")
				for _, f := range card.KeyFacts {
					b.WriteString(fmt.Sprintf("- %s\n", f))
				}
			}
			b.WriteString("\n")
		}
	} else {
		for _, s := range sources {
			title := s.Title
			if title == "" {
				title = s.URL
			}
			b.WriteString(fmt.Sprintf("- [%s](%s)\n", title, s.URL))
		}
		b.WriteString("\n")
	}

	return b.String()
}
