package classifier

import (
	"context"
	"encoding/json"
	"strings"

	"tzro/internal/inference"
	"tzro/internal/templates"
)

// TopologyArchetypeSystemPrompt instructs the worker model to classify a user
// prompt into exactly one of the 6 structural Topology Archetypes (ADR-0087).
const TopologyArchetypeSystemPrompt = `You are a plan topology classifier for the tzro agentic execution engine. Classify the user's request into exactly one structural topology archetype.

## Archetypes

- list-synthesis: Exploration, research, discovery, or analysis tasks that produce an answer or synthesis in memory/terminal without writing an output file to disk.
- list-and-write: Exploration, research, documentation generation, or report writing tasks that MUST save/write output content to a file on disk (e.g. .md, .txt, README).
- multi-list-synthesis: Tasks requiring exploration of multiple independent sources, directories, or targets that must be synthesized together.
- codegen: Code generation or modification tasks — writing or editing source code files (.go, .ts, .py, etc.). Context exploration followed by code generation.
- data-analysis: Analyzing structured or tabular data from CSV files, databases, or cache tables using counting, grouping, filtering, or SQL operations.
- action-chain: Multi-step tool workflows — sequential tool dispatch where each step uses a distinct action tool.

## Rules
- Respond with ONLY valid JSON matching the schema. No markdown fences.
- If the task asks to generate, update, or write a documentation file, report, README, index, or summary to a file path, choose "list-and-write".
- If the task produces a documentation artifact (function index, symbol index, API reference, changelog, architecture document), choose "list-and-write" — these are always written to disk.
- If the task asks to analyze, explore, summarize, or research without producing a persistent document, choose "list-synthesis".
- If the task involves writing or modifying executable source code files (.go, .ts, .py, etc.), choose "codegen".
- If the task involves multiple independent explorations, choose "multi-list-synthesis".
- If ambiguous between "list-synthesis" and "list-and-write", prefer "list-and-write" when the task produces structured output that should persist.

## Response Schema
{
  "topology": "<one of the 6 archetypes above>"
}`

// TopologyArchetypeSchema is the GBNF-constraining JSON schema for Pass 1 topology routing.
const TopologyArchetypeSchema = `{
  "type": "object",
  "properties": {
    "topology": {
      "type": "string",
      "enum": ["list-synthesis", "list-and-write", "multi-list-synthesis", "codegen", "data-analysis", "action-chain"]
    }
  },
  "required": ["topology"]
}`

// SourceModalitySystemPrompt instructs the worker model to classify the data source domain (ADR-0087).
const SourceModalitySystemPrompt = `You are a source modality classifier for the tzro agentic execution engine. Classify the user's request into exactly one source modality domain.

## Modalities

- local: Workspace repository, local files, directories, codebases, local documentation, or on-disk data.
- web: Internet research, searching the web, online documentation, reading web pages, looking up external CVEs/advisories, or citing URLs.
- hybrid: Tasks requiring BOTH local workspace exploration AND live web search/browsing.

## Rules
- Respond with ONLY valid JSON matching the schema. No markdown fences.
- If the prompt mentions web search, browsing URLs, looking up internet information, or citing web sources, choose "web".
- If the prompt references local files, paths, internal packages, or local repo docs, choose "local".
- If ambiguous, default to "local".

## Response Schema
{
  "modality": "local" | "web" | "hybrid"
}`

// SourceModalitySchema is the GBNF-constraining JSON schema for Pass 2 source modality routing.
const SourceModalitySchema = `{
  "type": "object",
  "properties": {
    "modality": {
      "type": "string",
      "enum": ["local", "web", "hybrid"]
    }
  },
  "required": ["modality"]
}`

// Legacy aliases for backward compatibility
const TemplateCategorySystemPrompt = TopologyArchetypeSystemPrompt
const TemplateCategorySchema = TopologyArchetypeSchema

// HasWebTools checks whether web search / browse tools are available in the tool inventory.
func HasWebTools(toolNames []string) bool {
	for _, name := range toolNames {
		if name == "web_search" || name == "web_browse" || strings.HasPrefix(name, "web_") {
			return true
		}
	}
	return false
}

// ClassifyTopologyArchetype executes Pass 1 of intake routing on the worker model.
func ClassifyTopologyArchetype(ctx context.Context, prompt string, toolNames []string) templates.TemplateCategory {
	req := inference.NewSimpleRequest(TopologyArchetypeSystemPrompt, prompt, TopologyArchetypeSchema)
	req.ToolNames = toolNames

	resContent, err := inference.ExecuteWorkerStructured(ctx, req)
	if err != nil {
		return templates.ProbeSynthesis
	}

	var result struct {
		Topology string `json:"topology"`
		Category string `json:"category"` // legacy fallback
	}
	if json.Unmarshal([]byte(resContent), &result) != nil {
		return templates.ProbeSynthesis
	}

	catStr := result.Topology
	if catStr == "" {
		catStr = result.Category
	}

	// Validate against registered categories (handles aliases)
	cat := templates.TemplateCategory(catStr)
	if templates.Get(cat) == nil {
		return templates.ProbeSynthesis
	}

	return cat
}

// ClassifySourceModality executes Pass 2 of intake routing on the worker model.
func ClassifySourceModality(ctx context.Context, prompt string, toolNames []string) templates.SourceModality {
	req := inference.NewSimpleRequest(SourceModalitySystemPrompt, prompt, SourceModalitySchema)
	req.ToolNames = toolNames

	resContent, err := inference.ExecuteWorkerStructured(ctx, req)
	if err != nil {
		return templates.SourceLocal
	}

	var result struct {
		Modality string `json:"modality"`
	}
	if json.Unmarshal([]byte(resContent), &result) != nil {
		return templates.SourceLocal
	}

	switch templates.SourceModality(result.Modality) {
	case templates.SourceWeb:
		return templates.SourceWeb
	case templates.SourceHybrid:
		return templates.SourceHybrid
	default:
		return templates.SourceLocal
	}
}

// ClassifyPlanTemplate coordinates the 2-pass Single-Decision routing process (ADR-0087).
// Pass 1 classifies the Topology Archetype. Pass 2 classifies the Source Modality
// only when multiple tool domains (e.g. web tools) exist in the tool inventory.
func ClassifyPlanTemplate(ctx context.Context, prompt string, toolNames []string) (templates.TemplateCategory, templates.SourceModality) {
	topology := ClassifyTopologyArchetype(ctx, prompt, toolNames)

	var modality templates.SourceModality
	if HasWebTools(toolNames) {
		modality = ClassifySourceModality(ctx, prompt, toolNames)
	} else {
		modality = templates.SourceLocal
	}

	return topology, modality
}

// ClassifyTemplateCategory is the legacy single-pass wrapper returning the Topology Archetype.
func ClassifyTemplateCategory(ctx context.Context, prompt string, toolNames []string) templates.TemplateCategory {
	return ClassifyTopologyArchetype(ctx, prompt, toolNames)
}
