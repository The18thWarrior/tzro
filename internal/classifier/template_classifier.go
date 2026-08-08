package classifier

import (
	"context"
	"encoding/json"

	"tzro/internal/inference"
	"tzro/internal/templates"
)

// TemplateCategorySystemPrompt instructs the router model to classify a user
// prompt into one of the 7 Plan Template Registry categories (ADR-0048).
const TemplateCategorySystemPrompt = `You are a template category classifier for the tzro execution engine. Classify the user's request into exactly one plan template category.

## Categories

- explore-only: Codebase exploration, architecture analysis, directory traversal, file reading tasks. The task requires discovering information but NOT writing any output files.
- docgen: Documentation generation, function indexes, API docs, report writing, creating summaries or analysis documents that must be saved to a file. Exploration followed by writing results to disk.
- research: Web research tasks — searching the internet, reading web pages, comparing products. Includes tasks that write research results to a file.
- data-analysis: Analyzing structured or tabular data from CSV files, databases, or cached data sources. Counting, grouping, filtering, ranking operations.
- multi-probe-synthesis: Tasks requiring exploration of multiple independent sources that must be synthesized together. Multi-directory analysis, cross-project comparison.
- codegen: Code generation or modification tasks — writing or editing source code files (.go, .ts, .py, etc.). Requires codebase context gathering followed by code generation.
- action-chain: Multi-step tool workflows — sequential tool dispatch where each step uses a different tool. API calls, data pipelines, automated processes with 2+ distinct tool steps.

## Rules
- Respond with ONLY valid JSON matching the schema. No markdown fences.
- If the task involves generating documentation, reports, summaries, indexes, or analysis documents, choose "docgen". This includes function indexes, API docs, and architecture summaries — even if the task requires reading source code first. If the output is a .md, .txt, or documentation file about code (not executable code itself), choose "docgen".
- If the task involves writing or modifying executable source code files (.go, .ts, .py, etc.), choose "codegen". Only choose this when the output IS source code, not documentation about source code.
- If the task involves web search, choose "research".
- If the task involves multiple independent explorations, choose "multi-probe-synthesis".
- If the task involves sequential tool calls (not exploration), choose "action-chain".
- If ambiguous, default to "explore-only".

## Response Schema
{
  "category": "<one of the 7 categories above>"
}`

// TemplateCategorySchema is the GBNF-constraining JSON schema for template
// category classification. The enum list must match the TemplateCategory
// constants in the templates package.
const TemplateCategorySchema = `{
  "type": "object",
  "properties": {
    "category": {
      "type": "string",
      "enum": ["explore-only", "docgen", "research", "data-analysis", "multi-probe-synthesis", "codegen", "action-chain"]
    }
  },
  "required": ["category"]
}`

// ClassifyTemplateCategory routes template category classification through the
// router sidecar. Returns ExploreOnly as a safe default on any failure.
func ClassifyTemplateCategory(ctx context.Context, prompt string, toolNames []string) templates.TemplateCategory {
	req := inference.NewSimpleRequest(TemplateCategorySystemPrompt, prompt, TemplateCategorySchema)
	req.ToolNames = toolNames

	resContent, err := inference.ExecuteRouterStructured(ctx, req)
	if err != nil {
		return templates.ExploreOnly
	}

	var result struct {
		Category string `json:"category"`
	}
	if json.Unmarshal([]byte(resContent), &result) != nil {
		return templates.ExploreOnly
	}

	// Validate against registered categories
	cat := templates.TemplateCategory(result.Category)
	if templates.Get(cat) == nil {
		return templates.ExploreOnly
	}

	return cat
}
