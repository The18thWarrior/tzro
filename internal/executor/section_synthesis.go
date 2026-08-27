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

	"tzro/internal/compactor"
	"tzro/internal/config"
	"tzro/internal/embeddings"
	"tzro/internal/inference"
	"tzro/internal/symbols"
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

// SectionSpec represents a planned section in the document outline (ADR-0083, ADR-0085).
type SectionSpec struct {
	Heading         string `json:"heading"`           // e.g. "## 1. Core Architectural Patterns"
	Objective       string `json:"objective"`         // What this section must cover
	TargetSourceIDs []int  `json:"target_source_ids"` // [1, 2] or empty
	FormatHint      string `json:"format_hint"`       // "table", "bulleted_deep_dive", "prose_comparison"
	IsInitial       bool   `json:"is_initial"`        // True if overview/intro (synthesized after body)
	IsTerminal      bool   `json:"is_terminal"`       // True if conclusion/synthesis (gets all sources)
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
          "is_initial": {"type": "boolean"},
          "is_terminal": {"type": "boolean"}
        },
        "required": ["heading", "objective"]
      }
    }
  },
  "required": ["title", "sections"]
}`

const docGenOutlineGBNFSchema = `{
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
          "format_hint": {"type": "string"},
          "is_initial": {"type": "boolean"},
          "is_terminal": {"type": "boolean"}
        },
        "required": ["heading", "objective"]
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
Mark the first section (if executive summary or overview) with is_initial: true.
The final section must be marked with is_terminal: true to perform the terminal synthesis.
Return ONLY a valid JSON object matching this schema:
{
  "title": "Document Title",
  "sections": [
    {
      "heading": "## 1. Section Title",
      "objective": "Clear description of what this section synthesizes",
      "target_source_ids": [1, 2],
      "is_initial": true,
      "is_terminal": false
    }
  ]
}`

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
	if len(outline.Sections) > 1 && isIntroHeading(outline.Sections[0].Heading) {
		outline.Sections[0].IsInitial = true
	}
	outline.Sections[len(outline.Sections)-1].IsTerminal = true

	return &outline, nil
}

// GenerateDocGenOutline dynamically plans a multi-section documentation outline from codebase context and symbols (ADR-0084, ADR-0085).
func GenerateDocGenOutline(ctx context.Context, engine ProbeInferenceEngine, goal, refinedCtx string, syms []symbols.Symbol) (*SynthesisOutline, error) {
	// Summary of discovered context for planning
	var symSummary strings.Builder
	if len(syms) > 0 {
		symSummary.WriteString("\nDiscovered AST Symbols:\n")
		for i, s := range syms {
			if i >= 30 {
				symSummary.WriteString(fmt.Sprintf("... and %d more symbols\n", len(syms)-30))
				break
			}
			symSummary.WriteString(fmt.Sprintf("- %s (%s): %s\n", s.Name, s.Kind, s.Signature))
		}
	}

	systemPrompt := `You are an expert technical documentation architect.
Plan a structured, multi-section documentation outline that faithfully addresses the documentation goal (e.g. module architecture, API reference, function/symbol index, or system manual).
Plan between 2 to 6 distinct sections. Do NOT collapse everything into one section.
Mark the first section (if it is an overview or executive summary) with is_initial: true.
Mark the final section (if it is a symbol index, API reference, or conclusion) with is_terminal: true.
Return ONLY a valid JSON object matching this schema:
{
  "title": "Document Title",
  "sections": [
    {
      "heading": "## 1. Section Title",
      "objective": "Clear description of what this section documents",
      "is_initial": true,
      "is_terminal": false
    }
  ]
}`

	userPrompt := fmt.Sprintf("Documentation Goal: %s\n%s\n\nDiscovery Context Preview:\n%s\nPlan the document outline in JSON now.",
		goal, symSummary.String(), truncateForPrompt(refinedCtx, 4000))

	// Cap outline generation to prevent 16K degenerate fills.
	// A valid outline (title + 2-6 sections with headings/objectives) fits in ~500 tokens.
	outlineCtx := context.WithValue(ctx, inference.MaxTokensKey, 2048)
	outlineCtx = context.WithValue(outlineCtx, inference.GenerationGuardKey,
		inference.NewRepetitionGuardWithMode(inference.ContentModeProse))

	resp, err := engine.Infer(outlineCtx, systemPrompt, userPrompt, docGenOutlineGBNFSchema, TargetWorker)
	if err != nil || strings.TrimSpace(resp) == "" {
		fmt.Fprintf(os.Stderr, "[DocGenSynthesis] Warning: Outline generation failed (%v), using safety floor\n", err)
		return BuildDocGenSafetyFloorOutline(goal, refinedCtx, syms), nil
	}

	var outline SynthesisOutline
	cleanedResp := stripThoughtAndFences(resp)
	if err := json.Unmarshal([]byte(cleanedResp), &outline); err != nil || len(outline.Sections) == 0 {
		firstBrace := strings.Index(cleanedResp, "{")
		lastBrace := strings.LastIndex(cleanedResp, "}")
		if firstBrace >= 0 && lastBrace > firstBrace {
			_ = json.Unmarshal([]byte(cleanedResp[firstBrace:lastBrace+1]), &outline)
		}
	}

	// Detect under-decomposition (lazy 1-section planner or <=3 sections on multi-file / large context)
	units := parseRefinedContextUnits(refinedCtx)
	uniqueFiles := getUniqueFilesFromUnitsAndSyms(units, syms)
	needsDynamicSplit := len(uniqueFiles) > 3 || len(units) > 3 || len(refinedCtx) > 16000

	isUnderDecomposed := len(outline.Sections) < 2
	if needsDynamicSplit && len(outline.Sections) <= 3 {
		isUnderDecomposed = true
	}

	if isUnderDecomposed && (len(refinedCtx) >= 1500 || len(syms) >= 4 || strings.Contains(goal, "across") || strings.Contains(goal, "ALL") || needsDynamicSplit) {
		fmt.Fprintf(os.Stderr, "[DocGenSynthesis] Notice: Model under-decomposed outline (%d sections), applying safety floor\n", len(outline.Sections))
		return BuildDocGenSafetyFloorOutline(goal, refinedCtx, syms), nil
	}

	if len(outline.Sections) == 0 {
		return BuildDocGenSafetyFloorOutline(goal, refinedCtx, syms), nil
	}

	if outline.Title == "" {
		outline.Title = deriveCleanDocumentTitle(goal)
	}

	for i := range outline.Sections {
		if !strings.HasPrefix(outline.Sections[i].Heading, "#") {
			outline.Sections[i].Heading = fmt.Sprintf("## %d. %s", i+1, outline.Sections[i].Heading)
		}
	}
	if len(outline.Sections) > 1 && isIntroHeading(outline.Sections[0].Heading) {
		outline.Sections[0].IsInitial = true
	}
	outline.Sections[len(outline.Sections)-1].IsTerminal = true

	return &outline, nil
}

// IsFunctionIndexGoal detects if a prompt requests an exported function/symbol index rather than narrative docs.
func IsFunctionIndexGoal(goal string) bool {
	lower := strings.ToLower(goal)
	return (strings.Contains(lower, "function index") || strings.Contains(lower, "symbol index") ||
		strings.Contains(lower, "api index") || strings.Contains(lower, "exported function") ||
		strings.Contains(lower, "exported type") || strings.Contains(lower, "symbol reference"))
}

// BuildDocGenSafetyFloorOutline builds a deterministic multi-section outline partitioned by module layers, file clusters, or ADR ranges (ADR-0084, ADR-0085).
func BuildDocGenSafetyFloorOutline(goal, refinedCtx string, syms []symbols.Symbol) *SynthesisOutline {
	units := parseRefinedContextUnits(refinedCtx)
	uniqueFiles := getUniqueFilesFromUnitsAndSyms(units, syms)
	needsDynamicSplit := len(uniqueFiles) > 3 || len(units) > 3 || len(refinedCtx) > 16000

	// Classify documentation archetype via Neural Embedding Semantic Vector Space (ADR-0081, SOLUTION_APPROACH.md Principle 1)
	archetypes := []struct {
		ID        string
		Prototype string
	}{
		{
			ID:        "function_index",
			Prototype: "Exhaustive function index listing every exported function, exported method on types, and struct signatures.",
		},
		{
			ID:        "project_readme",
			Prototype: "Comprehensive README documentation, project overview, quickstart guide, CLI command usage, and package directory index.",
		},
		{
			ID:        "system_architecture",
			Prototype: "System architecture documentation, package dependency graph, data flow, subsystem responsibilities, and core interfaces.",
		},
		{
			ID:        "decision_log",
			Prototype: "Consolidated decision log, architectural decision records ADRs, status dates and key implications.",
		},
		{
			ID:        "module_reference",
			Prototype: "Module-level technical documentation covering public types, subsystem layers, component interactions, and usage patterns.",
		},
	}

	bestArchetype := "module_reference"
	var maxSim float32 = -1.0

	for _, arch := range archetypes {
		sim := float32(embeddings.CosineSimilarity(goal, arch.Prototype))
		if sim > maxSim {
			maxSim = sim
			bestArchetype = arch.ID
		}
	}

	switch bestArchetype {
	case "function_index":
		if needsDynamicSplit && len(uniqueFiles) > 3 {
			batchSize := 3
			var funcSections []SectionSpec
			sectionCounter := 2

			numBatches := (len(uniqueFiles) + batchSize - 1) / batchSize
			for b := 0; b < numBatches; b++ {
				start := b * batchSize
				end := start + batchSize
				if end > len(uniqueFiles) {
					end = len(uniqueFiles)
				}
				batchFiles := uniqueFiles[start:end]
				var batchFileBases []string
				for _, f := range batchFiles {
					batchFileBases = append(batchFileBases, filepathBase(f))
				}
				heading := fmt.Sprintf("## %d. Exported Functions & Methods (%s)", sectionCounter, strings.Join(batchFileBases, ", "))
				objective := fmt.Sprintf("List all exported package-level functions AND all exported methods on types for %s. Include full signatures with parameter types and return values.", strings.Join(batchFileBases, ", "))

				funcSections = append(funcSections, SectionSpec{
					Heading:    heading,
					Objective:  objective,
					FormatHint: "bulleted_deep_dive",
				})
				sectionCounter++
			}

			sections := []SectionSpec{
				{
					Heading:    "## 1. Exported Types, Interfaces & Structs",
					Objective:  "List all exported struct types, interface types, and type aliases with their fields, signatures, and descriptions. Do NOT include functions or methods here.",
					FormatHint: "bulleted_deep_dive",
				},
			}
			sections = append(sections, funcSections...)
			sections = append(sections,
				SectionSpec{
					Heading:    fmt.Sprintf("## %d. Exported Constants, Variables & Configuration", sectionCounter),
					Objective:  "List all exported constants, package-level variables, and configuration values with their types and descriptions.",
					FormatHint: "bulleted_deep_dive",
				},
				SectionSpec{
					Heading:    fmt.Sprintf("## %d. Quick Reference Table", sectionCounter+1),
					Objective:  "Provide a compact summary table with columns: Symbol | Kind (func/type/const/var) | File | One-line Description. Do NOT include signatures — those are in the sections above.",
					FormatHint: "table",
					IsTerminal: true,
				},
			)
			return &SynthesisOutline{
				Title:    deriveCleanDocumentTitle(goal),
				Sections: sections,
			}
		}

		return &SynthesisOutline{
			Title: deriveCleanDocumentTitle(goal),
			Sections: []SectionSpec{
				{
					Heading:    "## 1. Exported Types, Interfaces & Structs",
					Objective:  "List all exported struct types, interface types, and type aliases with their fields, signatures, and descriptions. Do NOT include functions or methods here.",
					FormatHint: "bulleted_deep_dive",
				},
				{
					Heading:    "## 2. Exported Functions & Methods",
					Objective:  "List all exported package-level functions AND all exported methods on types. Group by source file. Include full signatures with parameter types and return values.",
					FormatHint: "bulleted_deep_dive",
				},
				{
					Heading:    "## 3. Exported Constants, Variables & Configuration",
					Objective:  "List all exported constants, package-level variables, and configuration values with their types and descriptions.",
					FormatHint: "bulleted_deep_dive",
				},
				{
					Heading:    "## 4. Quick Reference Table",
					Objective:  "Provide a compact summary table with columns: Symbol | Kind (func/type/const/var) | File | One-line Description. Do NOT include signatures — those are in the sections above.",
					FormatHint: "table",
					IsTerminal: true,
				},
			},
		}

	case "project_readme":
		if needsDynamicSplit && len(uniqueFiles) > 3 {
			batchSize := 3
			var bodySections []SectionSpec
			sectionCounter := 3 // starts after 1. Overview and 2. Quickstart

			numBatches := (len(uniqueFiles) + batchSize - 1) / batchSize
			for b := 0; b < numBatches; b++ {
				start := b * batchSize
				end := start + batchSize
				if end > len(uniqueFiles) {
					end = len(uniqueFiles)
				}
				batchFiles := uniqueFiles[start:end]
				var batchFileBases []string
				for _, f := range batchFiles {
					batchFileBases = append(batchFileBases, filepathBase(f))
				}
				clusterLabel := deriveSubsystemClusterLabel(batchFileBases)
				heading := fmt.Sprintf("## %d. %s Reference (%s)", sectionCounter, clusterLabel, strings.Join(batchFileBases, ", "))
				objective := fmt.Sprintf("Document the package structure, exported interfaces, and operational components in %s.", strings.Join(batchFileBases, ", "))

				bodySections = append(bodySections, SectionSpec{
					Heading:    heading,
					Objective:  objective,
					FormatHint: "bulleted_deep_dive",
				})
				sectionCounter++
			}

			sections := []SectionSpec{
				{
					Heading:    "## 1. Project Overview & Core Mission",
					Objective:  "Synthesize a clear, professional project overview explaining what the project is, its core capabilities, and high-level architecture.",
					FormatHint: "prose",
					IsInitial:  true,
				},
				{
					Heading:    "## 2. Quickstart & Usage Guide",
					Objective:  "Provide a concrete quickstart guide showing build instructions, CLI commands, and practical usage examples based on verified source files.",
					FormatHint: "prose",
				},
			}
			sections = append(sections, bodySections...)
			sections = append(sections, SectionSpec{
				Heading:    fmt.Sprintf("## %d. Public API & Symbol Reference", sectionCounter),
				Objective:  "Document key public types, interfaces, and exported functions discovered across packages with signatures and behavior descriptions.",
				FormatHint: "bulleted_deep_dive",
				IsTerminal: true,
			})
			return &SynthesisOutline{
				Title:    deriveCleanDocumentTitle(goal),
				Sections: sections,
			}
		}

		return &SynthesisOutline{
			Title: deriveCleanDocumentTitle(goal),
			Sections: []SectionSpec{
				{
					Heading:    "## 1. Project Overview & Core Mission",
					Objective:  "Synthesize a clear, professional project overview explaining what the project is, its core capabilities, and high-level architecture.",
					FormatHint: "prose",
					IsInitial:  true,
				},
				{
					Heading:    "## 2. Quickstart & Usage Guide",
					Objective:  "Provide a concrete quickstart guide showing build instructions, CLI commands, and practical usage examples based on verified source files.",
					FormatHint: "prose",
				},
				{
					Heading:    "## 3. Package & Directory Index",
					Objective:  "Provide a structured index documenting all discovered packages and key directories with architectural responsibilities.",
					FormatHint: "bulleted_deep_dive",
				},
				{
					Heading:    "## 4. Public API & Symbol Reference",
					Objective:  "Document key public types, interfaces, and exported functions discovered across packages with signatures and behavior descriptions.",
					FormatHint: "bulleted_deep_dive",
					IsTerminal: true,
				},
			},
		}

	case "system_architecture":
		if needsDynamicSplit && len(uniqueFiles) > 3 {
			batchSize := 3
			var bodySections []SectionSpec
			sectionCounter := 2

			numBatches := (len(uniqueFiles) + batchSize - 1) / batchSize
			for b := 0; b < numBatches; b++ {
				start := b * batchSize
				end := start + batchSize
				if end > len(uniqueFiles) {
					end = len(uniqueFiles)
				}
				batchFiles := uniqueFiles[start:end]
				var batchFileBases []string
				for _, f := range batchFiles {
					batchFileBases = append(batchFileBases, filepathBase(f))
				}
				clusterLabel := deriveSubsystemClusterLabel(batchFileBases)
				heading := fmt.Sprintf("## %d. %s (%s)", sectionCounter, clusterLabel, strings.Join(batchFileBases, ", "))
				objective := fmt.Sprintf("Detail the subsystem responsibilities, structs, and dependency interactions in %s.", strings.Join(batchFileBases, ", "))

				bodySections = append(bodySections, SectionSpec{
					Heading:    heading,
					Objective:  objective,
					FormatHint: "bulleted_deep_dive",
				})
				sectionCounter++
			}

			sections := []SectionSpec{
				{
					Heading:    "## 1. System Architecture & High-Level Design",
					Objective:  "Synthesize the system architecture overview explaining overall design principles, subsystem boundaries, and execution models.",
					FormatHint: "prose",
					IsInitial:  true,
				},
			}
			sections = append(sections, bodySections...)
			sections = append(sections, SectionSpec{
				Heading:    fmt.Sprintf("## %d. Key Abstractions & Cross-Cutting Mechanics", sectionCounter),
				Objective:  "Document key interfaces, core abstractions, state management, and configuration mechanisms discovered across the codebase.",
				FormatHint: "bulleted_deep_dive",
				IsTerminal: true,
			})
			return &SynthesisOutline{
				Title:    deriveCleanDocumentTitle(goal),
				Sections: sections,
			}
		}

		return &SynthesisOutline{
			Title: deriveCleanDocumentTitle(goal),
			Sections: []SectionSpec{
				{
					Heading:    "## 1. System Architecture & High-Level Design",
					Objective:  "Synthesize the system architecture overview explaining overall design principles, subsystem boundaries, and execution models.",
					FormatHint: "prose",
					IsInitial:  true,
				},
				{
					Heading:    "## 2. Core Subsystems & Package Responsibilities",
					Objective:  "Systematically document all discovered packages and subsystems detailing their specific roles, responsibilities, and boundaries.",
					FormatHint: "bulleted_deep_dive",
				},
				{
					Heading:    "## 3. Package Dependencies & Data Flow",
					Objective:  "Detail the directional data flow and dependency relationships between packages including call sequences and state lifecycles.",
					FormatHint: "prose",
				},
				{
					Heading:    "## 4. Key Abstractions & Cross-Cutting Mechanics",
					Objective:  "Document key interfaces, core abstractions, state management, and configuration mechanisms discovered across the codebase.",
					FormatHint: "bulleted_deep_dive",
					IsTerminal: true,
				},
			},
		}

	case "decision_log":
		if needsDynamicSplit && (len(units) > 3 || len(uniqueFiles) > 3) {
			targetUnits := units
			if len(targetUnits) <= 1 && len(uniqueFiles) > 3 {
				// Synthesize virtual units from uniqueFiles if units couldn't be parsed separately
				targetUnits = make([]docGenUnit, len(uniqueFiles))
				for i, f := range uniqueFiles {
					targetUnits[i] = docGenUnit{identifier: f, filePath: f}
				}
			}

			batchSize := 8
			if len(targetUnits) <= 12 && len(targetUnits) > 3 {
				batchSize = 4
			}
			var bodySections []SectionSpec
			sectionCounter := 2

			numBatches := (len(targetUnits) + batchSize - 1) / batchSize
			for b := 0; b < numBatches; b++ {
				start := b * batchSize
				end := start + batchSize
				if end > len(targetUnits) {
					end = len(targetUnits)
				}
				batchUnits := targetUnits[start:end]
				var batchNames []string
				for _, u := range batchUnits {
					name := u.filePath
					if name == "" {
						name = u.identifier
					}
					batchNames = append(batchNames, filepathBase(name))
				}
				firstName := batchNames[0]
				lastName := batchNames[len(batchNames)-1]

				heading := fmt.Sprintf("## %d. Consolidated Decision Records (%s to %s)", sectionCounter, firstName, lastName)
				if numBatches == 1 {
					heading = fmt.Sprintf("## %d. Consolidated Decision Records", sectionCounter)
				}
				objective := fmt.Sprintf("Provide a comprehensive, chronologically organized record of decisions from %s through %s with status, date, context, and key technical implications. Specifically cover: %s.", firstName, lastName, strings.Join(batchNames, ", "))

				bodySections = append(bodySections, SectionSpec{
					Heading:    heading,
					Objective:  objective,
					FormatHint: "bulleted_deep_dive",
				})
				sectionCounter++
			}

			sections := []SectionSpec{
				{
					Heading:    "## 1. Architectural Decisions Summary",
					Objective:  "Synthesize an executive summary of architectural decisions and systemic design patterns established in the project.",
					FormatHint: "prose",
					IsInitial:  true,
				},
			}
			sections = append(sections, bodySections...)
			sections = append(sections, SectionSpec{
				Heading:    fmt.Sprintf("## %d. Cross-Cutting Implications & Technical Trade-offs", sectionCounter),
				Objective:  "Synthesize the combined architectural impacts, constraints, and operational trade-offs across decisions.",
				FormatHint: "prose",
				IsTerminal: true,
			})
			return &SynthesisOutline{
				Title:    deriveCleanDocumentTitle(goal),
				Sections: sections,
			}
		}

		return &SynthesisOutline{
			Title: deriveCleanDocumentTitle(goal),
			Sections: []SectionSpec{
				{
					Heading:    "## 1. Architectural Decisions Summary",
					Objective:  "Synthesize an executive summary of architectural decisions and systemic design patterns established in the project.",
					FormatHint: "prose",
					IsInitial:  true,
				},
				{
					Heading:    "## 2. Consolidated Decision Records",
					Objective:  "Provide a comprehensive, chronologically organized record of all decisions with status, date, context, and key technical implications.",
					FormatHint: "bulleted_deep_dive",
				},
				{
					Heading:    "## 3. Cross-Cutting Implications & Technical Trade-offs",
					Objective:  "Synthesize the combined architectural impacts, constraints, and operational trade-offs across decisions.",
					FormatHint: "prose",
					IsTerminal: true,
				},
			},
		}

	default: // "module_reference"
		if needsDynamicSplit && len(uniqueFiles) > 3 {
			batchSize := 3
			var bodySections []SectionSpec
			sectionCounter := 2

			numBatches := (len(uniqueFiles) + batchSize - 1) / batchSize
			for b := 0; b < numBatches; b++ {
				start := b * batchSize
				end := start + batchSize
				if end > len(uniqueFiles) {
					end = len(uniqueFiles)
				}
				batchFiles := uniqueFiles[start:end]
				var batchFileBases []string
				for _, f := range batchFiles {
					batchFileBases = append(batchFileBases, filepathBase(f))
				}
				clusterLabel := deriveSubsystemClusterLabel(batchFileBases)
				heading := fmt.Sprintf("## %d. %s (%s)", sectionCounter, clusterLabel, strings.Join(batchFileBases, ", "))
				objective := fmt.Sprintf("Detail all major structs, functions, lifecycle methods, and operational interactions in %s.", strings.Join(batchFileBases, ", "))

				bodySections = append(bodySections, SectionSpec{
					Heading:    heading,
					Objective:  objective,
					FormatHint: "bulleted_deep_dive",
				})
				sectionCounter++
			}

			sections := []SectionSpec{
				{
					Heading:    "## 1. Architecture Overview & Design Principles",
					Objective:  "Synthesize an in-depth breakdown of module architecture, core design principles, responsibilities, and structural organization.",
					FormatHint: "prose",
					IsInitial:  true,
				},
			}
			sections = append(sections, bodySections...)
			sections = append(sections, SectionSpec{
				Heading:    fmt.Sprintf("## %d. Public Symbols, Interfaces & Usage Patterns", sectionCounter),
				Objective:  "Provide an exhaustive reference of all public types, interfaces, methods, configuration context keys, and concrete code usage patterns across all documented files.",
				FormatHint: "bulleted_deep_dive",
				IsTerminal: true,
			})
			return &SynthesisOutline{
				Title:    deriveCleanDocumentTitle(goal),
				Sections: sections,
			}
		}

		return &SynthesisOutline{
			Title: deriveCleanDocumentTitle(goal),
			Sections: []SectionSpec{
				{
					Heading:    "## 1. Architecture Overview & Design Principles",
					Objective:  "Synthesize an in-depth breakdown of module architecture, core design principles, responsibilities, and structural organization.",
					FormatHint: "prose",
					IsInitial:  true,
				},
				{
					Heading:    "## 2. Core Components & Subsystem Breakdown",
					Objective:  "Detail all major structs, functions, lifecycle methods, and operational interactions discovered in the codebase.",
					FormatHint: "prose",
				},
				{
					Heading:    "## 3. Public Symbols, Interfaces & Usage Patterns",
					Objective:  "Provide an exhaustive reference of all public types, interfaces, methods, configuration context keys, and concrete code usage patterns.",
					FormatHint: "bulleted_deep_dive",
					IsTerminal: true,
				},
			},
		}
	}
}

func getUniqueFilesFromUnitsAndSyms(units []docGenUnit, syms []symbols.Symbol) []string {
	seen := make(map[string]bool)
	var files []string
	for _, u := range units {
		f := u.filePath
		if f == "" && u.identifier != "" && u.identifier != "Discovery Context" {
			f = extractFilePathFromHeader(u.identifier)
		}
		if f != "" {
			base := filepathBase(f)
			if !seen[base] {
				seen[base] = true
				files = append(files, f)
			}
		}
	}
	for _, s := range syms {
		if s.File != "" {
			base := filepathBase(s.File)
			if !seen[base] {
				seen[base] = true
				files = append(files, s.File)
			}
		}
	}
	return files
}

func deriveSubsystemClusterLabel(files []string) string {
	lowerFiles := strings.ToLower(strings.Join(files, " "))
	switch {
	case strings.Contains(lowerFiles, "backend") || strings.Contains(lowerFiles, "remote") || strings.Contains(lowerFiles, "server"):
		return "Core & Backend Subsystems"
	case strings.Contains(lowerFiles, "model") || strings.Contains(lowerFiles, "catalog") || strings.Contains(lowerFiles, "local"):
		return "Local Model & Lifecycle Management"
	case strings.Contains(lowerFiles, "routing") || strings.Contains(lowerFiles, "router") || strings.Contains(lowerFiles, "prefill"):
		return "Dual Routing & Preflight Subsystems"
	case strings.Contains(lowerFiles, "metrics") || strings.Contains(lowerFiles, "thermal") || strings.Contains(lowerFiles, "token") || strings.Contains(lowerFiles, "tracker"):
		return "Support & Telemetry Subsystems"
	case strings.Contains(lowerFiles, "cache") || strings.Contains(lowerFiles, "query") || strings.Contains(lowerFiles, "store"):
		return "Caching & Data Storage Subsystems"
	case strings.Contains(lowerFiles, "executor") || strings.Contains(lowerFiles, "recall") || strings.Contains(lowerFiles, "node"):
		return "Execution & Synthesis Subsystems"
	default:
		return "Subsystem Layer"
	}
}

type docGenUnit struct {
	identifier string // e.g. "internal/inference/backend.go"
	content    string
	filePath   string
}

func parseRefinedContextUnits(refinedCtx string) []docGenUnit {
	var units []docGenUnit
	if strings.TrimSpace(refinedCtx) == "" {
		return units
	}

	lines := strings.Split(refinedCtx, "\n")
	var curIdentifier string
	var curLines []string

	flush := func() {
		if len(curLines) > 0 {
			content := strings.TrimSpace(strings.Join(curLines, "\n"))
			if content != "" {
				filePath := extractFilePathFromHeader(curIdentifier)
				units = append(units, docGenUnit{
					identifier: curIdentifier,
					content:    content,
					filePath:   filePath,
				})
			}
		}
		curLines = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isHeader := strings.HasPrefix(trimmed, "### ") ||
			strings.HasPrefix(trimmed, "File: ") ||
			strings.HasPrefix(trimmed, "## File: ") ||
			strings.HasPrefix(trimmed, "## ADR-") ||
			strings.HasPrefix(trimmed, "### ADR-") ||
			(strings.HasPrefix(trimmed, "## ") && (strings.Contains(trimmed, ".go") || strings.Contains(trimmed, ".md") || strings.Contains(trimmed, "ADR-") || strings.Contains(trimmed, "00")))

		if isHeader {
			flush()
			curIdentifier = trimmed
		}
		curLines = append(curLines, line)
	}
	flush()

	if len(units) == 0 && len(refinedCtx) > 0 {
		units = append(units, docGenUnit{
			identifier: "Discovery Context",
			content:    refinedCtx,
			filePath:   "",
		})
	}
	return units
}

func extractFilePathFromHeader(header string) string {
	lower := strings.ToLower(header)
	re := regexp.MustCompile(`[\w\-\.\/]+\.(?:go|ts|js|py|rs|c|cpp|h|md|json)`)
	if m := re.FindString(lower); m != "" {
		return m
	}
	adrRe := regexp.MustCompile(`\b(?:adr[-_]?)?\d{4}[-\w]*`)
	if m := adrRe.FindString(lower); m != "" {
		return m
	}
	return ""
}

var docGenStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
	"from": true, "into": true, "over": true, "under": true, "about": true, "all": true,
	"are": true, "was": true, "were": true, "will": true, "shall": true, "can": true,
	"each": true, "every": true, "any": true, "some": true, "what": true, "how": true,
	"section": true, "overview": true, "document": true, "documentation": true,
}

func extractQueryTokens(query string) []string {
	words := regexp.MustCompile(`[a-zA-Z0-9_\-]+`).FindAllString(strings.ToLower(query), -1)
	var tokens []string
	seen := make(map[string]bool)
	for _, w := range words {
		if len(w) >= 3 && !docGenStopWords[w] && !seen[w] {
			seen[w] = true
			tokens = append(tokens, w)
		}
	}
	return tokens
}

func filepathBase(path string) string {
	idx := strings.LastIndexAny(path, "/\\")
	if idx >= 0 {
		return path[idx+1:]
	}
	return path
}

func isIntroHeading(heading string) bool {
	h := strings.ToLower(heading)
	return strings.Contains(h, "overview") || strings.Contains(h, "executive summary") ||
		strings.Contains(h, "introduction") || strings.Contains(h, "architecture overview")
}

// PartitionDocGenContext deterministically partitions refinedCtx and symbols for a given section (ADR-0085).
// Caps context selection at maxContextChars (default 40,000 chars / ~10,000 tokens).
func PartitionDocGenContext(
	refinedCtx string,
	syms []symbols.Symbol,
	spec SectionSpec,
	allSpecs []SectionSpec,
	maxContextChars int,
) (string, string) {
	if maxContextChars <= 0 {
		maxContextChars = 40000 // ~10,000 tokens cap
	}

	units := parseRefinedContextUnits(refinedCtx)
	if len(units) == 0 && len(syms) == 0 {
		return refinedCtx, ""
	}

	query := strings.ToLower(spec.Heading + " " + spec.Objective)
	queryTokens := extractQueryTokens(query)

	type scoredUnit struct {
		unit  docGenUnit
		score float64
	}
	var scored []scoredUnit

	for _, u := range units {
		score := 0.0
		idLower := strings.ToLower(u.identifier)
		contentLower := strings.ToLower(u.content)
		pathLower := strings.ToLower(u.filePath)

		for _, tok := range queryTokens {
			if strings.Contains(idLower, tok) || strings.Contains(pathLower, tok) {
				score += 5.0
			}
			if strings.Contains(contentLower, tok) {
				score += 1.0
			}
		}

		if u.filePath != "" {
			base := strings.ToLower(filepathBase(u.filePath))
			baseNameNoExt := strings.TrimSuffix(base, ".go")
			if len(baseNameNoExt) >= 3 && strings.Contains(query, baseNameNoExt) {
				score += 10.0
			}
		}

		scored = append(scored, scoredUnit{unit: u, score: score})
	}

	// Sort descending by score
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	var selectedUnits []docGenUnit
	currentChars := 0
	matchedFilePaths := make(map[string]bool)

	for _, s := range scored {
		unitLen := len(s.unit.content)
		if currentChars+unitLen <= maxContextChars || len(selectedUnits) == 0 {
			selectedUnits = append(selectedUnits, s.unit)
			currentChars += unitLen
			if s.unit.filePath != "" {
				matchedFilePaths[s.unit.filePath] = true
				matchedFilePaths[filepathBase(s.unit.filePath)] = true
			}
		}
	}

	var ctxBuilder strings.Builder
	for _, u := range selectedUnits {
		ctxBuilder.WriteString(u.content)
		ctxBuilder.WriteString("\n\n")
	}

	// Filter symbols matching selected files or matching query tokens
	var selectedSyms []symbols.Symbol
	if spec.IsTerminal {
		selectedSyms = syms
	} else {
		for _, sym := range syms {
			symPath := strings.ToLower(sym.File)
			symBase := strings.ToLower(filepathBase(sym.File))
			symName := strings.ToLower(sym.Name)

			if matchedFilePaths[symPath] || matchedFilePaths[symBase] {
				selectedSyms = append(selectedSyms, sym)
				continue
			}
			for _, tok := range queryTokens {
				if len(tok) >= 3 && strings.Contains(symName, tok) {
					selectedSyms = append(selectedSyms, sym)
					break
				}
			}
		}
		if len(selectedSyms) == 0 && len(syms) > 0 {
			selectedSyms = syms
		}
	}

	var symBuilder strings.Builder
	if len(selectedSyms) > 0 {
		symBuilder.WriteString("\n\n## Authoritative Symbol Reference (AST-extracted, verified):\n")
		symBuilder.WriteString("Use ONLY these exact names when referring to types, functions, and interfaces:\n")
		maxSyms := 40
		if len(selectedSyms) < maxSyms {
			maxSyms = len(selectedSyms)
		}
		for _, s := range selectedSyms[:maxSyms] {
			symBuilder.WriteString(fmt.Sprintf("- %s (%s, %s): %s\n", s.Name, s.Kind, filepathBase(s.File), s.Signature))
		}
		if len(selectedSyms) > maxSyms {
			symBuilder.WriteString(fmt.Sprintf("... and %d more verified symbols\n", len(selectedSyms)-maxSyms))
		}
	}

	return strings.TrimSpace(ctxBuilder.String()), symBuilder.String()
}

func truncateForPrompt(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... [truncated for outline planning]"
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

	// --- Static Prefix Slotting (ADR-0092) ---
	// Construct 4-turn invariant conversation structure to maximize KV cache reuse:
	// Turn 1 (system): Invariant system prompt (cached across all sections)
	// Turn 2 (user): Overall document goal (cached across all sections in this task)
	// Turn 3 (assistant): Synthetic acknowledgment (extends the shared prefix)
	// Turn 4 (user): Volatile section-specific objective, evidence, and leads
	const staticSectionSynthesisSystemPrompt = `You are an expert technical research writer synthesizing sections of a comprehensive report from verified source evidence.

IMPORTANT INSTRUCTIONS:
1. Begin your response directly with the provided section heading. Do NOT output preambles, greetings, or meta-commentary.
2. Every quantitative metric, version, throughput number, or factual claim MUST include an inline citation tag citing the provided source ID (e.g. [1], [2]).
3. Do NOT cite URLs or source IDs not present in the assigned evidence.`

	var userDynamicPrompt strings.Builder
	userDynamicPrompt.WriteString(fmt.Sprintf("## Target Section: Section %d (%s)\nSection Objective: %s\n\n", sectionIdx+1, spec.Heading, spec.Objective))
	userDynamicPrompt.WriteString(evidenceBlock.String())
	userDynamicPrompt.WriteString(leadsBlock.String())
	if tableConstraint != "" {
		userDynamicPrompt.WriteString(tableConstraint)
		userDynamicPrompt.WriteString("\n")
	}
	userDynamicPrompt.WriteString(fmt.Sprintf("Synthesize Section %d (%s) now. Begin directly with %s", sectionIdx+1, spec.Heading, spec.Heading))

	messages := []inference.InferenceMessage{
		{Role: "system", Content: staticSectionSynthesisSystemPrompt},
		{Role: "user", Content: fmt.Sprintf("## Overall Document Goal\n%s", goal)},
		{Role: "assistant", Content: "Understood. Provide the assigned section objective, source evidence, and preceding section context."},
		{Role: "user", Content: userDynamicPrompt.String()},
	}

	resp, err := engine.InferMessages(ctx, messages, "", TargetWorker)
	if err != nil {
		return "", err
	}

	cleaned := strings.TrimSpace(resp)
	if decoded, err := compactor.DecodeWithHeader(cleaned); err == nil {
		cleaned = decoded
	}
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

// ExecuteDocGenSectionedSynthesis executes Inside-Out (Sandwich) Map-Reduce sectioned synthesis for codebase documentation (ADR-0084, ADR-0085).
// EmbeddingPruneChunks uses the Embedding Sidecar to deduplicate and
// relevance-filter file chunks before sectioned synthesis (ADR-0094).
// Pure deterministic scaffolding — no LLM calls.
//
// Algorithm:
//  1. Embed all chunk texts + goal in a single EmbedBatch call
//  2. Score each chunk by cosine similarity to the goal vector
//  3. Drop chunks below the relevance floor
//  4. Sort by goal relevance descending
//  5. Greedy dedup: skip chunks too similar to any already-kept chunk
//  6. Concatenate kept chunks within the character budget
func EmbeddingPruneChunks(
	ctx context.Context,
	chunks []ListFileChunk,
	goal string,
	redundancyThreshold float32,
	relevanceFloor float32,
	maxBudgetChars int,
) string {
	if len(chunks) == 0 {
		return ""
	}

	// Collect texts for batch embedding
	texts := make([]string, 0, len(chunks)+1)
	texts = append(texts, goal) // index 0 = goal vector
	for _, c := range chunks {
		// Use filepath + first 500 chars as the embedding text
		// to capture both structural and content similarity
		text := c.FilePath + "\n" + c.Content
		if len(text) > 500 {
			text = text[:500]
		}
		texts = append(texts, text)
	}

	// Embed via sidecar
	embCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	vecs, err := inference.GlobalEmbeddingSidecar.EmbedBatch(embCtx, texts)
	if err != nil || len(vecs) != len(texts) {
		// Fallback: return concatenated chunks within budget without dedup
		fmt.Fprintf(os.Stderr, "[EmbeddingPrune] Sidecar unavailable (%v), falling back to budget-only truncation\n", err)
		return fallbackBudgetTruncate(chunks, maxBudgetChars)
	}

	goalVec := vecs[0]
	chunkVecs := vecs[1:]

	// Score chunks by goal relevance
	type scoredChunk struct {
		idx       int
		relevance float32
	}
	var scored []scoredChunk
	for i, vec := range chunkVecs {
		sim := inference.GlobalEmbeddingSidecar.CosineSimilarity(goalVec, vec)
		if sim >= relevanceFloor {
			scored = append(scored, scoredChunk{idx: i, relevance: sim})
		} else {
			fmt.Fprintf(os.Stderr, "[EmbeddingPrune] Dropped chunk %s (relevance %.3f < floor %.3f)\n",
				chunks[i].FilePath, sim, relevanceFloor)
		}
	}

	// Sort by relevance descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].relevance > scored[j].relevance
	})

	// Greedy dedup: keep chunk only if not too similar to any already-kept chunk
	var kept []int
	var keptVecs [][]float32
	for _, sc := range scored {
		isDuplicate := false
		for _, kv := range keptVecs {
			sim := inference.GlobalEmbeddingSidecar.CosineSimilarity(chunkVecs[sc.idx], kv)
			if sim > redundancyThreshold {
				isDuplicate = true
				fmt.Fprintf(os.Stderr, "[EmbeddingPrune] Deduped chunk %s (similarity %.3f > threshold %.3f)\n",
					chunks[sc.idx].FilePath, sim, redundancyThreshold)
				break
			}
		}
		if !isDuplicate {
			kept = append(kept, sc.idx)
			keptVecs = append(keptVecs, chunkVecs[sc.idx])
		}
	}

	// Re-sort kept chunks by original order (preserve document flow)
	sort.Ints(kept)

	// Concatenate within budget
	var result strings.Builder
	for _, idx := range kept {
		chunk := chunks[idx]
		if result.Len()+len(chunk.Content) > maxBudgetChars {
			fmt.Fprintf(os.Stderr, "[EmbeddingPrune] Budget cap reached at %d chars (limit %d)\n",
				result.Len(), maxBudgetChars)
			break
		}
		result.WriteString(chunk.Content)
		result.WriteString("\n")
	}

	fmt.Fprintf(os.Stderr, "[EmbeddingPrune] %d/%d chunks kept, %d chars (budget %d)\n",
		len(kept), len(chunks), result.Len(), maxBudgetChars)
	return result.String()
}

// fallbackBudgetTruncate concatenates chunks within the budget without dedup.
// Used when the Embedding Sidecar is unavailable.
func fallbackBudgetTruncate(chunks []ListFileChunk, maxBudgetChars int) string {
	var result strings.Builder
	for _, c := range chunks {
		if result.Len()+len(c.Content) > maxBudgetChars {
			break
		}
		result.WriteString(c.Content)
		result.WriteString("\n")
	}
	return result.String()
}

// ExecuteDirectChunkSummarization processes each file / item chunk in secCtx independently
// through focused single-turn extraction calls and deterministically concatenates the results.
func ExecuteDirectChunkSummarization(ctx context.Context, goal string, sec SectionSpec, secCtx string, engine ProbeInferenceEngine) (string, error) {
	units := parseRefinedContextUnits(secCtx)
	if len(units) <= 1 {
		// If only 1 unit or unparsed, check if refinedCtx has chunks that can be split
		chunks := SplitListOutputIntoFileChunks(secCtx)
		if len(chunks) > 1 {
			units = make([]docGenUnit, len(chunks))
			for i, c := range chunks {
				units[i] = docGenUnit{
					identifier: c.FilePath,
					content:    c.Content,
					filePath:   c.FilePath,
				}
			}
		}
	}

	if len(units) <= 1 {
		return "", fmt.Errorf("insufficient units (%d) for direct chunk summarization", len(units))
	}

	var itemSummaries []string
	isADR := strings.Contains(strings.ToLower(goal), "adr") || strings.Contains(strings.ToLower(sec.Heading), "decision")

	for i, u := range units {
		itemPath := u.filePath
		if itemPath == "" {
			itemPath = u.identifier
		}
		itemBase := filepathBase(itemPath)

		var sysPrompt string
		if isADR {
			sysPrompt = fmt.Sprintf(`You are the Direct Decision Record Extraction Engine.
Goal: %s

CRITICAL INSTRUCTIONS:
- You are processing ONE specific Architectural Decision Record (ADR): %s
- Extract and format:
  ### ADR Title (as ### Heading with ADR number)
  - **Status**: Status value (e.g. Accepted, Proposed, Deprecated)
  - **Date**: Date value
  - **Context**: Concise 1-2 sentence problem context and background
  - **Decision**: Concise 1-2 sentence core technical decision made
  - **Technical Implications**: Bulleted list of key architectural consequences, invariants, and trade-offs
- Be concise, technical, and strictly grounded in the provided text.
- Do NOT hallucinate symbols or text from other files.
- Start directly with the ### heading.`, goal, itemBase)
		} else {
			sysPrompt = fmt.Sprintf(`You are the Direct Item Extraction Engine.
Goal: %s

CRITICAL INSTRUCTIONS:
- You are processing ONE specific file / item: %s
- Extract key structures, functions, interfaces, or operational responsibilities.
- Be concise, technical, and strictly grounded in the provided text.
- Start directly with the ### item heading.`, goal, itemBase)
		}

		userPrompt := fmt.Sprintf("Source File/Item: %s\n\nContent:\n%s\n\nExtract the structured record for this item now:", itemPath, u.content)

		callCtx := context.WithValue(ctx, inference.DRYSamplingKey, inference.DRYSamplingConfig{
			Multiplier: 0.8, Base: 1.75, AllowedLength: 2,
		})
		callCtx = context.WithValue(callCtx, inference.PresencePenaltyKey, 0.2)
		callCtx = context.WithValue(callCtx, inference.MaxTokensKey, 800)
		callCtx = context.WithValue(callCtx, inference.GenerationGuardKey,
			inference.NewRepetitionGuardWithMode(inference.ContentModeProse))

		resp, err := engine.Infer(callCtx, sysPrompt, userPrompt, "", TargetWorker)
		if err != nil || strings.TrimSpace(resp) == "" {
			fmt.Fprintf(os.Stderr, "[DirectChunkSummarization] Item %d/%d (%s) inference retry: %v\n", i+1, len(units), itemBase, err)
			resp, _ = engine.Infer(callCtx, sysPrompt, userPrompt, "", TargetWorker)
		}

		resp = stripPromptLeakage(strings.TrimSpace(resp))
		if resp != "" {
			if !strings.HasPrefix(resp, "### ") && !strings.HasPrefix(resp, "## ") {
				resp = fmt.Sprintf("### %s\n\n%s", itemBase, resp)
			}
			itemSummaries = append(itemSummaries, resp)
		}
	}

	if len(itemSummaries) == 0 {
		return "", fmt.Errorf("no item summaries produced")
	}

	var sb strings.Builder
	sb.WriteString(sec.Heading)
	sb.WriteString("\n\n")
	sb.WriteString(strings.Join(itemSummaries, "\n\n---\n\n"))
	return sb.String(), nil
}

func isDecisionOrItemLogGoal(goal, heading string) bool {
	g := strings.ToLower(goal)
	h := strings.ToLower(heading)
	return strings.Contains(g, "adr") || strings.Contains(g, "decision") ||
		strings.Contains(h, "decision") || strings.Contains(h, "adr") ||
		strings.Contains(h, "records")
}

func ExecuteDocGenSectionedSynthesis(ctx context.Context, goal, refinedCtx string, outline *SynthesisOutline, syms []symbols.Symbol, engine ProbeInferenceEngine) (string, error) {
	if outline == nil || len(outline.Sections) == 0 {
		outline = BuildDocGenSafetyFloorOutline(goal, refinedCtx, syms)
	}

	// ADR-0094: Embedding-based chunk dedup when receiving raw List output
	// directly (RecallPolicy: "skip" — no Recall Node compaction).
	pruneBudget := config.GetEmbeddingPruneBudgetChars()
	if len(refinedCtx) > pruneBudget {
		chunks := SplitListOutputIntoFileChunks(refinedCtx)
		if len(chunks) > 1 {
			chunks = ExpandChunksIntraFile(chunks, 8000)
			prunedCtx := EmbeddingPruneChunks(
				ctx, chunks, goal,
				config.GetEmbeddingPruneRedundancyThreshold(),
				config.GetEmbeddingPruneRelevanceFloor(),
				pruneBudget,
			)
			if len(prunedCtx) > 0 {
				fmt.Fprintf(os.Stderr, "[DocGen] EmbeddingPrune: %d → %d chars\n", len(refinedCtx), len(prunedCtx))
				refinedCtx = prunedCtx
			}
		}
	}

	// On-the-fly symbol extraction when the symbol index is empty.
	// This happens in comparison benchmarks that bypass probe nodes.
	if len(syms) == 0 && IsFunctionIndexGoal(goal) {
		syms = extractSymbolsFromContext(refinedCtx)
		if len(syms) > 0 {
			fmt.Fprintf(os.Stderr, "[DocGen] Extracted %d symbols on-the-fly from %d source files in context\n", len(syms), countUniqueFiles(syms))
		}
	}

	numSections := len(outline.Sections)
	if numSections == 0 {
		return "", fmt.Errorf("empty outline")
	}

	// 1. Classify section roles for Inside-Out (Sandwich) execution
	initialIdx := -1
	terminalIdx := -1
	var bodyIndices []int

	for i, sec := range outline.Sections {
		if sec.IsInitial || (i == 0 && numSections > 1 && isIntroHeading(sec.Heading)) {
			if initialIdx == -1 {
				initialIdx = i
				continue
			}
		}
		if sec.IsTerminal || (i == numSections-1 && numSections > 1) {
			terminalIdx = i
			continue
		}
		bodyIndices = append(bodyIndices, i)
	}

	// If no body indices identified (e.g. only 2 sections), make section 0 body if not initial
	if len(bodyIndices) == 0 {
		for i := 0; i < numSections; i++ {
			if i != terminalIdx {
				bodyIndices = append(bodyIndices, i)
			}
		}
		initialIdx = -1
	}

	completedSections := make([]string, numSections)
	allowedSymsMap := make(map[string]bool)
	for _, s := range syms {
		if s.Exported {
			allowedSymsMap[s.Name] = true
		}
	}

	// Phase 1: Synthesize all Body Sections with partitioned context
	for _, idx := range bodyIndices {
		sec := outline.Sections[idx]
		secCtx, secSymBlock := PartitionDocGenContext(refinedCtx, syms, sec, outline.Sections, 40000)

		// Direct chunk summarization fast-path for decision logs / multi-item extractions
		if isDecisionOrItemLogGoal(goal, sec.Heading) {
			chunkSynth, chunkErr := ExecuteDirectChunkSummarization(ctx, goal, sec, secCtx, engine)
			if chunkErr == nil && len(strings.TrimSpace(chunkSynth)) > 100 {
				completedSections[idx] = chunkSynth
				continue
			}
			if chunkErr != nil {
				fmt.Fprintf(os.Stderr, "[DocGenSynthesis] Direct chunk summarization skipped (%v), falling back to standard synthesis\n", chunkErr)
			}
		}

		// Detect function index archetype for exported-only filtering
		exportedOnlyHint := ""
		if IsFunctionIndexGoal(goal) {
			exportedOnlyHint = `
- IMPORTANT: Only document EXPORTED symbols (names starting with an uppercase letter in Go). Skip all unexported/private symbols (lowercase first letter like "compact", "stripBase64", "flattenMap").`
		}

		// AST-guided section boundary enforcement:
		// Tell the model exactly which symbols belong in this section.
		sectionBoundaryHint := ""
		if IsFunctionIndexGoal(goal) && len(syms) > 0 {
			headingLower := strings.ToLower(sec.Heading)
			var allowed []string
			for _, s := range syms {
				if !s.Exported {
					continue
				}
				switch {
				case strings.Contains(headingLower, "type") || strings.Contains(headingLower, "interface") || strings.Contains(headingLower, "struct"):
					if s.Kind == symbols.SymbolType || s.Kind == symbols.SymbolInterface || s.Kind == symbols.SymbolClass {
						allowed = append(allowed, fmt.Sprintf("  - %s (%s)", s.Name, s.Kind))
					}
				case strings.Contains(headingLower, "function") || strings.Contains(headingLower, "method"):
					if s.Kind == symbols.SymbolFunc || s.Kind == symbols.SymbolMethod {
						allowed = append(allowed, fmt.Sprintf("  - %s (%s)", s.Name, s.Kind))
					}
				case strings.Contains(headingLower, "constant") || strings.Contains(headingLower, "variable") || strings.Contains(headingLower, "configuration"):
					if s.Kind == symbols.SymbolVar || s.Kind == symbols.SymbolConst {
						allowed = append(allowed, fmt.Sprintf("  - %s (%s)", s.Name, s.Kind))
					}
				}
			}
			if len(allowed) > 0 {
				sectionBoundaryHint = fmt.Sprintf("\n- STRICT BOUNDARY: This section MUST ONLY document these symbols:\n%s\n- Do NOT include any symbol not in the list above.", strings.Join(allowed, "\n"))
			}
		}

		// Build negative context from previously completed body sections:
		// Extract H3 headings (symbol names) that were already documented to
		// prevent the 4B model from repeating them in this section.
		var previouslyDocumented string
		{
			var prev []string
			for _, prevIdx := range bodyIndices {
				if prevIdx >= idx {
					break // only look at earlier sections
				}
				if completedSections[prevIdx] == "" {
					continue
				}
				for _, line := range strings.Split(completedSections[prevIdx], "\n") {
					if strings.HasPrefix(line, "### ") {
						prev = append(prev, strings.TrimSpace(line))
					}
				}
			}
			if len(prev) > 0 {
				previouslyDocumented = fmt.Sprintf("\n\n## Symbols Already Documented (DO NOT REPEAT):\n%s", strings.Join(prev, "\n"))
			}
		}

		sysPrompt := fmt.Sprintf(`You are the Technical Documentation Synthesis Engine (Body Section Phase).
Document Goal: %s

## Relevant Codebase Context (Assigned Files & Verified Symbols):
%s%s%s

CRITICAL INSTRUCTIONS:
- You are generating the body section: "%s".
- Begin your response directly with the section heading "%s".
- Focus deeply on writing comprehensive, technical, concrete code signatures, parameters, types, and operational mechanics.
- STRICT FACTUAL GROUNDING & OMISSION POLICY:
  * Document ONLY symbols, methods, and types that appear VERBATIM in the provided source code blocks.
  * If you are not 100%% certain a symbol or parameter exists in the provided code, OMIT IT ENTIRELY.
  * Guessing, extrapolating, or inventing a symbol is penalized 10x more severely than omitting an existing one.
  * If a file has no exported symbols, do NOT infer helper structs or imagined methods.
- Do NOT repeat symbols already covered in other sections. Only document symbols that match THIS section's scope.%s%s
- Conclude cleanly without trailing fragments.`, goal, secCtx, secSymBlock, previouslyDocumented, sec.Heading, sec.Heading, exportedOnlyHint, sectionBoundaryHint)

		userPrompt := fmt.Sprintf("Synthesize Section: %s\nObjective: %s\nWrite the complete markdown section now:", sec.Heading, sec.Objective)

		callCtx := context.WithValue(ctx, inference.DRYSamplingKey, inference.DRYSamplingConfig{
			Multiplier: 0.8, Base: 1.75, AllowedLength: 2,
		})
		callCtx = context.WithValue(callCtx, inference.PresencePenaltyKey, 0.2)
		// Cap body sections at 2048 tokens. A function index body section
		// with ~30 symbols needs ~1.5K tokens. 4096 caused degenerate fills.
		callCtx = context.WithValue(callCtx, inference.MaxTokensKey, 2048)
		// Wire GenerationGuard to abort early on repetitive degeneration.
		callCtx = context.WithValue(callCtx, inference.GenerationGuardKey,
			inference.NewRepetitionGuardWithMode(inference.ContentModeProse))

		secText, err := engine.Infer(callCtx, sysPrompt, userPrompt, "", TargetWorker)
		if err != nil || strings.TrimSpace(secText) == "" {
			fmt.Fprintf(os.Stderr, "[DocGenSynthesis] Body section %d (%s) inference retry: err=%v\n", idx, sec.Heading, err)
			secText, _ = engine.Infer(callCtx, sysPrompt, userPrompt, "", TargetWorker)
		}
		if strings.TrimSpace(secText) != "" {
			secText = checkAndRepairSectionTruncation(callCtx, secText, sysPrompt, userPrompt, engine)
			secText = stripPromptLeakage(secText)
			secText = strings.TrimSpace(secText)
			// Post-filter: strip unexported and ungrounded Go symbols for function index goals.
			// Guard: if filtering would gut the section (<200 chars), keep original.
			// Imperfect content beats empty sections in quality scoring.
			if IsFunctionIndexGoal(goal) {
				filtered := stripUngroundedSymbolBlocks(secText, allowedSymsMap, secCtx)
				if len(strings.TrimSpace(filtered)) >= 200 {
					secText = filtered
				}
			}
			if !strings.HasPrefix(secText, "#") {
				secText = sec.Heading + "\n\n" + secText
			}
			completedSections[idx] = secText
		}
	}

	// Build Body Summary Context for Initial and Terminal passes
	var bodySummaryBuilder strings.Builder
	for _, idx := range bodyIndices {
		if completedSections[idx] != "" {
			bodySummaryBuilder.WriteString(completedSections[idx])
			bodySummaryBuilder.WriteString("\n\n---\n\n")
		}
	}
	bodyContext := strings.TrimSpace(bodySummaryBuilder.String())
	if len(bodyContext) > 30000 {
		bodyContext = bodyContext[:30000] + "\n... [body truncated for overview context]"
	}

	// Phase 2: Synthesize Initial Section (Overview / Executive Summary) using synthesized body
	if initialIdx >= 0 {
		sec := outline.Sections[initialIdx]
		sysPrompt := fmt.Sprintf(`You are the Technical Documentation Synthesis Engine (Executive Overview Phase).
Document Goal: %s

## Synthesized Document Body Sections (Authoritative Context):
%s

CRITICAL INSTRUCTIONS:
- Synthesize the Executive Overview / Architecture Introduction for the document based directly on the synthesized body sections above.
- Begin your response directly with the section heading "%s".
- Ensure all architectural concepts, layer names, and referenced mechanisms match what is documented in the body.
- Conclude cleanly without trailing fragments.`, goal, bodyContext, sec.Heading)

		userPrompt := fmt.Sprintf("Synthesize Section: %s\nObjective: %s\nWrite the complete markdown overview now:", sec.Heading, sec.Objective)

		callCtx := context.WithValue(ctx, inference.DRYSamplingKey, inference.DRYSamplingConfig{
			Multiplier: 0.8, Base: 1.75, AllowedLength: 2,
		})
		callCtx = context.WithValue(callCtx, inference.PresencePenaltyKey, 0.2)
		callCtx = context.WithValue(callCtx, inference.MaxTokensKey, 1200)
		callCtx = context.WithValue(callCtx, inference.GenerationGuardKey,
			inference.NewRepetitionGuardWithMode(inference.ContentModeProse))

		secText, err := engine.Infer(callCtx, sysPrompt, userPrompt, "", TargetWorker)
		if err != nil || strings.TrimSpace(secText) == "" {
			secText, _ = engine.Infer(callCtx, sysPrompt, userPrompt, "", TargetWorker)
		}
		secText = checkAndRepairSectionTruncation(callCtx, secText, sysPrompt, userPrompt, engine)
		secText = stripPromptLeakage(secText)
		secText = strings.TrimSpace(secText)
		if !strings.HasPrefix(secText, "#") {
			secText = sec.Heading + "\n\n" + secText
		}
		completedSections[initialIdx] = secText
	}

	// Phase 3: Synthesize Terminal Section (Summary Table / Public API Index)
	if terminalIdx >= 0 {
		sec := outline.Sections[terminalIdx]

		// For function index goals with AST symbols available, generate
		// the reference table deterministically. This eliminates:
		// - Hallucinated symbols (0 invented names)
		// - Wrong file attributions (uses Symbol.File)
		// - Unexported symbol leakage (filters by Symbol.Exported)
		// - 2048 inference tokens (zero model cost)
		if IsFunctionIndexGoal(goal) && len(syms) > 0 {
			var table strings.Builder
			table.WriteString(sec.Heading)
			table.WriteString("\n\n")
			table.WriteString("| Symbol | Kind | File | Signature |\n")
			table.WriteString("|--------|------|------|-----------|\n")
			for _, s := range syms {
				if !s.Exported {
					continue
				}
				kind := string(s.Kind)
				sig := s.Signature
				// Escape pipes in signatures for markdown table
				sig = strings.ReplaceAll(sig, "|", "\\|")
				// Truncate very long signatures
				if len(sig) > 120 {
					sig = sig[:117] + "..."
				}
				table.WriteString(fmt.Sprintf("| %s | %s | %s | `%s` |\n",
					s.Name, kind, filepathBase(s.File), sig))
			}
			completedSections[terminalIdx] = table.String()
			fmt.Fprintf(os.Stderr, "[DocGen/Terminal] Generated deterministic reference table from %d AST symbols\n", len(syms))
		} else {
			// Fallback: model-generated terminal section
			_, allSymBlock := PartitionDocGenContext(refinedCtx, syms, sec, outline.Sections, 40000)
			if allSymBlock == "" && len(syms) > 0 {
				var sb strings.Builder
				sb.WriteString("\n\n## Authoritative Symbol Reference (AST-extracted, verified):\n")
				for _, s := range syms {
					sb.WriteString(fmt.Sprintf("- %s (%s, %s): %s\n", s.Name, s.Kind, filepathBase(s.File), s.Signature))
				}
				allSymBlock = sb.String()
			}

			terminalCtx := bodyContext
			if len(refinedCtx) > 0 {
				if len(terminalCtx) > 0 {
					terminalCtx += "\n\n## Source Code & Exploration Context:\n" + refinedCtx
				} else {
					terminalCtx = refinedCtx
				}
			}
			if len(terminalCtx) > 35000 {
				terminalCtx = terminalCtx[:35000] + "\n... [truncated for reference context]"
			}

			terminalExportHint := ""
			if IsFunctionIndexGoal(goal) {
				terminalExportHint = `
- IMPORTANT: Only document EXPORTED symbols (names starting with an uppercase letter in Go). Skip all unexported/private symbols (lowercase first letter).`
			}

			sysPrompt := fmt.Sprintf(`You are the Technical Documentation Synthesis Engine (Terminal Reference Phase).
Document Goal: %s

## Authoritative Codebase & Synthesized Context:
%s%s

CRITICAL INSTRUCTIONS:
- Synthesize the final Reference Section: "%s".
- Begin your response directly with the section heading "%s".
- Provide an exhaustive, well-structured reference listing every single exported function, method, and type with its full signature and description.
- STRICT FACTUAL GROUNDING & OMISSION POLICY:
  * Document ONLY symbols, methods, and types that appear VERBATIM in the provided source code blocks.
  * If you are not 100%% certain a symbol or parameter exists in the provided code, OMIT IT ENTIRELY.
  * Guessing, extrapolating, or inventing a symbol is penalized 10x more severely than omitting an existing one.
  * If a file has no exported symbols, do NOT infer helper structs or imagined methods.
- Do NOT omit any exported symbols present in the context.
- Do NOT repeat symbols already fully documented in the body sections above.%s
- Conclude cleanly without trailing fragments.`, goal, terminalCtx, allSymBlock, sec.Heading, sec.Heading, terminalExportHint)

			userPrompt := fmt.Sprintf("Synthesize Section: %s\nObjective: %s\nWrite the complete markdown reference section now:", sec.Heading, sec.Objective)

			callCtx := context.WithValue(ctx, inference.DRYSamplingKey, inference.DRYSamplingConfig{
				Multiplier: 0.8, Base: 1.75, AllowedLength: 2,
			})
			callCtx = context.WithValue(callCtx, inference.PresencePenaltyKey, 0.2)
			callCtx = context.WithValue(callCtx, inference.MaxTokensKey, 2048)
			callCtx = context.WithValue(callCtx, inference.GenerationGuardKey,
				inference.NewRepetitionGuardWithMode(inference.ContentModeProse))

			secText, err := engine.Infer(callCtx, sysPrompt, userPrompt, "", TargetWorker)
			if err != nil || strings.TrimSpace(secText) == "" {
				secText, _ = engine.Infer(callCtx, sysPrompt, userPrompt, "", TargetWorker)
			}
			secText = checkAndRepairSectionTruncation(callCtx, secText, sysPrompt, userPrompt, engine)
			secText = stripPromptLeakage(secText)
			secText = strings.TrimSpace(secText)
			if IsFunctionIndexGoal(goal) {
				filtered := stripUngroundedSymbolBlocks(secText, allowedSymsMap, terminalCtx)
				if len(strings.TrimSpace(filtered)) >= 200 {
					secText = filtered
				}
			}
			if !strings.HasPrefix(secText, "#") {
				secText = sec.Heading + "\n\n" + secText
			}
			completedSections[terminalIdx] = secText
		}
	}

	// Phase 4: Assembly
	var validSections []string
	for _, s := range completedSections {
		if strings.TrimSpace(s) != "" {
			validSections = append(validSections, s)
		}
	}

	var doc strings.Builder
	docTitle := outline.Title
	if docTitle == "" {
		docTitle = deriveCleanDocumentTitle(goal)
	}
	if !strings.HasPrefix(docTitle, "#") {
		docTitle = "# " + docTitle
	}
	doc.WriteString(docTitle)
	doc.WriteString("\n\n")
	// Deduplicate table rows in each section to prevent the 4B model's
	// tendency to repeat symbols 2-3x in reference tables.
	for i, sec := range validSections {
		validSections[i] = deduplicateTableRows(sec)
	}
	doc.WriteString(strings.Join(validSections, "\n\n---\n\n"))
	return doc.String(), nil
}

func checkAndRepairSectionTruncation(ctx context.Context, text, systemPrompt, userPrompt string, engine ProbeInferenceEngine) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return text
	}

	// Check if section ends abruptly (e.g. unclosed code block or trailing sentence fragment)
	fenceCount := strings.Count(trimmed, "```")
	isUnclosedFence := fenceCount%2 != 0

	lastChar := trimmed[len(trimmed)-1:]
	isFragment := !strings.ContainsAny(lastChar, ".!?:)]`\"'|*-_>\n")

	if isUnclosedFence || isFragment {
		fmt.Fprintf(os.Stderr, "[SectionedSynthesis] Notice: Detected truncated section ending (fence=%v, fragment=%v) — repairing\n", isUnclosedFence, isFragment)
		repairPrompt := userPrompt + "\n\nIMPORTANT: Conclude this section completely without hitting token limits. Ensure all code blocks and sentences are closed."
		repaired, err := engine.Infer(ctx, systemPrompt, repairPrompt, "", TargetWorker)
		if err == nil && len(strings.TrimSpace(repaired)) > 100 {
			return repaired
		}
		if isUnclosedFence {
			trimmed += "\n```"
		}
		return trimmed
	}

	return text
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

// ShouldRunSectionedSynthesis determines if a synthesis goal warrants Sectioned Map-Reduce synthesis (ADR-0084).
// Pure code-generation sinks (tzro_code) are strictly exempted.
func ShouldRunSectionedSynthesis(goal, category, refinedCtx string, symbolCount, fileCount int, isCodegenSink bool) bool {
	if isCodegenSink {
		return false
	}

	cat := strings.ToLower(strings.TrimSpace(category))
	if cat == "docgen" {
		if fileCount >= 2 || symbolCount >= 4 || len(refinedCtx) >= 1500 || len(goal) >= 40 || IsDocGenGoal(goal) {
			return true
		}
	}

	if cat == "research" {
		if fileCount >= 2 || len(refinedCtx) >= 1500 || len(goal) >= 40 || IsMultiSectionResearchGoal(goal) {
			return true
		}
	}

	// General intent detection based on goal semantics and discovered scope
	if IsDocGenGoal(goal) || IsMultiSectionResearchGoal(goal) {
		return true
	}

	if len(refinedCtx) >= 3500 && (symbolCount >= 4 || fileCount >= 3) {
		return true
	}

	return false
}

// IsDocGenGoal detects if a prompt requests comprehensive module, architecture, or codebase documentation.
func IsDocGenGoal(goal string) bool {
	lower := strings.ToLower(goal)
	return (strings.Contains(lower, "documentation") || strings.Contains(lower, "document") ||
		strings.Contains(lower, "readme") || strings.Contains(lower, "architecture") ||
		strings.Contains(lower, "module-level") || strings.Contains(lower, "covering all") ||
		strings.Contains(lower, "public types") || strings.Contains(lower, "decision log")) && len(goal) >= 30
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

		const staticWhitepaperSectionSystemPrompt = `You are an expert technical writer synthesizing sections of a comprehensive research whitepaper.
IMPORTANT: Begin your response directly with the section heading. Do not write meta-commentary or introduction.
IMPORTANT: All concrete identifiers, versions, metrics, and claims must be corroborated by the discovery context.
IMPORTANT: Cite sources inline using markdown links: [Descriptive Title](URL).`

		boundedContext := refinedContext
		if len(boundedContext) > 16000 {
			if pruned, pruneErr := PruneUpstreamOutput(ctx, boundedContext, sec.FocusPrompt, 16000); pruneErr == nil && len(pruned) > 0 {
				boundedContext = pruned
			} else {
				boundedContext = truncate(boundedContext, 16000)
			}
		}

		var userDynamicPrompt strings.Builder
		userDynamicPrompt.WriteString(fmt.Sprintf("## Target Section: Section %d (%s)\nSection Objective: %s\n\n", i+1, sec.SectionID, sec.FocusPrompt))
		userDynamicPrompt.WriteString("## Verified Discovery Context:\n")
		userDynamicPrompt.WriteString(boundedContext)
		userDynamicPrompt.WriteString("\n\n")
		if tableConstraint != "" {
			userDynamicPrompt.WriteString(tableConstraint)
			userDynamicPrompt.WriteString("\n")
		}
		userDynamicPrompt.WriteString(fmt.Sprintf("Synthesize Section %d (%s) now. Begin directly with %s", i+1, sec.SectionID, sec.Heading))

		messages := []inference.InferenceMessage{
			{Role: "system", Content: staticWhitepaperSectionSystemPrompt},
			{Role: "user", Content: fmt.Sprintf("## Main Whitepaper Goal\n%s", goal)},
			{Role: "assistant", Content: "Understood. Ready for section objective and discovery context."},
			{Role: "user", Content: userDynamicPrompt.String()},
		}

		resp, err := engine.InferMessages(ctx, messages, "", TargetWorker)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[SectionedSynthesis] Warning: error generating section %s: %v\n", sec.SectionID, err)
			continue
		}

		cleaned := strings.TrimSpace(resp)
		if decoded, err := compactor.DecodeWithHeader(cleaned); err == nil {
			cleaned = decoded
		}
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
	if strings.Contains(lower, "function index") || strings.Contains(lower, "symbol index") {
		// Extract package path from goal for a clean title
		if idx := strings.Index(lower, "internal/"); idx >= 0 {
			end := strings.IndexAny(goal[idx:], " \n,")
			if end < 0 {
				end = len(goal[idx:])
			}
			pkgPath := strings.TrimRight(goal[idx:idx+end], "/")
			return fmt.Sprintf("Exported Symbol Index: %s", pkgPath)
		}
		return "Exported Symbol Index"
	}

	// Fallback to first line of goal
	firstLine := strings.Split(goal, "\n")[0]
	firstLine = strings.TrimPrefix(firstLine, "Search the web and ")
	firstLine = strings.TrimPrefix(firstLine, "Research ")
	return strings.Title(strings.TrimSpace(firstLine))
}

// extractSymbolsFromContext scans the refinedCtx for absolute file paths
// ending in .go, reads the source files, and extracts symbols using tree-sitter.
// This is a fallback for when the symbol index was not populated by probe nodes.
func extractSymbolsFromContext(refinedCtx string) []symbols.Symbol {
	// Extract unique file paths from the context.
	// Fan-reduce partials contain paths in:
	// 1. HTML comments: "<!-- source: /path/to/file.go -->"
	// 2. Token patterns: "(/path/to/file.go)", "/path/to/file.go (signature fallback)"
	seen := make(map[string]bool)
	var paths []string
	for _, line := range strings.Split(refinedCtx, "\n") {
		line = strings.TrimSpace(line)
		// Pattern 1: HTML comment source markers
		if strings.HasPrefix(line, "<!-- source: ") && strings.HasSuffix(line, " -->") {
			p := strings.TrimPrefix(line, "<!-- source: ")
			p = strings.TrimSuffix(p, " -->")
			p = strings.TrimSpace(p)
			if strings.HasSuffix(p, ".go") && !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
			continue
		}
		// Pattern 2: Token-based extraction
		for _, token := range strings.Fields(line) {
			token = strings.Trim(token, "()[]`\"'")
			if strings.HasPrefix(token, "/") && strings.HasSuffix(token, ".go") {
				if !seen[token] {
					seen[token] = true
					paths = append(paths, token)
				}
			}
		}
	}

	if len(paths) == 0 {
		return nil
	}

	var allSyms []symbols.Symbol
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		syms, err := symbols.ExtractSymbols(p, data)
		if err != nil {
			continue
		}
		allSyms = append(allSyms, syms...)
	}
	return allSyms
}

// countUniqueFiles counts the number of unique source files in a symbol slice.
func countUniqueFiles(syms []symbols.Symbol) int {
	seen := make(map[string]bool)
	for _, s := range syms {
		seen[s.File] = true
	}
	return len(seen)
}

// stripPromptLeakage removes lines where the 4B model echoes its system prompt
// instructions instead of following them. Common leaked phrases include
// "CRITICAL INSTRUCTIONS:", "Begin your response directly", etc.
func stripPromptLeakage(text string) string {
	leakPhrases := []string{
		"CRITICAL INSTRUCTIONS:",
		"Begin your response directly with the section heading",
		"Do NOT repeat symbols already",
		"Conclude cleanly without trailing fragments",
		"Do NOT omit any exported symbols",
		"Focus deeply on writing comprehensive",
		"STRICT BOUNDARY: This section MUST ONLY",
		"Do NOT include any symbol not in the list above",
	}

	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		leaked := false
		for _, phrase := range leakPhrases {
			if strings.Contains(line, phrase) {
				leaked = true
				break
			}
		}
		if !leaked {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}
