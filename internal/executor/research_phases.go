package executor

// research_phases.go — Research Node phase template for the Phase Runner (ADR-0078, ADR-0080).
//
// New node type (type: "research") for web exploration tasks. Replaces
// the pattern of Probe nodes with web tools in allowedTools.
// 4-phase pipeline: Search → Refine → Deep-Read → Synthesize.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"tzro/internal/compiler"
	"tzro/internal/embeddings"
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

	// ADR-0083: If EvidenceCards were collected during deep_read, execute the
	// 4-stage Dynamic Sectioned Map-Reduce Synthesis Pipeline for grounded synthesis
	if runner.EvidenceCardsProvider != nil {
		cards := runner.EvidenceCardsProvider()
		if len(cards) > 0 {
			fmt.Fprintf(os.Stderr, "[ResearchPhases] Executing Dynamic Sectioned Map-Reduce Synthesis for %d evidence cards\n", len(cards))
			sectionedDoc, err := RunSectionedSynthesisPipeline(ctx, synthesisEngine, queryGoal, cards)
			if err == nil && len(sectionedDoc) > 100 {
				finalSynthesis = sectionedDoc
			} else {
				fmt.Fprintf(os.Stderr, "[ResearchPhases] Sectioned synthesis fallback to phase summary (%v)\n", err)
			}
		}
	}

	if runner.SourceTracker != nil {
		finalSynthesis = runner.SourceTracker.InjectOrNormalizeReferences(finalSynthesis)
	}

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
	var discoveredResults []DiscoveredSearchResult
	visitedURLs := make(map[string]bool)
	var evidenceCards []EvidenceCard
	var secondaryQueries []string
	refinedSearchDispatched := false
	sourceTracker := NewSourceTracker()

	runner := &PhaseRunner{
		SourceTracker: sourceTracker,
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
					results := extractSearchResultsFromOutput(output)
					for _, r := range results {
						if !visitedURLs[r.URL] {
							discoveredResults = append(discoveredResults, r)
							discoveredURLs = append(discoveredURLs, r.URL)
						}
					}
					if len(results) == 0 {
						urls := extractURLsFromWebSearch(output)
						for _, u := range urls {
							if !visitedURLs[u] {
								discoveredURLs = append(discoveredURLs, u)
								discoveredResults = append(discoveredResults, DiscoveredSearchResult{URL: u})
							}
						}
					}
					if len(discoveredURLs) > 0 {
						fmt.Fprintf(os.Stderr, "[ResearchPhases] ToolPostProcess: tracked %d candidate URLs from web_search (total queue: %d)\n",
							len(results), len(discoveredURLs))
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
						card := extractEvidenceCardFromPage(context.Background(), url, output, config.Goal)
						evidenceCards = append(evidenceCards, card)
						sourceTracker.AddWebSource(url, card.Title, "", card.KeyFacts)
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
				StepBudget: func() int {
					ents := extractTargetEntities(config.Goal)
					if len(ents) >= 2 {
						b := len(ents) * 2
						if b > 8 {
							return 8
						}
						return b
					}
					return 5
				}(),
				Driver: NewDynamicQueueDriver(func() []QueueItem {
					var unvisited []DiscoveredSearchResult
					seen := make(map[string]bool)
					for _, res := range discoveredResults {
						if !visitedURLs[res.URL] && !seen[res.URL] {
							seen[res.URL] = true
							unvisited = append(unvisited, res)
						}
					}
					if len(unvisited) == 0 {
						for _, u := range discoveredURLs {
							if !visitedURLs[u] && !seen[u] {
								seen[u] = true
								unvisited = append(unvisited, DiscoveredSearchResult{URL: u})
							}
						}
					}

					// Neural semantic reranking against goal vector with domain authority weighting
					if len(unvisited) > 1 && inference.GlobalEmbeddingSidecar != nil && inference.GlobalEmbeddingSidecar.IsAvailable() {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()

						goalVec, err := inference.GlobalEmbeddingSidecar.Embed(ctx, config.Goal)
						if err == nil && len(goalVec) > 0 {
							var texts []string
							for _, r := range unvisited {
								txt := fmt.Sprintf("%s %s %s", r.Title, r.Snippet, r.URL)
								texts = append(texts, txt)
							}
							vecs, err := inference.GlobalEmbeddingSidecar.EmbedBatch(ctx, texts)
							if err == nil && len(vecs) == len(unvisited) {
								type scoredRes struct {
									res DiscoveredSearchResult
									sim float32
								}
								var scored []scoredRes
								for i, vec := range vecs {
									baseSim := inference.GlobalEmbeddingSidecar.CosineSimilarity(goalVec, vec)
									authorityFactor := calculateStructuralAuthority(unvisited[i].URL)
									rankFactor := searchRankDecay(unvisited[i].Rank)
									weightedSim := baseSim * authorityFactor * rankFactor
									scored = append(scored, scoredRes{res: unvisited[i], sim: weightedSim})
								}
								sort.Slice(scored, func(i, j int) bool {
									return scored[i].sim > scored[j].sim
								})
								unvisited = nil
								for _, s := range scored {
									unvisited = append(unvisited, s.res)
								}
							}
						}
					}

					// Entity-aware round-robin partitioning for multi-entity tasks
					entities := extractTargetEntities(config.Goal)
					targetBudget := 5
					if len(entities) >= 2 {
						targetBudget = len(entities) * 2
						if targetBudget > 8 {
							targetBudget = 8
						}
						unvisited = PartitionDiscoveredURLsByEntity(unvisited, entities, targetBudget)
					}

					var browseItems []QueueItem
					for i := 0; i < len(unvisited) && i < targetBudget; i++ {
						browseItems = append(browseItems, QueueItem{
							Tool: "web_browse",
							Args: map[string]interface{}{"url": unvisited[i].URL},
						})
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

	runner.EvidenceCardsProvider = func() []EvidenceCard {
		return evidenceCards
	}

	return runner
}

// extractTargetEntities parses a goal/prompt to identify multiple distinct comparison subjects or framework entities.
func extractTargetEntities(goal string) []string {
	if goal == "" {
		return nil
	}
	lower := strings.ToLower(goal)
	if !strings.Contains(lower, "compare") && !strings.Contains(lower, "vs") && !strings.Contains(lower, "versus") && !strings.Contains(lower, "between") {
		return nil
	}

	cleaned := goal
	prefixPatterns := []string{
		"Compare ", "compare ", "Research and compare ", "research and compare ",
		"Identify and compare ", "identify and compare ", "Conduct research on ",
	}
	for _, p := range prefixPatterns {
		if strings.HasPrefix(cleaned, p) {
			cleaned = strings.TrimPrefix(cleaned, p)
			break
		}
	}

	delims := []string{" across ", " for ", " in 20", " regarding ", " based on ", " to compare "}
	for _, d := range delims {
		if idx := strings.Index(strings.ToLower(cleaned), d); idx > 0 {
			cleaned = cleaned[:idx]
			break
		}
	}

	reReplace := regexp.MustCompile(`(?i)\b(and|&|vs\.?|versus|or)\b`)
	cleaned = reReplace.ReplaceAllString(cleaned, ",")

	parts := strings.Split(cleaned, ",")
	var entities []string
	seen := make(map[string]bool)
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		trimmed = strings.Trim(trimmed, `"'.,;:()`)
		lowerT := strings.ToLower(trimmed)
		if len(trimmed) >= 2 && !seen[lowerT] {
			if lowerT != "the" && lowerT != "a" && lowerT != "top" && lowerT != "leading" && lowerT != "frameworks" && lowerT != "engines" {
				seen[lowerT] = true
				entities = append(entities, trimmed)
			}
		}
	}

	if len(entities) >= 2 {
		return entities
	}
	return nil
}

// PartitionDiscoveredURLsByEntity groups candidate search results by target entity and picks
// round-robin with at least 1 and up to 2 slots per entity for multi-entity tasks.
func PartitionDiscoveredURLsByEntity(candidates []DiscoveredSearchResult, entities []string, maxSlots int) []DiscoveredSearchResult {
	if len(entities) <= 1 || len(candidates) <= 1 {
		if len(candidates) > maxSlots {
			return candidates[:maxSlots]
		}
		return candidates
	}

	entityBuckets := make(map[string][]DiscoveredSearchResult)
	var unassigned []DiscoveredSearchResult

	for _, cand := range candidates {
		assigned := false
		candText := strings.ToLower(cand.URL + " " + cand.Title + " " + cand.Snippet)
		for _, ent := range entities {
			if strings.Contains(candText, strings.ToLower(ent)) {
				entityBuckets[ent] = append(entityBuckets[ent], cand)
				assigned = true
				break
			}
		}
		if !assigned {
			unassigned = append(unassigned, cand)
		}
	}

	var selected []DiscoveredSearchResult
	selectedURLs := make(map[string]bool)

	// Pass 1: Round-robin 1 slot per entity
	for _, ent := range entities {
		bucket := entityBuckets[ent]
		for _, item := range bucket {
			if !selectedURLs[item.URL] && len(selected) < maxSlots {
				selectedURLs[item.URL] = true
				selected = append(selected, item)
				break
			}
		}
	}

	// Pass 2: Round-robin 2nd slot per entity (up to 2 per entity)
	for _, ent := range entities {
		bucket := entityBuckets[ent]
		countForEntity := 0
		for _, s := range selected {
			sText := strings.ToLower(s.URL + " " + s.Title + " " + s.Snippet)
			if strings.Contains(sText, strings.ToLower(ent)) {
				countForEntity++
			}
		}
		if countForEntity < 2 {
			for _, item := range bucket {
				if !selectedURLs[item.URL] && len(selected) < maxSlots {
					selectedURLs[item.URL] = true
					selected = append(selected, item)
					break
				}
			}
		}
	}

	// Pass 3: Fill remaining slots from unassigned or remaining bucket items
	for _, cand := range candidates {
		if !selectedURLs[cand.URL] && len(selected) < maxSlots {
			selectedURLs[cand.URL] = true
			selected = append(selected, cand)
		}
	}

	return selected
}

// calculateStructuralAuthority computes a generalized, domain-agnostic quality multiplier
// based on URL path topology, documentation hierarchy, and structural signals.
func calculateStructuralAuthority(u string) float32 {
	lower := strings.ToLower(u)
	multiplier := float32(1.0)

	// 1. Institutional / non-commercial TLD trust signals
	if strings.Contains(lower, ".gov") || strings.Contains(lower, ".edu") {
		multiplier *= 1.25
	} else if strings.Contains(lower, ".org") {
		multiplier *= 1.10
	}

	// 2. Canonical documentation / developer reference subdomains
	if strings.Contains(lower, "://docs.") || strings.Contains(lower, "://developer.") ||
		strings.Contains(lower, "://api.") || strings.Contains(lower, "://pkg.") ||
		strings.Contains(lower, "://guide.") || strings.Contains(lower, "://manual.") {
		multiplier *= 1.20
	}

	// 3. Canonical documentation / release / specification URL paths
	if strings.Contains(lower, "/docs/") || strings.Contains(lower, "/documentation/") ||
		strings.Contains(lower, "/api/") || strings.Contains(lower, "/releases/") ||
		strings.Contains(lower, "/reference/") || strings.Contains(lower, "/spec/") ||
		strings.Contains(lower, "/manual/") || strings.Contains(lower, "/guide/") ||
		strings.Contains(lower, "/blob/") || strings.Contains(lower, "/tree/") {
		multiplier *= 1.15
	}

	// 4. Boost primary open source repositories
	if strings.Contains(lower, "github.com/") || strings.Contains(lower, "gitlab.com/") {
		multiplier *= 1.20
	}

	// 5. Dampen speculative listicles, SEO aggregate tag pages, and generic blogs
	if strings.Contains(lower, "/blog/") || strings.Contains(lower, "/posts/") ||
		strings.Contains(lower, "/article/") || strings.Contains(lower, "showdown") ||
		strings.Contains(lower, "top-10") || strings.Contains(lower, "best-") {
		multiplier *= 0.85
	}
	if strings.Contains(lower, "/tag/") || strings.Contains(lower, "/category/") ||
		strings.Contains(lower, "?q=") || strings.Contains(lower, "?query=") {
		multiplier *= 0.80
	}

	// 6. Explicitly penalize known SEO aggregator / AI-generated scraper domains
	if strings.Contains(lower, "markaicode.com") || strings.Contains(lower, "local-llm.net") ||
		strings.Contains(lower, "oflight.co.jp") || strings.Contains(lower, "aimultiple.com") ||
		strings.Contains(lower, "artificial-intelligence-wiki.com") || strings.Contains(lower, "eonsr.com") {
		multiplier *= 0.40
	}

	return multiplier
}

// searchRankDecay computes rank decay from search engine ordering.
func searchRankDecay(rank int) float32 {
	if rank <= 0 {
		return 1.0
	}
	// Smooth reciprocal decay: rank 0 -> 1.0, rank 1 -> 0.89, rank 2 -> 0.80, rank 5 -> 0.62
	return 1.0 / (1.0 + 0.12*float32(rank))
}

// DiscoveredSearchResult represents a search result item discovered during research.
type DiscoveredSearchResult struct {
	URL     string
	Title   string
	Snippet string
	Rank    int
}

func extractSearchResultsFromOutput(toolOutput string) []DiscoveredSearchResult {
	var results []DiscoveredSearchResult

	var envelope struct {
		Data struct {
			Results []struct {
				URL     string `json:"url"`
				Title   string `json:"title"`
				Snippet string `json:"snippet"`
			} `json:"results"`
		} `json:"data"`
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if json.Unmarshal([]byte(toolOutput), &envelope) == nil {
		sourceList := envelope.Data.Results
		if len(sourceList) == 0 {
			sourceList = envelope.Results
		}
		for i, r := range sourceList {
			if r.URL != "" {
				results = append(results, DiscoveredSearchResult{
					URL:     r.URL,
					Title:   r.Title,
					Snippet: r.Snippet,
					Rank:    i,
				})
			}
		}
	}
	return results
}

// extractSecondaryQueriesFromOutput generates targeted second-order queries from search snippets (ADR-0080).
func extractSecondaryQueriesFromOutput(goal, toolOutput string) []string {
	var queries []string
	lowerGoal := strings.ToLower(goal)

	// If goal mentions vulnerabilities/CVEs, target official vulnerability database & announcements
	if strings.Contains(lowerGoal, "vulnerabilit") || strings.Contains(lowerGoal, "cve") || strings.Contains(lowerGoal, "advisory") {
		if strings.Contains(lowerGoal, "go") || strings.Contains(lowerGoal, "golang") {
			queries = append(queries, "site:pkg.go.dev/vuln Go standard library security")
			queries = append(queries, "Go standard library security release announcement 2024 2025")
		} else {
			queries = append(queries, "site:nvd.nist.gov "+extractSearchQueryFromGoal(goal))
		}
	}

	// If goal mentions frameworks/comparisons, target benchmark & pricing metrics
	if strings.Contains(lowerGoal, "compare") || strings.Contains(lowerGoal, "framework") || strings.Contains(lowerGoal, "vs") {
		base := extractSearchQueryFromGoal(goal)
		queries = append(queries, base+" pricing enterprise open source")
		queries = append(queries, base+" benchmark latency throughput")
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
// and structured data using neural embedding cosine similarity and k-nearest neighbor (k-NN) ranking.
func extractEvidenceCardFromPage(ctx context.Context, url, content, goal string) EvidenceCard {
	card := EvidenceCard{
		URL: url,
	}

	pageText := content

	// Unpack JSON tool result envelope if present (standard ToolResult with Data or top-level)
	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			Content string `json:"content"`
			URL     string `json:"url"`
			Text    string `json:"text"`
		} `json:"data"`
		Content string `json:"content"`
		URL     string `json:"url"`
		Output  string `json:"output"`
	}
	if json.Unmarshal([]byte(content), &envelope) == nil {
		if envelope.Data.Content != "" {
			pageText = envelope.Data.Content
		} else if envelope.Data.Text != "" {
			pageText = envelope.Data.Text
		} else if envelope.Content != "" {
			pageText = envelope.Content
		} else if envelope.Output != "" {
			pageText = envelope.Output
		}
		if card.URL == "" {
			if envelope.Data.URL != "" {
				card.URL = envelope.Data.URL
			} else if envelope.URL != "" {
				card.URL = envelope.URL
			}
		}
	}

	// Normalize escaped newlines if unescaping was partial
	pageText = strings.ReplaceAll(pageText, "\r\n", "\n")
	pageText = strings.ReplaceAll(pageText, "\r", "\n")

	lines := strings.Split(pageText, "\n")
	var rawCandidates []string
	var tableBlocks []string

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

	// 2. Structural Parsing: extract candidate paragraphs and table rows/blocks
	var currentTable []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if len(trimmed) < 10 {
			if len(currentTable) > 0 {
				tableBlocks = append(tableBlocks, strings.Join(currentTable, "\n"))
				currentTable = nil
			}
			continue
		}

		// Markdown Table Row
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			currentTable = append(currentTable, trimmed)
			if len(currentTable) >= 6 {
				tableBlocks = append(tableBlocks, strings.Join(currentTable, "\n"))
				currentTable = nil
			}
			continue
		} else if len(currentTable) > 0 {
			tableBlocks = append(tableBlocks, strings.Join(currentTable, "\n"))
			currentTable = nil
		}

		cleanText := strings.TrimLeft(trimmed, "-*# \t•")
		cleanText = strings.TrimSpace(cleanText)
		if len(cleanText) >= 15 {
			rawCandidates = append(rawCandidates, cleanText)
		}
	}
	if len(currentTable) > 0 {
		tableBlocks = append(tableBlocks, strings.Join(currentTable, "\n"))
	}

	// Pool all candidates (text chunks + table blocks)
	var allCandidates []string
	seen := make(map[string]bool)
	for _, c := range rawCandidates {
		norm := strings.ToLower(c)
		if !seen[norm] && len(c) <= 1500 {
			seen[norm] = true
			allCandidates = append(allCandidates, c)
		}
	}
	for _, tb := range tableBlocks {
		if !seen[tb] {
			seen[tb] = true
			allCandidates = append(allCandidates, tb)
		}
	}

	if len(allCandidates) == 0 {
		return card
	}

	targetGoal := goal
	if targetGoal == "" {
		targetGoal = card.Title
	}

	// 3. Neural Embedding & k-NN Ranking
	type candidateScore struct {
		text  string
		score float32
	}
	var scored []candidateScore

	if inference.GlobalEmbeddingSidecar != nil && inference.GlobalEmbeddingSidecar.IsAvailable() {
		embCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		goalVec, err := inference.GlobalEmbeddingSidecar.Embed(embCtx, targetGoal)
		if err == nil && len(goalVec) > 0 {
			// Chunk batch requests in groups of 16 to respect sidecar constraints
			const batchChunkSize = 16
			for i := 0; i < len(allCandidates); i += batchChunkSize {
				end := i + batchChunkSize
				if end > len(allCandidates) {
					end = len(allCandidates)
				}
				chunk := allCandidates[i:end]
				vecs, err := inference.GlobalEmbeddingSidecar.EmbedBatch(embCtx, chunk)
				if err == nil && len(vecs) == len(chunk) {
					for j, vec := range vecs {
						sim := inference.GlobalEmbeddingSidecar.CosineSimilarity(goalVec, vec)
						scored = append(scored, candidateScore{text: chunk[j], score: sim})
					}
				}
			}
		}
	}

	// Fallback to pure Go cosine similarity if neural sidecar was unavailable
	if len(scored) == 0 {
		for _, c := range allCandidates {
			sim := float32(embeddings.CosineSimilarity(targetGoal, c))
			scored = append(scored, candidateScore{text: c, score: sim})
		}
	}

	// Sort candidates by descending similarity score (k-NN)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Select top-k nearest neighbors (k = 8)
	const kNN = 8
	var topFacts []string
	for i := 0; i < len(scored) && i < kNN; i++ {
		topFacts = append(topFacts, scored[i].text)
	}

	card.KeyFacts = topFacts
	return card
}

// buildCitationPreamble constructs a markdown ## Verified Sources block from
// a list of successfully scraped sources and evidence cards.
func buildCitationPreamble(sources []ScrapedSource, cards []EvidenceCard) string {
	if len(sources) == 0 && len(cards) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Verified Research Evidence & Numbered Sources\n\n")
	b.WriteString("The following URLs were successfully read during research. ")
	b.WriteString("IMPORTANT: Every factual claim, version, and quantitative metric in your response MUST include an inline numbered citation tag (e.g. [1], [2]) citing these sources.\n")
	b.WriteString("Do NOT invent or hallucinate additional URLs, future CVE IDs, or unvisited sources. If data is not in the sources, write 'Not reported in sources'.\n\n")

	if len(cards) > 0 {
		b.WriteString("### Numbered Bibliography (Use inline tags [1], [2] in your analysis):\n")
		for i, card := range cards {
			title := card.Title
			if title == "" || title == card.URL {
				title = "Documentation"
			}
			b.WriteString(fmt.Sprintf("[%d] [%s](%s)\n", i+1, title, card.URL))
		}
		b.WriteString("\n")

		for i, card := range cards {
			title := card.Title
			if title == "" || title == card.URL {
				title = "Documentation"
			}
			b.WriteString(fmt.Sprintf("### [%d] [%s](%s)\n", i+1, title, card.URL))
			if len(card.KeyFacts) > 0 {
				b.WriteString("Key Evidence:\n")
				for _, f := range card.KeyFacts {
					b.WriteString(fmt.Sprintf("- %s\n", f))
				}
			}
			b.WriteString("\n")
		}
	} else {
		b.WriteString("### Numbered Bibliography:\n")
		for i, s := range sources {
			title := s.Title
			if title == "" {
				title = s.URL
			}
			b.WriteString(fmt.Sprintf("[%d] [%s](%s)\n", i+1, title, s.URL))
		}
		b.WriteString("\n")
	}

	return b.String()
}
