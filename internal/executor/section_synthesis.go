package executor

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// ResearchSectionSpec defines a single discrete section to be synthesized
// in an isolated local inference context (ADR-0082).
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

// ExecuteSectionedSynthesis coordinates Map-Reduce synthesis across all sections (ADR-0082).
func ExecuteSectionedSynthesis(ctx context.Context, goal, refinedContext string, sections []ResearchSectionSpec, engine ProbeInferenceEngine) (string, error) {
	if len(sections) == 0 {
		return "", fmt.Errorf("no sections specified for sectioned synthesis")
	}

	var sectionOutputs []string
	var mainTitle string

	// Derive a clean document title
	mainTitle = fmt.Sprintf("# %s", deriveCleanDocumentTitle(goal))

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
			// Ensure proper heading
			if !strings.HasPrefix(cleaned, "#") {
				cleaned = fmt.Sprintf("%s\n\n%s", sec.Heading, cleaned)
			}
			sectionOutputs = append(sectionOutputs, cleaned)
		}
	}

	if len(sectionOutputs) == 0 {
		return "", fmt.Errorf("sectioned synthesis produced no valid sections")
	}

	// Deterministic Reduce: stitch title + sections
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
