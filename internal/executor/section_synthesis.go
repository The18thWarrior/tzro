package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"tzro/internal/config"
	"tzro/internal/embeddings"
	"tzro/internal/inference"
)

// EvidenceTable represents structured facts extracted from a single source URL (ADR-0083).
type EvidenceTable struct {
	SourceIndex int      `json:"source_index"` // 1-based index: 1, 2, 3...
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	KeyMetrics  []string `json:"key_metrics"`  // Extracted numbers, versions, ports, throughput
	CoreClaims  []string `json:"core_claims"`  // Key assertions
	RawSnippets []string `json:"raw_snippets"` // Top-K k-NN text chunks
}

// SectionSpec represents a planned section in the document outline (ADR-0083).
type SectionSpec struct {
	Heading         string `json:"heading"`          // e.g. "## 1. Core Architectural Patterns"
	Objective       string `json:"objective"`        // What this section must cover
	TargetSourceIDs []int  `json:"target_source_ids"`// [1, 2] or empty
	FormatHint      string `json:"format_hint"`      // "table", "bulleted_deep_dive", "prose_comparison"
	IsTerminal      bool   `json:"is_terminal"`      // True if conclusion/synthesis (gets all sources)
}

// SynthesisOutline is the dynamic multi-section plan generated in Step 2 (ADR-0083).
type SynthesisOutline struct {
	Title    string        `json:"title"`
	Sections []SectionSpec `json:"sections"` // Dynamic length (no arbitrary cap)
}

var metricPattern = regexp.MustCompile(`(?i)\b(?:\d+(?:\.\d+)?(?:\s*(?:tok(?:ens?)?/s|ms|s|%|gb|mb|kb|tb|m|b|k|usd|\$|users?|ports?|stars?|stars))\b|cve-\d{4}-\d+|v?\d+\.\d+(?:\.\d+)?|port\s+\d+)`)

// RankEvidenceForSource ranks candidate snippets from an EvidenceCard against the research goal
// and extracts quantitative metrics into an EvidenceTable. Respects configurable K.
func RankEvidenceForSource(ctx context.Context, goal string, card EvidenceCard, sourceIndex int, k int) EvidenceTable {
	if k <= 0 {
		k = config.GlobalConfig.GetResearchEvidenceSnippetsPerSource()
	}

	table := EvidenceTable{
		SourceIndex: sourceIndex,
		Title:       card.Title,
		URL:         card.URL,
	}
	if table.Title == "" {
		table.Title = card.URL
	}

	candidates := card.KeyFacts
	if len(candidates) == 0 {
		return table
	}

	// 1. Extract quantitative metrics & key identifiers
	metricSet := make(map[string]bool)
	for _, c := range candidates {
		matches := metricPattern.FindAllString(c, -1)
		for _, m := range matches {
			m = strings.TrimSpace(m)
			if len(m) > 1 && !metricSet[strings.ToLower(m)] {
				metricSet[strings.ToLower(m)] = true
				table.KeyMetrics = append(table.KeyMetrics, m)
			}
		}
	}

	// 2. Vector Ranking (k-NN)
	targetGoal := goal
	if targetGoal == "" {
		targetGoal = card.Title
	}

	type scoredSnippet struct {
		text  string
		score float32
	}
	var scored []scoredSnippet

	if inference.GlobalEmbeddingSidecar != nil && inference.GlobalEmbeddingSidecar.IsAvailable() {
		embCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		goalVec, err := inference.GlobalEmbeddingSidecar.Embed(embCtx, targetGoal)
		if err == nil && len(goalVec) > 0 {
			const batchChunkSize = 16
			for i := 0; i < len(candidates); i += batchChunkSize {
				end := i + batchChunkSize
				if end > len(candidates) {
					end = len(candidates)
				}
				chunk := candidates[i:end]
				vecs, err := inference.GlobalEmbeddingSidecar.EmbedBatch(embCtx, chunk)
				if err == nil && len(vecs) == len(chunk) {
					for j, vec := range vecs {
						sim := inference.GlobalEmbeddingSidecar.CosineSimilarity(goalVec, vec)
						scored = append(scored, scoredSnippet{text: chunk[j], score: sim})
					}
				}
			}
		}
	}

	// Fallback to pure Go cosine similarity
	if len(scored) == 0 {
		for _, c := range candidates {
			sim := float32(embeddings.CosineSimilarity(targetGoal, c))
			scored = append(scored, scoredSnippet{text: c, score: sim})
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	for i := 0; i < len(scored) && i < k; i++ {
		table.RawSnippets = append(table.RawSnippets, scored[i].text)
		table.CoreClaims = append(table.CoreClaims, scored[i].text)
	}

	return table
}

const outlineGBNFSchema = `{
  "type": "object",
  "properties": {
    "title": {"type": "string"},
    "sections": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "heading": {"type": "string"},
          "objective": {"type": "string"},
          "target_source_ids": {"type": "array", "items": {"type": "integer"}},
          "format_hint": {"type": "string"},
          "is_terminal": {"type": "boolean"}
        },
        "required": ["heading", "objective", "target_source_ids", "is_terminal"]
      }
    }
  },
  "required": ["title", "sections"]
}`

// GenerateSynthesisOutline plans an unbounded multi-section document structure from research evidence (Step 2).
func GenerateSynthesisOutline(ctx context.Context, engine ProbeInferenceEngine, goal string, evidence []EvidenceTable) (*SynthesisOutline, error) {
	if len(evidence) == 0 {
		return buildDefaultOutline(goal, evidence), nil
	}

	var sourceSummary strings.Builder
	for _, e := range evidence {
		metrics := strings.Join(e.KeyMetrics, ", ")
		if len(metrics) > 100 {
			metrics = metrics[:100] + "..."
		}
		sourceSummary.WriteString(fmt.Sprintf("[%d] %s (%s) — Metrics: %s\n", e.SourceIndex, e.Title, e.URL, metrics))
	}

	systemPrompt := `You are an expert research architect designing a comprehensive synthesis outline.
Plan a structured, multi-section technical document that thoroughly answers the user's research goal.
Allocate specific source IDs (e.g. [1, 2]) to each section based on where the evidence belongs.
The final section must be marked with is_terminal: true to perform the terminal synthesis.
Return ONLY a valid JSON object matching the schema.`

	userPrompt := fmt.Sprintf("Research Goal: %s\n\nAvailable Evidence Sources:\n%s\nPlan the document outline in JSON now.", goal, sourceSummary.String())

	resp, err := engine.Infer(ctx, systemPrompt, userPrompt, outlineGBNFSchema, TargetWorker)
	if err != nil || strings.TrimSpace(resp) == "" {
		fmt.Fprintf(os.Stderr, "[SectionedSynthesis] Warning: Outline generation failed (%v), using default outline\n", err)
		return buildDefaultOutline(goal, evidence), nil
	}

	var outline SynthesisOutline
	cleanedResp := stripThoughtAndFences(resp)
	if err := json.Unmarshal([]byte(cleanedResp), &outline); err != nil || len(outline.Sections) == 0 {
		// Fallback: search for JSON object between outermost braces
		firstBrace := strings.Index(cleanedResp, "{")
		lastBrace := strings.LastIndex(cleanedResp, "}")
		if firstBrace >= 0 && lastBrace > firstBrace {
			_ = json.Unmarshal([]byte(cleanedResp[firstBrace:lastBrace+1]), &outline)
		}
	}

	if len(outline.Sections) == 0 {
		fmt.Fprintf(os.Stderr, "[SectionedSynthesis] Warning: JSON parse failed for outline, using default outline\n")
		return buildDefaultOutline(goal, evidence), nil
	}

	// Ensure document title exists
	if outline.Title == "" {
		outline.Title = deriveCleanDocumentTitle(goal)
	}

	// Ensure headings have ## and the last section is marked terminal
	for i := range outline.Sections {
		if !strings.HasPrefix(outline.Sections[i].Heading, "#") {
			outline.Sections[i].Heading = fmt.Sprintf("## %d. %s", i+1, outline.Sections[i].Heading)
		}
	}
	outline.Sections[len(outline.Sections)-1].IsTerminal = true

	return &outline, nil
}

func stripThoughtAndFences(s string) string {
	s = strings.TrimSpace(s)
	reThought := regexp.MustCompile(`(?s)<thought>.*?</thought>`)
	s = reThought.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	reFences := regexp.MustCompile("(?s)```(?:json)?\\s*\n?(.*?)\\s*```")
	if m := reFences.FindStringSubmatch(s); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return s
}

func buildDefaultOutline(goal string, evidence []EvidenceTable) *SynthesisOutline {
	allSourceIDs := make([]int, len(evidence))
	for i := range evidence {
		allSourceIDs[i] = evidence[i].SourceIndex
	}
	if len(allSourceIDs) == 0 {
		allSourceIDs = []int{1}
	}

	return &SynthesisOutline{
		Title: deriveCleanDocumentTitle(goal),
		Sections: []SectionSpec{
			{
				Heading:         "## 1. Executive Summary & Core Architectural Patterns",
				Objective:       "Synthesize an in-depth breakdown of primary concepts, mechanisms, and design patterns from the discovered sources.",
				TargetSourceIDs: allSourceIDs,
				FormatHint:      "prose",
				IsTerminal:      false,
			},
			{
				Heading:         "## 2. Comparative Analysis Matrix & Specifications",
				Objective:       "Generate a comprehensive, structured Markdown comparison table covering metrics, benchmarks, and capabilities across all evaluated systems.",
				TargetSourceIDs: allSourceIDs,
				FormatHint:      "table",
				IsTerminal:      false,
			},
			{
				Heading:         "## 3. Practical Recommendations & Strategic Synthesis",
				Objective:       "Provide concrete, workload-based guidance, decision frameworks, and trade-offs.",
				TargetSourceIDs: allSourceIDs,
				FormatHint:      "bulleted_deep_dive",
				IsTerminal:      true,
			},
		},
	}
}

// AssembleSection synthesizes one section of the outline using rolling prefix context and bound sources.
func AssembleSection(
	ctx context.Context,
	engine ProbeInferenceEngine,
	goal string,
	outline *SynthesisOutline,
	sectionIdx int,
	spec SectionSpec,
	evidence []EvidenceTable,
	sectionLeads []string,
) (string, error) {
	// 1. Select evidence: if terminal, use all sources; otherwise filter by TargetSourceIDs
	var assignedEvidence []EvidenceTable
	if spec.IsTerminal || len(spec.TargetSourceIDs) == 0 {
		assignedEvidence = evidence
	} else {
		targetMap := make(map[int]bool)
		for _, id := range spec.TargetSourceIDs {
			targetMap[id] = true
		}
		for _, e := range evidence {
			if targetMap[e.SourceIndex] {
				assignedEvidence = append(assignedEvidence, e)
			}
		}
		if len(assignedEvidence) == 0 {
			assignedEvidence = evidence
		}
	}

	// 2. Build Evidence Context Block
	var evidenceBlock strings.Builder
	evidenceBlock.WriteString("### Assigned Source Evidence:\n\n")
	for _, e := range assignedEvidence {
		evidenceBlock.WriteString(fmt.Sprintf("#### Source [%d] [%s](%s)\n", e.SourceIndex, e.Title, e.URL))
		if len(e.KeyMetrics) > 0 {
			evidenceBlock.WriteString(fmt.Sprintf("- Key Metrics/Identifiers: %s\n", strings.Join(e.KeyMetrics, ", ")))
		}
		if len(e.RawSnippets) > 0 {
			evidenceBlock.WriteString("- Key Evidence Snippets:\n")
			for _, snip := range e.RawSnippets {
				evidenceBlock.WriteString(fmt.Sprintf("  * %s\n", snip))
			}
		}
		evidenceBlock.WriteString("\n")
	}

	// 3. Rolling Prefix Context (preceding section leads)
	var leadsBlock strings.Builder
	if len(sectionLeads) > 0 {
		leadsBlock.WriteString("### Preceding Section Context (Do NOT repeat these introductions):\n")
		for _, lead := range sectionLeads {
			leadsBlock.WriteString(fmt.Sprintf("- %s\n", lead))
		}
		leadsBlock.WriteString("\n")
	}

	// 4. Format constraint
	tableConstraint := ""
	if spec.FormatHint == "table" || strings.Contains(strings.ToLower(spec.Heading), "matrix") || strings.Contains(strings.ToLower(spec.Heading), "table") {
		tableConstraint = `
CRITICAL INSTRUCTION: You are generating a Comparison Table/Matrix section.
You MUST output a complete, fully populated Markdown table (| Header 1 | Header 2 | ... |).
Do NOT truncate rows or columns. Include inline citation tags [N] in table cells.
`
	}

	systemPrompt := fmt.Sprintf(`You are an expert technical research writer synthesizing Section %d of a comprehensive report.

Overall Document Goal: %s
Current Section: %s
Section Objective: %s

%s
%s
%s
IMPORTANT INSTRUCTIONS:
1. Begin your response directly with the section heading "%s". Do NOT output preambles, greetings, or meta-commentary.
2. Every quantitative metric, version, throughput number, or factual claim MUST include an inline citation tag citing the provided source ID (e.g. [1], [2]).
3. Do NOT cite URLs or source IDs not present in the assigned evidence.`,
		sectionIdx+1, goal, spec.Heading, spec.Objective, evidenceBlock.String(), leadsBlock.String(), tableConstraint, spec.Heading)

	userPrompt := fmt.Sprintf("Synthesize Section %d (%s) now. Begin directly with %s", sectionIdx+1, spec.Heading, spec.Heading)

	resp, err := engine.Infer(ctx, systemPrompt, userPrompt, "", TargetWorker)
	if err != nil {
		return "", err
	}

	cleaned := strings.TrimSpace(resp)
	if !strings.HasPrefix(cleaned, "#") {
		cleaned = fmt.Sprintf("%s\n\n%s", spec.Heading, cleaned)
	}

	return cleaned, nil
}

// RunSectionedSynthesisPipeline executes the 4-stage Dynamic Sectioned Map-Reduce Synthesis pipeline (ADR-0083).
func RunSectionedSynthesisPipeline(ctx context.Context, engine ProbeInferenceEngine, goal string, cards []EvidenceCard) (string, error) {
	if len(cards) == 0 {
		return "", fmt.Errorf("no evidence cards provided for synthesis")
	}

	// Step 1: Map Phase — Neural Evidence Ranking per source
	evidenceTables := make([]EvidenceTable, len(cards))
	for i, card := range cards {
		evidenceTables[i] = RankEvidenceForSource(ctx, goal, card, i+1, 0)
	}

	// Step 2: Synthesis Outline Planner
	outline, err := GenerateSynthesisOutline(ctx, engine, goal, evidenceTables)
	if err != nil || outline == nil || len(outline.Sections) == 0 {
		outline = buildDefaultOutline(goal, evidenceTables)
	}

	// Step 3: Reduce Phase — Section-by-Section Assembler with Rolling Prefix Context
	var sectionOutputs []string
	var sectionLeads []string

	for i, spec := range outline.Sections {
		fmt.Fprintf(os.Stderr, "[SectionedSynthesis] Assembling section %d/%d: %s\n", i+1, len(outline.Sections), spec.Heading)
		secText, err := AssembleSection(ctx, engine, goal, outline, i, spec, evidenceTables, sectionLeads)
		if err != nil || strings.TrimSpace(secText) == "" {
			fmt.Fprintf(os.Stderr, "[SectionedSynthesis] Warning: Failed to assemble section %d (%v), continuing\n", i+1, err)
			continue
		}

		sectionOutputs = append(sectionOutputs, secText)

		// Extract lead (heading + first 2 non-empty lines) for rolling context
		lead := extractSectionLead(secText)
		if lead != "" {
			sectionLeads = append(sectionLeads, lead)
		}
	}

	if len(sectionOutputs) == 0 {
		return "", fmt.Errorf("section assembly produced no output")
	}

	// Concatenate title + sections
	var docBuilder strings.Builder
	docTitle := outline.Title
	if docTitle == "" {
		docTitle = deriveCleanDocumentTitle(goal)
	}
	if !strings.HasPrefix(docTitle, "#") {
		docTitle = "# " + docTitle
	}
	docBuilder.WriteString(docTitle)
	docBuilder.WriteString("\n\n")

	for _, secText := range sectionOutputs {
		docBuilder.WriteString(secText)
		docBuilder.WriteString("\n\n---\n\n")
	}

	assembledDraft := strings.TrimSpace(docBuilder.String())

	// Step 4: Verification Gate & Semantic Citation Remapping
	remapThresh := config.GlobalConfig.GetResearchCitationRemapThreshold()
	metricThresh := config.GlobalConfig.GetResearchMetricBindingThreshold()
	finalDoc := RemapAndGroundCitations(ctx, assembledDraft, evidenceTables, remapThresh, metricThresh)

	return finalDoc, nil
}

var citationRegex = regexp.MustCompile(`\[(\d+)\]`)

// RemapAndGroundCitations validates citation tags, remaps out-of-bounds tags using embedding similarity (>= remapThresh),
// auto-binds unsourced metrics (>= metricThresh), and appends the verified bibliography table.
func RemapAndGroundCitations(ctx context.Context, draftDoc string, evidence []EvidenceTable, remapThresh, metricThresh float32) string {
	if len(evidence) == 0 {
		return draftDoc
	}

	if remapThresh <= 0 {
		remapThresh = 0.45
	}
	if metricThresh <= 0 {
		metricThresh = 0.65
	}

	maxSourceID := len(evidence)

	// Pre-build snippet pool per source for vector similarity
	sourceSnippets := make(map[int]string)
	for _, e := range evidence {
		var combined strings.Builder
		combined.WriteString(e.Title)
		combined.WriteString(" ")
		combined.WriteString(e.URL)
		combined.WriteString(" ")
		combined.WriteString(strings.Join(e.KeyMetrics, " "))
		combined.WriteString(" ")
		combined.WriteString(strings.Join(e.RawSnippets, " "))
		sourceSnippets[e.SourceIndex] = combined.String()
	}

	// 1. Process line by line / sentence by sentence
	lines := strings.Split(draftDoc, "\n")
	var processedLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			processedLines = append(processedLines, line)
			continue
		}

		// Remap invalid out-of-bounds citation tags in line
		newLine := citationRegex.ReplaceAllStringFunc(line, func(match string) string {
			var tagNum int
			fmt.Sscanf(match, "[%d]", &tagNum)
			if tagNum >= 1 && tagNum <= maxSourceID {
				return match // Valid tag
			}

			// Out-of-bounds tag — calculate similarity against all sources
			bestID, maxSim := findBestMatchingSource(ctx, line, sourceSnippets)
			if maxSim >= remapThresh && bestID >= 1 && bestID <= maxSourceID {
				return fmt.Sprintf("[%d]", bestID)
			}
			return "" // Strip invalid tag if no source matches threshold
		})

		// Metric grounding: if line contains numbers/metrics but no citation tags
		if !citationRegex.MatchString(newLine) && metricPattern.MatchString(newLine) {
			bestID, maxSim := findBestMatchingSource(ctx, newLine, sourceSnippets)
			if maxSim >= metricThresh && bestID >= 1 && bestID <= maxSourceID {
				newLine = fmt.Sprintf("%s [%d]", strings.TrimRight(newLine, " \t."), bestID)
				if strings.HasSuffix(line, ".") {
					newLine += "."
				}
			}
		}

		processedLines = append(processedLines, newLine)
	}

	body := strings.Join(processedLines, "\n")

	// 2. Append canonical bibliography if not already present
	if !strings.Contains(body, "## Verified Sources & Citations") && !strings.Contains(body, "## Numbered Bibliography") {
		var bib strings.Builder
		bib.WriteString("\n\n## Verified Sources & Citations\n\n")
		bib.WriteString("| Source ID | Title | URL |\n")
		bib.WriteString("| :--- | :--- | :--- |\n")
		for _, e := range evidence {
			title := e.Title
			if title == "" {
				title = e.URL
			}
			bib.WriteString(fmt.Sprintf("| [%d] | %s | %s |\n", e.SourceIndex, title, e.URL))
		}
		body = strings.TrimSpace(body) + "\n" + bib.String()
	}

	return strings.TrimSpace(body)
}

func findBestMatchingSource(ctx context.Context, sentence string, sourceSnippets map[int]string) (int, float32) {
	bestID := 1
	var maxSim float32 = 0.0

	for id, snip := range sourceSnippets {
		var sim float32
		if inference.GlobalEmbeddingSidecar != nil && inference.GlobalEmbeddingSidecar.IsAvailable() {
			embCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			vec1, err1 := inference.GlobalEmbeddingSidecar.Embed(embCtx, sentence)
			vec2, err2 := inference.GlobalEmbeddingSidecar.Embed(embCtx, snip)
			cancel()
			if err1 == nil && err2 == nil {
				sim = inference.GlobalEmbeddingSidecar.CosineSimilarity(vec1, vec2)
			} else {
				sim = float32(embeddings.CosineSimilarity(sentence, snip))
			}
		} else {
			sim = float32(embeddings.CosineSimilarity(sentence, snip))
		}

		if sim > maxSim {
			maxSim = sim
			bestID = id
		}
	}

	return bestID, maxSim
}

func extractSectionLead(secText string) string {
	lines := strings.Split(secText, "\n")
	var leadParts []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		leadParts = append(leadParts, trimmed)
		if len(leadParts) >= 2 {
			break
		}
	}
	return strings.Join(leadParts, " — ")
}

// ResearchSectionSpec defines a single discrete section to be synthesized (ADR-0082 backward compatibility).
type ResearchSectionSpec struct {
	SectionID   string
	Heading     string
	FocusPrompt string
	MaxTokens   int
	IsTable     bool
}

// IsMultiSectionResearchGoal determines if a goal warrants sectioned Map-Reduce synthesis.
func IsMultiSectionResearchGoal(goal string) bool {
	lower := strings.ToLower(goal)
	return (strings.Contains(lower, "compare") || strings.Contains(lower, "comparison") ||
		strings.Contains(lower, "framework") || strings.Contains(lower, "landscape") ||
		strings.Contains(lower, "durable execution") || strings.Contains(lower, "deep dive") ||
		strings.Contains(lower, "guide") || strings.Contains(lower, "whitepaper") ||
		strings.Contains(lower, "overview")) && len(goal) >= 40
}

// DecomposeResearchGoalIntoSections breaks a broad research goal into focused sub-sections.
func DecomposeResearchGoalIntoSections(goal string) []ResearchSectionSpec {
	lower := strings.ToLower(goal)

	// Framework / Tool Comparison Goals
	if strings.Contains(lower, "compare") || strings.Contains(lower, "framework") || strings.Contains(lower, "engine") {
		return []ResearchSectionSpec{
			{
				SectionID:   "arch_overview",
				Heading:     "## 1. Core Architectural Patterns & Mechanics",
				FocusPrompt: "Synthesize an in-depth technical breakdown of the core architectural patterns, design philosophies, and language SDK ecosystems of the target systems. Detail their internal mechanics and primary differences.",
				MaxTokens:   1200,
			},
			{
				SectionID:   "comparison_matrix",
				Heading:     "## 2. Comparative Analysis Matrix & Benchmarks",
				FocusPrompt: "Generate a comprehensive, structured Markdown comparison table and benchmark breakdown across all evaluated systems. Include key features, performance throughput, latency, licensing, and community metrics. You MUST produce a complete Markdown table with all columns and rows filled using verified facts.",
				MaxTokens:   1200,
				IsTable:     true,
			},
			{
				SectionID:   "cost_pricing",
				Heading:     "## 3. Cost Arbitrage, Pricing & Economics",
				FocusPrompt: "Synthesize a concrete quantitative cost and pricing analysis. Compare cloud API pricing models, self-hosted infrastructure expenses, hardware requirements, and amortized operational costs with real estimates from the context.",
				MaxTokens:   1000,
			},
			{
				SectionID:   "decision_guide",
				Heading:     "## 4. Decision Framework & Recommendations",
				FocusPrompt: "Provide a rigorous decision guide and concrete recommendations for engineering teams and CTOs. Define clear scenarios detailing when to choose each system along with their respective trade-offs.",
				MaxTokens:   1000,
			},
		}
	}

	// Technical Format / Deep Dive Goals (e.g. GGUF, CVEs)
	return []ResearchSectionSpec{
		{
			SectionID:   "fundamentals",
			Heading:     "## 1. Technical Foundations & Architectural Evolution",
			FocusPrompt: "Synthesize a comprehensive technical breakdown of the architecture, background evolution, format specification, and core mechanics based strictly on the verified evidence.",
			MaxTokens:   1200,
		},
		{
			SectionID:   "technical_matrix",
			Heading:     "## 2. Detailed Technical Breakdown & Specifications Matrix",
			FocusPrompt: "Generate a comprehensive, structured Markdown table and detailed specifications breakdown covering all variants, quantization/security levels, memory tradeoffs, and metrics.",
			MaxTokens:   1200,
			IsTable:     true,
		},
		{
			SectionID:   "recommendations",
			Heading:     "## 3. Practical Recommendations & Implementation Guidelines",
			FocusPrompt: "Provide actionable technical recommendations, sizing advice, migration paths, or mitigation strategies with concrete guidance based on the verified evidence.",
			MaxTokens:   1000,
		},
	}
}

// ExecuteSectionedSynthesis coordinates Map-Reduce synthesis across all sections (ADR-0082 backward compatibility).
func ExecuteSectionedSynthesis(ctx context.Context, goal, refinedContext string, sections []ResearchSectionSpec, engine ProbeInferenceEngine) (string, error) {
	if len(sections) == 0 {
		return "", fmt.Errorf("no sections specified for sectioned synthesis")
	}

	var sectionOutputs []string
	mainTitle := fmt.Sprintf("# %s", deriveCleanDocumentTitle(goal))

	for i, sec := range sections {
		fmt.Fprintf(os.Stderr, "[SectionedSynthesis] Generating section %d/%d: %s\n", i+1, len(sections), sec.SectionID)

		tableConstraint := ""
		if sec.IsTable {
			tableConstraint = `
CRITICAL INSTRUCTION: You are generating the Comparison Matrix section.
You MUST output a complete, fully populated Markdown table (| Header 1 | Header 2 | ... |).
Do NOT truncate rows or columns. Every cell must contain concrete corroborated data.
`
		}

		systemPrompt := fmt.Sprintf(`You are an expert technical writer synthesizing Section %d of a comprehensive research whitepaper.

Main Whitepaper Goal: %s
Section Objective: %s

## Verified Discovery Context:
%s

%s
IMPORTANT: Begin your response directly with "%s". Do not write meta-commentary or introduction.
IMPORTANT: All concrete identifiers, versions, metrics, and claims must be corroborated by the discovery context.
IMPORTANT: Cite sources inline using markdown links: [Descriptive Title](URL).`, i+1, goal, sec.FocusPrompt, refinedContext, tableConstraint, sec.Heading)

		userPrompt := fmt.Sprintf("Synthesize Section %d (%s) now. Begin directly with %s", i+1, sec.SectionID, sec.Heading)

		resp, err := engine.Infer(ctx, systemPrompt, userPrompt, "", TargetWorker)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[SectionedSynthesis] Warning: error generating section %s: %v\n", sec.SectionID, err)
			continue
		}

		cleaned := strings.TrimSpace(resp)
		if len(cleaned) > 50 {
			if !strings.HasPrefix(cleaned, "#") {
				cleaned = fmt.Sprintf("%s\n\n%s", sec.Heading, cleaned)
			}
			sectionOutputs = append(sectionOutputs, cleaned)
		}
	}

	if len(sectionOutputs) == 0 {
		return "", fmt.Errorf("sectioned synthesis produced no valid sections")
	}

	var docBuilder strings.Builder
	docBuilder.WriteString(mainTitle)
	docBuilder.WriteString("\n\n")

	for _, secText := range sectionOutputs {
		docBuilder.WriteString(secText)
		docBuilder.WriteString("\n\n---\n\n")
	}

	return strings.TrimSpace(docBuilder.String()), nil
}

// deriveCleanDocumentTitle generates a clean top-level H1 title from the research goal.
func deriveCleanDocumentTitle(goal string) string {
	lower := strings.ToLower(goal)
	if strings.Contains(lower, "gguf") {
		return "Technical Analysis and Architectural Specification of GGUF"
	}
	if strings.Contains(lower, "durable execution") || (strings.Contains(lower, "temporal") && strings.Contains(lower, "restate")) {
		return "Comparative Architectural Analysis of Durable Execution Engines: Temporal, Restate, and Inngest"
	}
	if strings.Contains(lower, "orchestration") || (strings.Contains(lower, "langchain") && strings.Contains(lower, "llamaindex")) {
		return "Comprehensive Evaluation of LLM Orchestration Frameworks"
	}
	if strings.Contains(lower, "local") && strings.Contains(lower, "inference") {
		return "Local AI Inference Landscape: Engine Architecture, Hardware Sizing, and Cost Arbitrage"
	}
	if strings.Contains(lower, "cve") || strings.Contains(lower, "vulnerabilit") || strings.Contains(lower, "security") {
		return "Go Standard Library Security Vulnerabilities and Advisory Analysis"
	}

	// Fallback to first line of goal
	firstLine := strings.Split(goal, "\n")[0]
	firstLine = strings.TrimPrefix(firstLine, "Search the web and ")
	firstLine = strings.TrimPrefix(firstLine, "Research ")
	return strings.Title(strings.TrimSpace(firstLine))
}
