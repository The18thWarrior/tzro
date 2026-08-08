package executor

import (
	"encoding/json"
	"fmt"
	"strings"
	"tzro/internal/tools"
)

// buildProbeSystemPrompt constructs the system prompt for the probe's Local Model call.
// Includes per-tool parameter schemas so the local model knows exactly what arguments
// each tool requires (fixes empty-arguments bug where model omitted required params).
// taskContext, when non-empty, is pinned above the exploration goal so task requirements
// (e.g., target language, specific APIs) override workspace conventions.
func buildProbeSystemPrompt(goal string, allowedTools []string, taskContext string, upstreamContext string) string {
	toolList := ""
	for i, t := range allowedTools {
		if i > 0 {
			toolList += ", "
		}
		toolList += t
	}

	toolSchemas := buildToolSchemaReference(allowedTools)

	var taskContextSection string
	if taskContext != "" {
		taskContextSection = fmt.Sprintf(`
## Task Specification (PRIORITY — follow these requirements over workspace conventions)
%s

`, taskContext)
	}

	// ADR-0059: Upstream context baked into system prompt for maximum KV cache reuse.
	var upstreamSection string
	if upstreamContext != "" {
		upstreamSection = fmt.Sprintf(`
## Upstream Node Outputs (from completed DAG steps)
%s

`, upstreamContext)
	}

	return fmt.Sprintf(`You are a Probe Node — an autonomous code exploration agent.
%s%sYour goal: %s

You have access to these tools: [%s]

## Tool Parameter Reference
%s
On each step, reason about what to explore next. 
If you need to use a tool, output an XML tag: <ACTION>{"tool": "tool_name", "arguments": {"param": "value"}}</ACTION>.
If you have gathered enough information and are ready to synthesize a final answer, output <SYNTHESIZE_READY>.

IMPORTANT: Do NOT output markdown JSON blocks for the action, use the raw <ACTION> tag.

Be systematic. Build understanding incrementally.
Exploration strategy: list_dir for structure, read_file for content of files relevant to your goal, search_files for patterns (like grep) to locate specific definitions across multiple files when you do not know the exact filenames.
If you list a directory and see files that are directly related to your goal, read them using read_file directly rather than trying to search or guess. Do not assume search_files is required if you already know which files to read.
Do not read the same file multiple times with overlapping ranges. Once you have read a file, assume you know its structure and move on to the other files in the directory to ensure complete coverage. Do not exhaust your step budget on a single file.
If a path fails with "does not exist", DO NOT call list_dir or read_file on that path again. You MUST use search_files to locate the correct file instead of guessing directory names.
Do not assume documentation files describe implementation — verify by reading source code.`, taskContextSection, upstreamSection, goal, toolList, toolSchemas)
}

// extractionStrategySection returns an additional prompt section that biases
// the Probe toward SELECT queries with specific columns when the goal implies
// field-level data extraction. Returns empty string for aggregation goals.
func extractionStrategySection(extractionMode bool) string {
	if !extractionMode {
		return ""
	}
	return `
## EXTRACTION MODE (your goal asks for specific records/fields)
Your goal asks you to find and return specific data (names, emails, values, etc.).
- PRIORITIZE queries that SELECT the actual columns mentioned in the goal
- Example: SELECT name, email FROM <table> WHERE account_name = 'Target'
- Do NOT waste queries on COUNT(*) alone — the goal needs actual data rows, not just counts
- Your FIRST sql_cached_data call should retrieve the data requested by the goal
- You may run a COUNT query for verification AFTER retrieving the actual data
`
}

// buildAnalyzeSystemPrompt constructs the system prompt for an Analyze Node's
// Thought Chain. Unlike the probe prompt (codebase exploration), this teaches
// the model to use cache exploration tools for data analysis, filtering, and
// aggregation. When no cached data is available, it degrades to synthesis.
func buildAnalyzeSystemPrompt(goal string, allowedTools []string, taskContext string, extractedCacheIds []string, upstreamContext string, isExtractionGoal ...bool) string {
	extractionMode := len(isExtractionGoal) > 0 && isExtractionGoal[0]
	toolList := ""
	for i, t := range allowedTools {
		if i > 0 {
			toolList += ", "
		}
		toolList += t
	}

	toolSchemas := buildToolSchemaReference(allowedTools)

	var taskContextSection string
	if taskContext != "" {
		taskContextSection = fmt.Sprintf(`
## Task Specification (PRIORITY — follow these requirements over workspace conventions)
%s

`, taskContext)
	}
	// Build the available cacheId section from deterministically extracted IDs
	var cacheIdSection string
	if len(extractedCacheIds) > 0 {
		cacheIdSection = "## AVAILABLE CACHE IDS (use these EXACT strings — do NOT invent your own)\n"
		for _, id := range extractedCacheIds {
			cacheIdSection += fmt.Sprintf("- **%s** — use this as both the cacheId argument and the SQL table name\n", id)
			cacheIdSection += fmt.Sprintf("  Example: introspect_cache({\"cacheId\": \"%s\"})\n", id)
			cacheIdSection += fmt.Sprintf("  Example: sql_cached_data({\"cacheId\": \"%s\", \"sql\": \"SELECT * FROM %s LIMIT 5\"})\n", id, id)
		}
		cacheIdSection += "\nIMPORTANT: Use ONLY the cacheIds listed above. Do NOT fabricate or guess cacheIds.\n"
	} else {
		cacheIdSection = "## CACHE ID DISCOVERY\nNo cacheIds were pre-extracted from upstream context. Check the upstream context for cacheId values, or use introspect_cache with any cacheId you find.\n"
	}

	// Build few-shot examples with real cacheId substitution.
	// The 4B model needs concrete examples of the exact <ACTION> XML format
	// to reliably emit tool calls. Without these, it often generates reasoning
	// text without valid tags (benchmark: 0/15 successful tool calls).
	//
	// IMPORTANT: Only show cache tool examples when real cache IDs are available.
	// When no real IDs exist, omit these examples entirely — if there's no cache
	// to query, showing examples teaches the model to hallucinate cache IDs.
	var fewShotSection string
	if len(extractedCacheIds) > 0 {
		exampleCacheId := extractedCacheIds[0]
		fewShotSection = fmt.Sprintf(`## MANDATORY: Tool Call Format — Follow These Examples EXACTLY

Your FIRST action must be to inspect the cache schema. Output this EXACT format:

Step 1 — Always start here:
<ACTION>{"tool": "introspect_cache", "arguments": {"cacheId": "%s"}}</ACTION>

Step 2 — Count total records:
<ACTION>{"tool": "sql_cached_data", "arguments": {"cacheId": "%s", "sql": "SELECT COUNT(*) as total FROM %s"}}</ACTION>

Step 3 — Group and count by a column:
<ACTION>{"tool": "sql_cached_data", "arguments": {"cacheId": "%s", "sql": "SELECT column_name, COUNT(*) as cnt FROM %s GROUP BY column_name ORDER BY cnt DESC"}}</ACTION>

CRITICAL RULES:
- You MUST wrap every tool call in <ACTION>...</ACTION> tags — no other format works
- You MUST use the exact JSON structure shown above — {"tool": "...", "arguments": {...}}
- Do NOT use markdown code blocks, do NOT describe what you would do — CALL THE TOOL
- If you want data, you MUST call sql_cached_data. Do NOT try to count or aggregate manually from text.
`, exampleCacheId, exampleCacheId, exampleCacheId, exampleCacheId, exampleCacheId)
	} else {
		// No cache data available — show generic ACTION format without cache-specific examples.
		// This prevents the model from hallucinating cache IDs while still teaching the XML format.
		fewShotSection = `## MANDATORY: Tool Call Format

You MUST wrap every tool call in <ACTION>...</ACTION> tags — no other format works.
You MUST use this exact JSON structure: {"tool": "tool_name", "arguments": {"key": "value"}}
Do NOT use markdown code blocks, do NOT describe what you would do — CALL THE TOOL.

No cached data is available for this task. Synthesize your analysis from the text data in the accumulated context above.
`
	}

	// ADR-0059: Upstream context baked into system prompt.
	var upstreamSection string
	if upstreamContext != "" {
		upstreamSection = fmt.Sprintf(`
## Upstream Node Outputs (from completed DAG steps)
%s

`, upstreamContext)
	}

	return fmt.Sprintf(`You are an Analyze Node — an autonomous data analysis agent.
%s%sYour goal: %s

You have access to these tools: [%s]

## Tool Parameter Reference
%s

## Data Analysis Strategy
You analyze data from upstream nodes using a systematic approach:

1. First, check the accumulated context for a cacheId from an upstream data source.
2. If a cacheId is available:
   - Use 'introspect_cache' to understand the data schema (column names, types, sample records)
   - IMPORTANT: Use the EXACT column names from introspect_cache in your SQL queries
   - Use 'sql_cached_data' to query the data using **SQLite** SQL dialect
   - The table name is the cacheId itself
3. If no cacheId is available, synthesize your analysis from the raw text data in the accumulated context.

%s
## CRITICAL: cacheId Handling
The cacheId is an OPAQUE STRING identifier.
- You MUST copy the cacheId EXACTLY as it appears in the examples or context — do NOT round, truncate, or modify the digits
- The cacheId is NOT a number — it is a string. Copy it character-by-character
- Do NOT invent or guess a cacheId — only use IDs that appear in the context above

## SQLite SQL Dialect
The query engine is SQLite. Use SQLite-compatible syntax ONLY:
- String concatenation: Use || not CONCAT()
- GROUP_CONCAT: Use GROUP_CONCAT(col) or GROUP_CONCAT(DISTINCT col) — NO 'SEPARATOR' keyword
- Boolean: Use 1/0, not TRUE/FALSE
- Case-insensitive LIKE is default in SQLite
- LIMIT/OFFSET for pagination (no FETCH/OFFSET)

## Data Quality Best Practices
- ALWAYS start with introspect_cache to see column names, then SELECT COUNT(*) to verify record count
- Check for empty/blank values: SELECT COUNT(*) FROM <table> WHERE ColName IS NULL OR TRIM(ColName) = ''
- Use COALESCE to handle NULLs: SELECT COALESCE(ColName, 'Unspecified') as ColName
- Use TRIM() to clean whitespace: SELECT TRIM(ColName) as ColName
- When grouping text data, first run SELECT DISTINCT ColName to see actual values
- Validate your results: if a GROUP BY total doesn't match the overall COUNT(*), investigate why
- CRITICAL: Run aggregation queries (GROUP BY with COUNT) as a SINGLE complete SQL query
  - Do NOT try to count items incrementally or by hand — let SQL do the counting
  - After grouping, verify: SELECT SUM(cnt) FROM (SELECT COUNT(*) as cnt FROM table GROUP BY col)
    should equal SELECT COUNT(*) FROM table
`+extractionStrategySection(extractionMode)+`

## Text Matching and Filtering
- For exact value lookups, use LIKE with wildcards: WHERE ColName LIKE '%%%%value%%%%'
- For case-insensitive matching: WHERE LOWER(ColName) = LOWER('value')
- When filtering by a company or category name, always try case-insensitive LIKE first

%s
On each step, reason about what analysis to perform next.
If you need to use a tool, output an XML tag: <ACTION>{"tool": "tool_name", "arguments": {"param": "value"}}</ACTION>.
If you have gathered enough information and are ready to synthesize a final answer, output <SYNTHESIZE_READY>.

IMPORTANT: Do NOT output markdown JSON blocks for the action, use the raw <ACTION> tag.

Be systematic. Start by understanding the data schema, then build your analysis incrementally.
If a SQL query returns an error, try a simpler approach or inspect the data with introspect_cache first.`, taskContextSection, upstreamSection, goal, toolList, toolSchemas, cacheIdSection, fewShotSection)
}

// buildToolSchemaReference generates a compact reference block describing each tool's
// parameters. Extracts the inner properties from the GBNF schema envelope.
func buildToolSchemaReference(allowedTools []string) string {
	var sb strings.Builder
	for _, toolName := range allowedTools {
		t := tools.GetTool(toolName)
		if t == nil {
			continue
		}
		schemaStr, err := t.GetSchema()
		if err != nil || schemaStr == "" {
			continue
		}

		// Parse the GBNF schema to extract inner properties
		var schema map[string]interface{}
		if json.Unmarshal([]byte(schemaStr), &schema) != nil {
			continue
		}

		// Navigate: properties -> tool_arguments -> properties
		props, _ := schema["properties"].(map[string]interface{})
		if props == nil {
			continue
		}
		toolArgs, _ := props["tool_arguments"].(map[string]interface{})
		if toolArgs == nil {
			continue
		}
		innerProps, _ := toolArgs["properties"].(map[string]interface{})
		if innerProps == nil {
			continue
		}
		requiredList, _ := toolArgs["required"].([]interface{})

		// Build compact parameter listing
		requiredSet := make(map[string]bool)
		for _, r := range requiredList {
			if s, ok := r.(string); ok {
				requiredSet[s] = true
			}
		}

		sb.WriteString(fmt.Sprintf("### %s\n", toolName))
		// Include tool description (capped at 100 chars) for semantic context
		desc := t.Description()
		if desc != "" {
			if len(desc) > 100 {
				desc = desc[:97] + "..."
			}
			sb.WriteString(fmt.Sprintf("_%s_\n", desc))
		}
		for paramName, paramVal := range innerProps {
			paramMap, _ := paramVal.(map[string]interface{})
			paramType := "string"
			if paramMap != nil {
				if t, ok := paramMap["type"].(string); ok {
					paramType = t
				}
			}
			reqMarker := ""
			if requiredSet[paramName] {
				reqMarker = " (REQUIRED)"
			}
			sb.WriteString(fmt.Sprintf("- %s: %s%s\n", paramName, paramType, reqMarker))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
