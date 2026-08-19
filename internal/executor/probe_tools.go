package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"tzro/internal/compiler"
	cfgpkg "tzro/internal/config"
	"tzro/internal/embeddings"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/symbols"
	"tzro/internal/tools"
)

// --- Goal Classifiers ---

// classifyProbeGoal determines the substrate mode from the probe's goal text
// using keyword matching. This is a fast, deterministic fallback when
// SubstrateMode is not set by the planner.
//
// Returns "overview", "focused", "aggregate", or "" (unknown → Thought Chain fallback).
func classifyProbeGoal(goal string) string {
	lower := strings.ToLower(goal)

	// Overview patterns: broad documentation, README, architecture summaries
	overviewKeywords := []string{
		"readme", "overview", "architecture", "project structure",
		"comprehensive", "high-level", "documentation", "describe the project",
		"explain the codebase", "write a readme",
	}
	for _, kw := range overviewKeywords {
		if strings.Contains(lower, kw) {
			return "overview"
		}
	}

	// Aggregate patterns: summarize collections, list items, consolidate
	aggregateKeywords := []string{
		"summarize all", "summarize the", "list all", "aggregate",
		"consolidate", "compile all", "summarize adr", "summarize each",
		"all files in", "every file", "catalog",
	}
	for _, kw := range aggregateKeywords {
		if strings.Contains(lower, kw) {
			return "aggregate"
		}
	}

	// Focused patterns: specific function/module analysis, deep dives, git history exploration
	focusedKeywords := []string{
		"explain how", "trace the", "follow the", "debug",
		"how does", "call graph", "entry point", "specific",
		"deep dive", "detailed analysis of",
		"commit history", "git log", "regression", "who changed",
		"what changed", "evolution of", "improvement arc",
	}
	for _, kw := range focusedKeywords {
		if strings.Contains(lower, kw) {
			return "focused"
		}
	}

	return "" // Unknown → Thought Chain fallback
}

// SearchQueriesSchema constrains token generation to a JSON array of search query strings.
const SearchQueriesSchema = `{
	"type": "array",
	"items": { "type": "string" }
}`

// GenerateSearchQueries uses a single 1-shot Worker call with GBNF array grammar
// to decompose a research goal into 2 to 3 distinct search queries.
// Falls back to deterministic clause extraction if inference fails or yields < 2 queries.
func GenerateSearchQueries(ctx context.Context, engine ProbeInferenceEngine, goal string) ([]string, error) {
	if strings.TrimSpace(goal) == "" {
		return nil, nil
	}

	systemPrompt := `You are a search query optimizer for a technical research agent.
Given the user's research goal and context, generate 2 to 4 distinct, high-precision search query strings designed to find authoritative, factual information from different angles.

Follow these decomposition rules:
1. ENTITY TARGETING: If specific named entities, tools, products, or standards are mentioned (e.g. llama.cpp, Ollama, MLX, vLLM, Go CVE, Temporal, Restate), generate targeted queries pinning each entity + its required comparison dimensions.
2. THEMATIC DECOMPOSITION: If the goal is conceptual or thematic without specific entities (e.g. common business practices for remote teams), extract the core subject noun-phrase and decompose into distinct sub-aspect queries (e.g. communication standards, performance metrics, workflow guidelines).
3. NO GENERIC QUERIES: Never emit broad 1-2 word queries (e.g. "market analysis" or "security"). Always include concrete technical qualifiers.

Output ONLY a JSON array of query strings with no other text, e.g. ["query 1", "query 2", "query 3"].`

	userPrompt := fmt.Sprintf("Research Goal: %s\n\nGenerate 2-4 distinct web search queries:", goal)

	messages := []inference.InferenceMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	if engine != nil {
		res, err := engine.InferMessages(ctx, messages, SearchQueriesSchema, TargetWorker)
		if err == nil {
			var parsed []string
			if json.Unmarshal([]byte(strings.TrimSpace(res)), &parsed) == nil && len(parsed) >= 2 {
				var valid []string
				for _, q := range parsed {
					qTrim := strings.TrimSpace(q)
					if qTrim != "" {
						valid = append(valid, qTrim)
					}
				}
				if len(valid) >= 2 {
					return valid, nil
				}
			}
		}
	}

	// Fallback to deterministic query decomposition
	return extractSearchQueryVariantsFromGoal(goal), nil
}

// extractSearchQueryVariantsFromGoal decomposes a research goal into 2-4 diverse search queries
// using neural embedding cosine similarity and semantic clause segmentation.
func extractSearchQueryVariantsFromGoal(goal string) []string {
	clauses := segmentGoalIntoClauses(goal)
	if len(clauses) == 0 {
		clauses = []string{goal}
	}

	metaPrototypes := []string{
		"Search the web and browse at least 3 distinct source pages",
		"Use web_search to find authoritative sources and web_browse to inspect",
		"Synthesize a structured comparative analysis tailored for a CTO",
		"Cite specific URLs and visited sources",
		"Format citations inline using markdown links",
		"Produce a comprehensive well-sourced answer",
	}

	type scoredClause struct {
		text   string
		sim    float32
		isMeta bool
	}
	var scored []scoredClause

	if inference.GlobalEmbeddingSidecar != nil && inference.GlobalEmbeddingSidecar.IsAvailable() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		goalVec, err := inference.GlobalEmbeddingSidecar.Embed(ctx, goal)
		if err == nil && len(goalVec) > 0 {
			allTexts := append([]string{}, clauses...)
			allTexts = append(allTexts, metaPrototypes...)
			vecs, err := inference.GlobalEmbeddingSidecar.EmbedBatch(ctx, allTexts)
			if err == nil && len(vecs) == len(allTexts) {
				clauseVecs := vecs[:len(clauses)]
				metaVecs := vecs[len(clauses):]

				for i, cv := range clauseVecs {
					simToGoal := inference.GlobalEmbeddingSidecar.CosineSimilarity(goalVec, cv)
					var maxMetaSim float32
					for _, mv := range metaVecs {
						ms := inference.GlobalEmbeddingSidecar.CosineSimilarity(cv, mv)
						if ms > maxMetaSim {
							maxMetaSim = ms
						}
					}
					isMeta := maxMetaSim > 0.70 && maxMetaSim > simToGoal
					scored = append(scored, scoredClause{
						text:   clauses[i],
						sim:    simToGoal,
						isMeta: isMeta,
					})
				}
			}
		}
	}

	if len(scored) == 0 {
		for _, cl := range clauses {
			sim := float32(embeddings.CosineSimilarity(goal, cl))
			scored = append(scored, scoredClause{text: cl, sim: sim, isMeta: false})
		}
	}

	var candidates []scoredClause
	for _, sc := range scored {
		if !sc.isMeta && len(sc.text) >= 5 {
			candidates = append(candidates, sc)
		}
	}
	if len(candidates) == 0 {
		candidates = scored
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].sim > candidates[j].sim
	})

	var queries []string
	seen := make(map[string]bool)
	for _, c := range candidates {
		q := strings.TrimSpace(c.text)
		q = strings.Trim(q, ".,;:-_\"'")
		lower := strings.ToLower(q)
		if len(q) >= 5 && !seen[lower] {
			seen[lower] = true
			queries = append(queries, q)
			if len(queries) >= 3 {
				break
			}
		}
	}

	if len(queries) == 0 {
		queries = []string{goal}
	}
	return appendDomainAnchorsIfRelevant(goal, queries)
}

// appendDomainAnchorsIfRelevant appends targeted domain filters for recognized technical ecosystems.
func appendDomainAnchorsIfRelevant(goal string, queries []string) []string {
	lower := strings.ToLower(goal)
	var anchors []string
	if strings.Contains(lower, "go ") || strings.Contains(lower, "golang") || strings.Contains(lower, "cve") {
		if strings.Contains(lower, "cve") || strings.Contains(lower, "vulnerabilit") || strings.Contains(lower, "security") {
			anchors = append(anchors, "site:pkg.go.dev/vuln OR site:go.dev/doc/security")
		}
	}
	if strings.Contains(lower, "temporal") {
		anchors = append(anchors, "site:docs.temporal.io")
	}
	if strings.Contains(lower, "restate") {
		anchors = append(anchors, "site:docs.restate.dev")
	}
	if strings.Contains(lower, "inngest") {
		anchors = append(anchors, "site:inngest.com")
	}
	if strings.Contains(lower, "gguf") || strings.Contains(lower, "llama.cpp") {
		anchors = append(anchors, "site:github.com/ggerganov/llama.cpp")
	}

	if len(anchors) == 0 {
		return queries
	}

	result := append([]string{}, queries...)
	if len(queries) > 0 {
		cleanBase := queries[0]
		for _, p := range []string{"site:pkg.go.dev/vuln", "site:go.dev", "site:docs.temporal.io", "site:docs.restate.dev", "site:inngest.com", "site:github.com"} {
			cleanBase = strings.ReplaceAll(cleanBase, p, "")
		}
		cleanBase = strings.TrimSpace(cleanBase)
		if cleanBase != "" {
			for _, a := range anchors {
				result = append(result, fmt.Sprintf("%s %s", cleanBase, a))
			}
		}
	}
	return result
}

func stripToolMetaPhrases(s string) string {
	toolPhrases := []string{
		"using web_search and web_browse",
		"using web_search",
		"using web_browse",
		"use web_search and web_browse",
		"use web_search",
		"use web_browse",
		"via web_search and web_browse",
		"via web_search",
		"via web_browse",
		"with web_search and web_browse",
		"with web_search",
		"with web_browse",
		"web_search and web_browse",
		"web_search",
		"web_browse",
		"on at least 3 authoritative sources",
		"on at least 3 distinct source URLs",
		"on at least 3 source URLs",
		"on at least 3 sources",
		"on at least 3 URLs",
		"across at least 3 distinct source URLs",
		"across at least 3 source URLs",
		"across at least 3 URLs",
		"citing all consulted source URLs",
		"citing all visited URLs",
		"citing all source URLs",
		"citing all URLs",
		"citing visited URLs",
		"citing source URLs",
		"citing consulted source URLs",
		"with cited URLs",
		"with cited source URLs",
		"with exact URL citations",
		"with verified source URLs",
	}
	result := s
	for _, p := range toolPhrases {
		re := regexp.MustCompile("(?i)" + regexp.QuoteMeta(p))
		result = re.ReplaceAllString(result, " ")
	}
	return strings.TrimSpace(result)
}

func segmentGoalIntoClauses(goal string) []string {
	var clauses []string
	cleanGoal := strings.TrimSpace(stripToolMetaPhrases(goal))
	if cleanGoal == "" {
		return nil
	}

	// 1. Split by newlines and structural sentence delimiters
	rawSegments := strings.FieldsFunc(cleanGoal, func(r rune) bool {
		return r == '\n' || r == ';' || r == ':'
	})
	if len(rawSegments) == 0 {
		rawSegments = []string{cleanGoal}
	}

	// 2. Further split compound segments by comparative conjunctions
	conjunctions := []string{
		" and compare it to ", " and compare to ", " and compare with ",
		" compared to ", " versus ", " vs. ", " vs ",
		" as well as ", ", ", " and ",
	}

	for _, seg := range rawSegments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		splitDone := false
		for _, conj := range conjunctions {
			if idx := strings.Index(strings.ToLower(seg), conj); idx > 0 {
				p1 := strings.TrimSpace(seg[:idx])
				p2 := strings.TrimSpace(seg[idx+len(conj):])
				if len(p1) >= 4 && len(p2) >= 4 {
					clauses = append(clauses, p1, p2)
					splitDone = true
					break
				}
			}
		}
		if !splitDone {
			clauses = append(clauses, seg)
		}
	}

	// Strip common leading action prefixes from candidate clauses
	var cleanedClauses []string
	prefixes := []string{
		"Search the web and browse at least ",
		"Search the web for ", "search the web for ",
		"Search for ", "search for ",
		"Find ", "find ",
		"Research ", "research ",
		"Look up ", "look up ",
		"Investigate ", "investigate ",
		"Explore ", "explore ",
	}

	for _, cl := range clauses {
		cl = strings.TrimSpace(cl)
		for _, p := range prefixes {
			if strings.HasPrefix(cl, p) {
				cl = strings.TrimSpace(cl[len(p):])
				break
			}
		}
		if len(cl) >= 4 {
			cleanedClauses = append(cleanedClauses, cl)
		}
	}

	return cleanedClauses
}

// extractSearchQueryFromGoal derives a web search query from the probe goal text.
func extractSearchQueryFromGoal(goal string) string {
	variants := extractSearchQueryVariantsFromGoal(goal)
	if len(variants) > 0 {
		return variants[0]
	}
	return strings.TrimSpace(goal)
}

// parseActionFromResponse parses <ACTION>tool_name(args)</ACTION> or <ACTION>{"tool":"...", ...}</ACTION> or signals synthesis readiness.
func parseActionFromResponse(response string) (action, toolName string, args map[string]interface{}) {
	if strings.Contains(response, "<SYNTHESIZE_READY>") {
		return "synthesize", "", nil
	}
	// Check for <ACTION>{"tool": "...", "arguments": {...}}</ACTION>
	jsonRe := regexp.MustCompile(`(?s)<ACTION>\s*(\{.*?\})\s*</ACTION>`)
	if m := jsonRe.FindStringSubmatch(response); len(m) == 2 {
		var parsed struct {
			Tool      string                 `json:"tool"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if json.Unmarshal([]byte(m[1]), &parsed) == nil && parsed.Tool != "" {
			if parsed.Arguments == nil {
				parsed.Arguments = make(map[string]interface{})
			}
			return "tool_call", parsed.Tool, parsed.Arguments
		}
	}
	// Check for <ACTION>tool_name(args)</ACTION>
	re := regexp.MustCompile(`(?s)<ACTION>\s*([a-zA-Z0-9_-]+)\((.*?)\)\s*</ACTION>`)
	if m := re.FindStringSubmatch(response); len(m) == 3 {
		tool := m[1]
		rawArgs := strings.TrimSpace(m[2])
		var parsedArgs map[string]interface{}
		if rawArgs != "" {
			_ = json.Unmarshal([]byte(rawArgs), &parsedArgs)
		}
		if parsedArgs == nil {
			parsedArgs = make(map[string]interface{})
		}
		return "tool_call", tool, parsedArgs
	}
	return "synthesize", "", nil
}

// goalImpliesExtraction returns true when the goal text suggests the user
// wants specific data fields extracted (names, emails, records) rather than
// computed aggregates (counts, totals, distributions). This biases the
// Probe's SQL queries toward SELECT with specific columns instead of COUNT(*).
//
// Intentionally broad: false positives (treating aggregation as extraction)
// are low-cost because SELECT queries still work for aggregations.
func goalImpliesExtraction(goal string) bool {
	lower := strings.ToLower(goal)
	// Action verbs that imply returning specific records.
	// Note: "return the" is intentionally omitted — it's too broad and
	// matches aggregation goals like "return the top 5 countries".
	extractionVerbs := []string{
		"extract ", "list the ", "list all ", "find the ",
		"show the ", "get the ", "retrieve the ", "look up ", "lookup ",
		"fetch the ", "pull the ", "display the ",
		"find and return", "find all ",
		"return the name", "return the email", "return the record",
		"return the detail", "return the value", "return the data",
		"return their ",
	}
	for _, verb := range extractionVerbs {
		if strings.Contains(lower, verb) {
			return true
		}
	}
	// Field-level nouns that suggest row-level data is needed
	extractionFields := []string{
		"name column", "email column", "names and email",
		"email address", "for each matching", "for each row",
		"each matching row", "each matching lead",
		"for each lead", "for every ",
	}
	for _, field := range extractionFields {
		if strings.Contains(lower, field) {
			return true
		}
	}
	return false
}

// --- Config Predicates ---

// isAnalyzeConfig returns true if the allowed tools contain cache tools,
// indicating this is an analyze node's Thought Chain rather than a probe.
func isAnalyzeConfig(allowedTools []string) bool {
	for _, t := range allowedTools {
		if t == "introspect_cache" || t == "sql_cached_data" {
			return true
		}
	}
	return false
}

// containsTool checks if a specific tool name is in the allowed tools list.
func containsTool(allowedTools []string, tool string) bool {
	for _, t := range allowedTools {
		if t == tool {
			return true
		}
	}
	return false
}

// shouldPhaseGateApply determines whether the synthesis phase gate should fire
// for a given probe config. Returns true when either:
// 1. Legacy condition: isAnalyze && SourceHint=cache && has sql_cached_data (ADR-0053)
// 2. New condition: RequiredToolDispatch is non-empty (ADR-0068)
func shouldPhaseGateApply(config *compiler.ProbeConfig) bool {
	legacy := isAnalyzeConfig(config.AllowedTools) &&
		config.SourceHint == "cache" &&
		containsTool(config.AllowedTools, "sql_cached_data")
	return legacy || len(config.RequiredToolDispatch) > 0
}

// requiredToolsBlocked checks whether all tools in RequiredToolDispatch have
// been dispatched. Returns true and the list of missing tools if any required
// tool has not been used. Returns false if no dispatch requirements exist.
func requiredToolsBlocked(required []string, usedToolSet map[string]bool) (blocked bool, missing []string) {
	for _, tool := range required {
		if !usedToolSet[tool] {
			missing = append(missing, tool)
		}
	}
	return len(missing) > 0, missing
}

// --- Tool Argument Utilities ---

// sanitizeToolName attempts to recover a valid tool name from garbled model output.
// The 4B model sometimes concatenates reasoning into the tool field, producing names
// like "list_dir_dir_contents_path_or_file_name_and_path_if_file_is_specified".
// This function finds the longest allowed tool name that appears as a prefix.
func sanitizeToolName(garbled string, allowedTools map[string]bool) string {
	bestMatch := ""
	for toolName := range allowedTools {
		if strings.HasPrefix(garbled, toolName) && len(toolName) > len(bestMatch) {
			bestMatch = toolName
		}
	}
	return bestMatch
}

// normalizeToolArguments remaps miskeyed arguments based on the tool's schema.
// When the local model emits a bare string as arguments (e.g. "CONTEXT.md"),
// UnmarshalJSON wraps it as {"query": "CONTEXT.md"}. But filesystem tools
// expect {"path": "CONTEXT.md"}. This function detects the mismatch by
// inspecting the tool's schema and remaps the value to the correct key.
func normalizeToolArguments(toolName string, args map[string]interface{}) map[string]interface{} {
	// Only normalize if there's a "query" key that might be a fallback
	queryVal, hasQuery := args["query"]
	if !hasQuery {
		return args
	}

	// Get the tool's schema to find required parameter names
	t := tools.GetTool(toolName)
	if t == nil {
		return args
	}
	schemaStr, err := t.GetSchema()
	if err != nil || schemaStr == "" {
		return args
	}

	var schema map[string]interface{}
	if json.Unmarshal([]byte(schemaStr), &schema) != nil {
		return args
	}

	// Navigate: properties -> tool_arguments -> required
	props, _ := schema["properties"].(map[string]interface{})
	if props == nil {
		return args
	}
	toolArgs, _ := props["tool_arguments"].(map[string]interface{})
	if toolArgs == nil {
		return args
	}
	requiredList, _ := toolArgs["required"].([]interface{})
	if len(requiredList) == 0 {
		return args
	}

	// Find the first required parameter that isn't "query"
	for _, r := range requiredList {
		reqKey, ok := r.(string)
		if !ok || reqKey == "query" {
			continue
		}
		// If the required key is missing from args, remap "query" to it
		if _, exists := args[reqKey]; !exists {
			args[reqKey] = queryVal
			delete(args, "query")
			fmt.Fprintf(os.Stderr, "[Probe] Normalized argument: remapped 'query' -> '%s' for tool '%s'\n", reqKey, toolName)
			break
		}
	}

	return args
}

// rescueEmptyPathFromThought attempts to extract a file/directory path from the
// model's nextThought text when filesystem or git tool arguments are missing or empty.
// The 4B local model frequently describes what it wants to read in its reasoning
// (e.g., "Read CONTEXT.md", "explore internal/compiler") but fails to populate
// the arguments JSON correctly. This function recovers those paths.
func rescueEmptyPathFromThought(toolName string, args map[string]interface{}, thought string) map[string]interface{} {
	// Only rescue for filesystem and git tools that take a path parameter
	fsTools := map[string]bool{"read_file": true, "list_dir": true, "search_files": true, "git_log": true, "git_diff": true}
	if !fsTools[toolName] {
		return args
	}

	// Check if path is already populated
	if pathVal, exists := args["path"]; exists {
		if pathStr, ok := pathVal.(string); ok && pathStr != "" {
			// Resolve relative paths to absolute
			if !filepath.IsAbs(pathStr) {
				resolved := cfgpkg.ResolvePath(pathStr)
				if resolved != pathStr {
					fmt.Fprintf(os.Stderr, "[Probe] Resolved relative path: '%s' -> '%s' for tool '%s'\n", pathStr, resolved, toolName)
					args["path"] = resolved
				}
			}
			return args
		}
	}

	// Try to extract a path from the thought text
	extracted := extractPathFromText(thought)
	if extracted != "" {
		// Resolve relative paths to absolute using TZRO_DIR
		if !filepath.IsAbs(extracted) {
			resolved := cfgpkg.ResolvePath(extracted)
			if resolved != extracted {
				fmt.Fprintf(os.Stderr, "[Probe] Resolved rescued path: '%s' -> '%s' for tool '%s'\n", extracted, resolved, toolName)
				extracted = resolved
			}
		}
		args["path"] = extracted
		fmt.Fprintf(os.Stderr, "[Probe] Rescued empty path from thought: '%s' for tool '%s'\n", extracted, toolName)
	}

	return args
}

// rescueRefFromThought extracts git ref, branch names, tags, or commit hashes from thought text
// for git_show and git_diff when ref is missing or empty.
func rescueRefFromThought(toolName string, args map[string]interface{}, thought string) map[string]interface{} {
	if toolName != "git_show" && toolName != "git_diff" {
		return args
	}

	if refVal, exists := args["ref"]; exists {
		if refStr, ok := refVal.(string); ok && strings.TrimSpace(refStr) != "" {
			return args
		}
	}

	lower := strings.ToLower(thought)

	// Natural language ref phrases
	refPatterns := []struct {
		re *regexp.Regexp
	}{
		{regexp.MustCompile(`(?i)(?:changes\s+since|diff\s+against|diff\s+with|show\s+commit|inspect\s+commit|commit)\s+([a-zA-Z0-9_\-./~^]+)`)},
	}

	for _, p := range refPatterns {
		if matches := p.re.FindStringSubmatch(thought); len(matches) > 1 {
			candidate := strings.Trim(matches[1], `'".,;()[]`)
			if candidate != "" && !isCommonNonRefWord(candidate) {
				args["ref"] = candidate
				fmt.Fprintf(os.Stderr, "[Probe] Rescued git ref from thought: '%s' for tool '%s'\n", candidate, toolName)
				return args
			}
		}
	}

	// Hex patterns (7+ hex digits, e.g. commit hashes)
	hexRe := regexp.MustCompile(`\b([0-9a-fA-F]{7,40})\b`)
	if matches := hexRe.FindStringSubmatch(thought); len(matches) > 1 {
		args["ref"] = matches[1]
		fmt.Fprintf(os.Stderr, "[Probe] Rescued commit hash ref from thought: '%s' for tool '%s'\n", matches[1], toolName)
		return args
	}

	// Keywords implying HEAD
	if strings.Contains(lower, "latest commit") || strings.Contains(lower, "most recent commit") || strings.Contains(lower, "recent commit") || strings.Contains(lower, "head") {
		args["ref"] = "HEAD"
		return args
	}

	// Default fallback for git_show
	if toolName == "git_show" {
		args["ref"] = "HEAD"
	}

	return args
}

func isCommonNonRefWord(s string) bool {
	lower := strings.ToLower(s)
	switch lower {
	case "the", "a", "an", "this", "that", "to", "in", "for", "and", "or", "history", "log", "repo", "repository", "files", "changes":
		return true
	}
	return false
}

// rescueMaxCountFromThought extracts commit count limits from thought text
// for git_log when maxCount is missing or unset.
func rescueMaxCountFromThought(toolName string, args map[string]interface{}, thought string) map[string]interface{} {
	if toolName != "git_log" {
		return args
	}

	if countVal, exists := args["maxCount"]; exists {
		if countInt, ok := countVal.(int); ok && countInt > 0 {
			return args
		}
		if countFloat, ok := countVal.(float64); ok && countFloat > 0 {
			return args
		}
	}

	// Match patterns like "last 5 commits", "recent 10", "past 20 commits"
	countRe := regexp.MustCompile(`(?i)(?:last|recent|past)\s+(\d+)\s*(?:commits?|entries|logs?|changes)?`)
	if matches := countRe.FindStringSubmatch(thought); len(matches) > 1 {
		if n, err := strconv.Atoi(matches[1]); err == nil && n > 0 {
			args["maxCount"] = n
			fmt.Fprintf(os.Stderr, "[Probe] Rescued maxCount from thought: %d for tool '%s'\n", n, toolName)
			return args
		}
	}

	return args
}

// rescueFileGlobFromThought extracts fileGlob patterns from thought text
// for search_files when fileGlob is missing.
func rescueFileGlobFromThought(toolName string, args map[string]interface{}, thought string) map[string]interface{} {
	if toolName != "search_files" {
		return args
	}

	if globVal, exists := args["fileGlob"]; exists {
		if globStr, ok := globVal.(string); ok && strings.TrimSpace(globStr) != "" {
			return args
		}
	}

	// 1. Explicit glob pattern like *.ts, *.go, *.md, *.py, *.json
	globRe := regexp.MustCompile(`(?i)\*(\.[a-zA-Z0-9_]+)\b`)
	if matches := globRe.FindStringSubmatch(thought); len(matches) > 1 {
		glob := "*" + strings.ToLower(matches[1])
		args["fileGlob"] = glob
		fmt.Fprintf(os.Stderr, "[Probe] Rescued fileGlob from thought: '%s' for tool '%s'\n", glob, toolName)
		return args
	}

	// 2. Extension pattern like "in .md files", ".go files"
	extRe := regexp.MustCompile(`(?i)(?:in\s+)?(\.[a-zA-Z0-9_]+)\s+files?\b`)
	if matches := extRe.FindStringSubmatch(thought); len(matches) > 1 {
		glob := "*" + strings.ToLower(matches[1])
		args["fileGlob"] = glob
		fmt.Fprintf(os.Stderr, "[Probe] Rescued fileGlob extension from thought: '%s' for tool '%s'\n", glob, toolName)
		return args
	}

	// 3. Natural language language names: "Go files", "Python files", "TypeScript files", "Markdown files", "Rust files", etc.
	lower := strings.ToLower(thought)
	langMap := []struct {
		pattern string
		glob    string
	}{
		{"go file", "*.go"},
		{"python file", "*.py"},
		{"typescript file", "*.ts"},
		{"ts file", "*.ts"},
		{"javascript file", "*.js"},
		{"js file", "*.js"},
		{"markdown file", "*.md"},
		{"md file", "*.md"},
		{"rust file", "*.rs"},
		{"json file", "*.json"},
		{"yaml file", "*.yaml"},
		{"yml file", "*.yml"},
	}

	for _, lm := range langMap {
		if strings.Contains(lower, lm.pattern) {
			args["fileGlob"] = lm.glob
			fmt.Fprintf(os.Stderr, "[Probe] Rescued fileGlob from language name: '%s' for tool '%s'\n", lm.glob, toolName)
			return args
		}
	}

	return args
}

// extractPathFromText uses heuristics to find file/directory paths in free text.
// Looks for: absolute paths, quoted names, relative paths with extensions, known directory names.
func extractPathFromText(text string) string {
	if text == "" {
		return ""
	}

	// Priority 1: Absolute paths (e.g., /home/user/project/tzro/CONTEXT.md)
	absPathRe := regexp.MustCompile(`(/[a-zA-Z0-9._\-]+(?:/[a-zA-Z0-9._\-]+)+)`)
	if matches := absPathRe.FindStringSubmatch(text); len(matches) > 1 {
		return matches[1]
	}

	// Priority 2: Quoted or backtick-delimited names (e.g., 'tzro-mcp', `bootstrap.go`, "main.go")
	// This catches bare names the model mentions in reasoning regardless of extension.
	quotedRe := regexp.MustCompile("['\"`]([a-zA-Z0-9_][a-zA-Z0-9_.\\-]*)['\"`]")
	if matches := quotedRe.FindStringSubmatch(text); len(matches) > 1 {
		candidate := matches[1]
		// Exclude common English words and meta-terms that appear in quotes
		exclusions := map[string]bool{"path": true, "query": true, "error": true, "tool": true, "arguments": true, "file": true, "directory": true}
		if !exclusions[candidate] {
			return candidate
		}
	}

	// Priority 3: Filenames with extensions (e.g., CONTEXT.md, go.mod, main.go)
	fileRe := regexp.MustCompile(`\b([a-zA-Z0-9_\-]+\.[a-zA-Z]{1,10})\b`)
	if matches := fileRe.FindStringSubmatch(text); len(matches) > 1 {
		return matches[1]
	}

	// Priority 4: Known directory patterns (e.g., internal/compiler, cmd/tzro)
	dirRe := regexp.MustCompile(`\b((?:internal|cmd|pkg|plugins|tests|docs)/[a-zA-Z0-9_\-/]+)\b`)
	if matches := dirRe.FindStringSubmatch(text); len(matches) > 1 {
		return matches[1]
	}

	// Priority 5: Bare filenames with hyphens (e.g., tzro-mcp, llama-server)
	// These are common executable/project names the model refers to without quotes.
	bareHyphenRe := regexp.MustCompile(`\b([a-zA-Z][a-zA-Z0-9]*(?:-[a-zA-Z0-9]+)+)\b`)
	if matches := bareHyphenRe.FindStringSubmatch(text); len(matches) > 1 {
		candidate := matches[1]
		// Exclude common non-path hyphenated phrases
		exclusions := map[string]bool{"tool-call": true, "read-file": true, "list-dir": true, "next-step": true}
		if !exclusions[candidate] {
			return candidate
		}
	}

	// Priority 6: Bare known directory names
	bareDirRe := regexp.MustCompile(`\b(internal|cmd|pkg|plugins|tests|docs|bin)\b`)
	if matches := bareDirRe.FindStringSubmatch(text); len(matches) > 1 {
		return matches[1]
	}

	return ""
}

// --- Tool Output Utilities ---

// isToolError checks if a tool result string indicates a tool-level error.
// Tools return JSON with "success":false for validation failures, nonexistent
// paths, etc. The Go error return from tools.Call is nil in these cases.
func isToolError(result string) bool {
	// Check for the JSON success field pattern
	if strings.Contains(result, `"success":false`) {
		return true
	}
	// Also catch the "Error: ..." prefix used for disallowed tools and parse failures
	if strings.HasPrefix(result, "Error:") {
		return true
	}
	return false
}

// truncate shortens a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// --- Post-Dispatch Hooks ---

// extractAndPersistSymbols runs the Symbol Extractor on a file's content
// and persists any extracted symbols to the Symbol Index. Called as a
// post-read_file hook in the Thought Chain loop (ADR-0047).
//
// Errors are logged but not propagated — symbol extraction is best-effort
// and must not disrupt the probe's primary exploration loop.
func extractAndPersistSymbols(probeID, taskID, resolvedPath string) {
	contentBytes, err := os.ReadFile(resolvedPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Probe] Symbol extraction: failed to read resolved path %s: %v\n", resolvedPath, err)
		return
	}
	syms, err := symbols.ExtractSymbols(filepath.Base(resolvedPath), contentBytes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Probe] Symbol extraction error for %s: %v\n", resolvedPath, err)
		return
	}
	if len(syms) == 0 {
		return
	}

	// Set full file paths (extractor only sees the basename for language detection)
	for i := range syms {
		syms[i].File = resolvedPath
	}

	if err := memory.DB.InsertSymbols(probeID, taskID, syms); err != nil {
		fmt.Fprintf(os.Stderr, "[Probe] Symbol index persist error: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "[Probe] Extracted %d symbols from %s\n", len(syms), resolvedPath)
}

// extractURLsFromWebSearch parses web_search JSON output and returns discovered URLs.
// Uses structured JSON parsing first (for the ToolSuccess envelope format), then
// falls back to regex extraction for non-standard output formats.
//
// P0 fix: The 4B local model cannot reliably extract URLs from search result
// prose to pass as web_browse arguments (benchmark run 8: 40 empty-URL rejections).
// This function deterministically extracts URLs so the probe can auto-populate
// web_browse calls without requiring model-side URL parsing.
func extractURLsFromWebSearch(toolOutput string) []string {
	// Primary: parse the ToolSuccess JSON envelope
	// Format: {"success":true,"data":{"results":[{"title":"...","url":"...","snippet":"..."}],...}}
	var envelope struct {
		Data struct {
			Results []struct {
				URL string `json:"url"`
			} `json:"results"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(toolOutput), &envelope) == nil && len(envelope.Data.Results) > 0 {
		var urls []string
		for _, r := range envelope.Data.Results {
			if r.URL != "" {
				urls = append(urls, r.URL)
			}
		}
		if len(urls) > 0 {
			return urls
		}
	}

	// Secondary: try flat results array (raw SearchResult format)
	var flat struct {
		Results []struct {
			URL string `json:"url"`
		} `json:"results"`
	}
	if json.Unmarshal([]byte(toolOutput), &flat) == nil && len(flat.Results) > 0 {
		var urls []string
		for _, r := range flat.Results {
			if r.URL != "" {
				urls = append(urls, r.URL)
			}
		}
		if len(urls) > 0 {
			return urls
		}
	}

	// Fallback: regex extraction for non-standard formats
	urlRe := regexp.MustCompile(`https?://[^\s"',\]}>]+`)
	matches := urlRe.FindAllString(toolOutput, 20)
	// Deduplicate
	seen := make(map[string]bool)
	var unique []string
	for _, u := range matches {
		if !seen[u] {
			seen[u] = true
			unique = append(unique, u)
		}
	}
	return unique
}

// nodeIDFromProbeID extracts the original node ID from the probe's composite ID.
// Probe IDs follow the pattern taskID + "_" + nodeID (set in executor.go).
// Falls back to probeID if the prefix doesn't match.
func nodeIDFromProbeID(probeID, taskID string) string {
	prefix := taskID + "_"
	if strings.HasPrefix(probeID, prefix) {
		return strings.TrimPrefix(probeID, prefix)
	}
	return probeID
}
