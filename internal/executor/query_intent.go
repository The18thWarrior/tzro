package executor

// query_intent.go — GBNF-constrained QueryIntent extraction for the
// Analyze Node query phase.
//
// Instead of asking the 4B model to compose query_builder operations
// from scratch (which it consistently fails at), we run a fast GBNF
// extraction pass to pull structured keywords from the goal, then
// deterministically map them into query_builder operations.
//
// The model extracts; code composes.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"tzro/internal/cache"
	"tzro/internal/inference"
)

// QueryIntent is the GBNF-constrained extraction target.
// The model fills in fields from the goal + column context.
type QueryIntent struct {
	// Multi-filter support (ADR-0076: Deterministic Query Path)
	Filters   []FilterClause `json:"filters,omitempty"`
	AggExtras []AggClause    `json:"aggExtras,omitempty"`

	// Filter clause (WHERE) — legacy single-filter fields, kept for backward compatibility
	FilterColumn   string `json:"filterColumn,omitempty"`
	FilterOperator string `json:"filterOperator,omitempty"`
	FilterValue    string `json:"filterValue,omitempty"`

	// Grouping (GROUP BY)
	GroupColumn string `json:"groupColumn,omitempty"`

	// Aggregation
	AggFunction string `json:"aggFunction,omitempty"`
	AggColumn   string `json:"aggColumn,omitempty"`

	// Ordering (ORDER BY)
	OrderColumn    string `json:"orderColumn,omitempty"`
	OrderDirection string `json:"orderDirection,omitempty"`

	// Select specific columns (empty = all)
	SelectColumns []string `json:"selectColumns,omitempty"`
}

// queryIntentSchema is the GBNF-constraining JSON schema for intent extraction.
// Enum constraints prevent hallucination of operators and functions.
const queryIntentSchema = `{
  "type": "object",
  "properties": {
    "filterColumn":   { "type": "string" },
    "filterOperator": { "type": "string", "enum": ["=", "!=", "LIKE", ">", "<", ">=", "<=", "IS NULL", "IS NOT NULL"] },
    "filterValue":    { "type": "string" },
    "groupColumn":    { "type": "string" },
    "aggFunction":    { "type": "string", "enum": ["COUNT", "SUM", "AVG", "MIN", "MAX", "GROUP_CONCAT"] },
    "aggColumn":      { "type": "string" },
    "orderColumn":    { "type": "string" },
    "orderDirection": { "type": "string", "enum": ["ASC", "DESC"] },
    "selectColumns":  { "type": "array", "items": { "type": "string" } }
  }
}`

// buildIntentExtractionPrompt builds the system prompt for the GBNF extraction pass.
func buildIntentExtractionPrompt(columns []string) string {
	return fmt.Sprintf(`You are extracting structured query parameters from a data analysis goal.

The dataset has these columns: %s

Extract ONLY data query operations from the goal:
- filterColumn: a column name from the list above to filter on (e.g., "account_name", "Country")
- filterOperator: the comparison operator
- filterValue: the value to match (e.g., "Walmart", "Yes")
- groupColumn: a column name to group by
- aggFunction/aggColumn: aggregate function and column
- orderColumn/orderDirection: sorting column and direction
- selectColumns: specific columns to return

CRITICAL RULES:
- filterColumn MUST be one of the column names listed above. NEVER use file paths, filenames, or non-column values.
- filterValue is the data value to match, NOT a file path or filename.
- Ignore any file reading instructions (like "Read the CSV file at..."). Focus only on the data analysis question.
- Leave fields empty ("") if not needed.
- For aggColumn, use "*" for COUNT(*).
- Respond with ONLY valid JSON matching the schema.`, strings.Join(columns, ", "))
}

// ExtractQueryIntent runs incremental GBNF-constrained inference passes to
// extract structured query parameters from the goal text.
//
// Instead of asking the 4B model to generate the full QueryIntent JSON in
// one shot (which stochastically truncates or misses fields), we decompose
// into tiny micro-extractions. Each step picks ONE value from a small enum,
// which the 4B model handles near-deterministically (~0.1s per step).
//
// Steps:
//   1. filterColumn: pick from column enum + "none"
//   2. filterValue: extract free-text value (only if filter needed)
//   3. groupColumn: pick from column enum + "none"
//   4. orderColumn + direction (only if group or filter present)
func ExtractQueryIntent(ctx context.Context, goal string, cacheId string) (*QueryIntent, error) {
	columns := cache.GetCacheColumns(cacheId)
	if len(columns) == 0 {
		return nil, fmt.Errorf("no columns available for cacheId %s", cacheId)
	}

	intent := &QueryIntent{}
	columnList := strings.Join(columns, ", ")

	// Step 1a: Binary gate — does this task need filtering at all?
	needsFilter, err := extractBinaryDecision(ctx, goal,
		"Does the user's question require filtering rows WHERE a column equals a SPECIFIC NAMED value?\n\nAnswer \"yes\" ONLY if the user mentions a specific data value to match (e.g., \"Walmart\", \"Yes\", \"USA\").\nAnswer \"no\" if the user just wants grouping, counting, or breakdowns without a specific filter value.\n\nExamples:\n- \"Find leads where account_name is Walmart\" → yes (specific value: Walmart)\n- \"leads where Target_Account equals Yes\" → yes (specific value: Yes)\n- \"Count leads for each country\" → no (just grouping)\n- \"Group by sector and show percentages\" → no (just grouping)\n- \"For each account owner, count their leads\" → no (just grouping)")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[QueryIntent] Step 1a (needsFilter) failed: %v\n", err)
	} else if needsFilter {
		// Step 1b: Which column to filter on?
		filterCol, err := extractColumnFromEnum(ctx, goal, columns,
			fmt.Sprintf("The dataset has columns: %s\n\nWhich column should be used for the filter?", columnList))
		if err != nil {
			fmt.Fprintf(os.Stderr, "[QueryIntent] Step 1b (filterColumn) failed: %v\n", err)
		} else {
			intent.FilterColumn = matchColumnName(filterCol, columns)

			// Step 1c: What value to filter for?
			if intent.FilterColumn != "" {
				filterVal, err := extractFilterValue(ctx, goal, intent.FilterColumn)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[QueryIntent] Step 1c (filterValue) failed: %v\n", err)
				} else if strings.EqualFold(filterVal, "unknown") || filterVal == "" {
					// Value extractor couldn't find a literal value in the goal text.
					// This means the binary gate was a false positive — discard filter.
					fmt.Fprintf(os.Stderr, "[QueryIntent] Step 1c: discarding filter (value=%q indicates false positive gate)\n", filterVal)
					intent.FilterColumn = ""
				} else {
					intent.FilterValue = filterVal
					intent.FilterOperator = "=" // Default
				}
			}
		}
	}

	// Step 3: Extract group column
	groupCol, err := extractColumnFromEnum(ctx, goal, columns,
		fmt.Sprintf("The dataset has columns: %s\n\nDoes the user's question require grouping rows by a column (e.g., GROUP BY column)? This is needed when the user asks for breakdowns, distributions, or per-category counts. If yes, return the column name. If no grouping is needed, return \"none\".", columnList))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[QueryIntent] Step 3 (groupColumn) failed: %v\n", err)
	} else if groupCol != "" && groupCol != "none" {
		intent.GroupColumn = matchColumnName(groupCol, columns)
	}

	// Step 4: Extract order direction (only if we have group or filter)
	if intent.GroupColumn != "" || intent.FilterColumn != "" {
		orderDir, err := extractOrderDirection(ctx, goal)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[QueryIntent] Step 4 (orderDirection) failed: %v\n", err)
		} else {
			intent.OrderDirection = orderDir
			// Default order column to group column for aggregation queries
			if intent.GroupColumn != "" {
				intent.OrderColumn = intent.GroupColumn
			}
		}
	}

	fmt.Fprintf(os.Stderr, "[QueryIntent] Extracted: filter=%s %s %q, group=%s, agg=%s(%s), order=%s %s, select=%v\n",
		intent.FilterColumn, intent.FilterOperator, intent.FilterValue,
		intent.GroupColumn,
		intent.AggFunction, intent.AggColumn,
		intent.OrderColumn, intent.OrderDirection,
		intent.SelectColumns)

	return intent, nil
}

// extractColumnFromEnum asks the 4B model to pick a single column from the
// schema's column list (or "none"). Uses a GBNF enum constraint — the model
// can ONLY output one of the known column names, making this near-deterministic.
// Routed to the 4B worker (not 1B router) because column selection is a semantic
// task: the model must understand that "Walmart" maps to account_name, not name1.
func extractColumnFromEnum(ctx context.Context, goal string, columns []string, systemPrompt string) (string, error) {
	// Build GBNF enum from column names + "none"
	enumValues := make([]string, 0, len(columns)+1)
	enumValues = append(enumValues, "none")
	enumValues = append(enumValues, columns...)

	// Marshal enum values for JSON schema
	enumJSON, _ := json.Marshal(enumValues)

	schema := fmt.Sprintf(`{
  "type": "object",
  "properties": {
    "column": { "type": "string", "enum": %s }
  },
  "required": ["column"]
}`, string(enumJSON))

	req := inference.NewSimpleRequest(systemPrompt, goal, schema)
	// Not IsLowStakes — semantic column selection requires 4B worker reasoning.

	result, err := inference.ExecuteWorkerStructured(ctx, req)
	if err != nil {
		return "", err
	}

	var parsed struct {
		Column string `json:"column"`
	}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return "", fmt.Errorf("column enum parse failed: %w", err)
	}

	return parsed.Column, nil
}

// extractFilterValue asks the 4B model to extract the literal value to filter
// on. This is a free-text extraction (not enum-constrained) since filter values
// are arbitrary data values like "Walmart", "Yes", "USA".
func extractFilterValue(ctx context.Context, goal string, filterColumn string) (string, error) {
	systemPrompt := fmt.Sprintf(`Extract the filter value from the user's question.

The user wants to filter the column "%s". What EXACT value do they specify?

RULES:
- Return ONLY a value that appears LITERALLY in the user's question text
- Examples: "Walmart", "Yes", "USA", "eCommerce"
- Do NOT invent values from sample data or column names
- Do NOT return file paths, column names, or SQL syntax
- If no specific value is mentioned, return "unknown"`, filterColumn)

	schema := `{
  "type": "object",
  "properties": {
    "value": { "type": "string" }
  },
  "required": ["value"]
}`

	req := inference.NewSimpleRequest(systemPrompt, goal, schema)
	req.IsLowStakes = true

	result, err := inference.ExecuteRouterStructured(ctx, req)
	if err != nil {
		return "", err
	}

	var parsed struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return "", fmt.Errorf("filter value parse failed: %w", err)
	}

	return parsed.Value, nil
}

// extractBinaryDecision asks a yes/no question using a GBNF enum constraint.
// The model can ONLY output "yes" or "no" — maximally deterministic.
func extractBinaryDecision(ctx context.Context, goal string, systemPrompt string) (bool, error) {
	schema := `{
  "type": "object",
  "properties": {
    "answer": { "type": "string", "enum": ["yes", "no"] }
  },
  "required": ["answer"]
}`

	req := inference.NewSimpleRequest(systemPrompt, goal, schema)
	req.IsLowStakes = true

	result, err := inference.ExecuteRouterStructured(ctx, req)
	if err != nil {
		return false, err
	}

	var parsed struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return false, fmt.Errorf("binary decision parse failed: %w", err)
	}

	return parsed.Answer == "yes", nil
}

// extractOrderDirection asks the 4B model whether results should be sorted
// ascending or descending. Simple binary enum — near-deterministic.
func extractOrderDirection(ctx context.Context, goal string) (string, error) {
	schema := `{
  "type": "object",
  "properties": {
    "direction": { "type": "string", "enum": ["ASC", "DESC"] }
  },
  "required": ["direction"]
}`

	systemPrompt := "Based on the user's question, should the results be sorted in ascending (ASC) or descending (DESC) order? If the user mentions 'top', 'highest', 'most', or 'ranked', use DESC. Otherwise use ASC."

	req := inference.NewSimpleRequest(systemPrompt, goal, schema)
	req.IsLowStakes = true

	result, err := inference.ExecuteRouterStructured(ctx, req)
	if err != nil {
		return "", err
	}

	var parsed struct {
		Direction string `json:"direction"`
	}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return "", fmt.Errorf("order direction parse failed: %w", err)
	}

	return parsed.Direction, nil
}

// matchColumnName does case-insensitive matching of an extracted column name
// against the known columns. Uses a 3-tier cascade:
//  1. Exact case-insensitive match
//  2. Normalized match (strip non-alphanumeric chars except underscores)
//  3. Prefix/containment match on normalized forms
//
// Returns the correctly-cased column name, or "" if no match found.
func matchColumnName(extracted string, columns []string) string {
	if extracted == "" || extracted == "*" {
		return extracted
	}

	// Tier 1: Exact case-insensitive match
	for _, col := range columns {
		if strings.EqualFold(col, extracted) {
			return col
		}
	}

	// Tier 2: Normalized match — strip non-alphanumeric chars (keep underscores)
	// Handles: Target_Account? → Target_Account_ , Accout_Owner! → Accout_Owner
	normExtracted := normalizeColumnName(extracted)
	if normExtracted == "" {
		fmt.Fprintf(os.Stderr, "[QueryIntent] Rejected non-column value %q (empty after normalization)\n", extracted)
		return ""
	}

	for _, col := range columns {
		normCol := normalizeColumnName(col)
		if strings.EqualFold(normCol, normExtracted) {
			fmt.Fprintf(os.Stderr, "[QueryIntent] FM-4 fuzzy match: %q → %q (normalized match)\n", extracted, col)
			return col
		}
	}

	// Tier 3: Prefix/containment match — the extracted name is a prefix of a column
	// or vice versa (handles truncation or extra suffixes)
	// Requires: shorter form ≥ 8 chars OR ≥ 80% length ratio to prevent false positives
	if len(normExtracted) >= 4 {
		var bestMatch string
		bestLen := 0
		for _, col := range columns {
			normCol := normalizeColumnName(col)
			// Check if one is a prefix of the other
			shorter, longer := normExtracted, normCol
			if len(shorter) > len(longer) {
				shorter, longer = longer, shorter
			}
			if len(shorter) < 4 {
				continue
			}
			// Coverage check: require 80%+ overlap or min 8 chars
			coverageRatio := float64(len(shorter)) / float64(len(longer))
			if coverageRatio < 0.80 && len(shorter) < 8 {
				continue
			}
			if strings.HasPrefix(strings.ToLower(longer), strings.ToLower(shorter)) {
				// Prefer the longest matching prefix (most specific)
				if len(shorter) > bestLen {
					bestLen = len(shorter)
					bestMatch = col
				}
			}
		}
		if bestMatch != "" {
			fmt.Fprintf(os.Stderr, "[QueryIntent] FM-4 fuzzy match: %q → %q (prefix match)\n", extracted, bestMatch)
			return bestMatch
		}
	}

	// No match — clear the value to prevent hallucinated columns from
	// becoming filter operations. The IntentToOperations mapper will skip
	// empty column fields.
	fmt.Fprintf(os.Stderr, "[QueryIntent] Rejected non-column value %q (not in schema)\n", extracted)
	return ""
}

// normalizeColumnName strips non-alphanumeric characters (except underscores)
// from a column name for fuzzy matching purposes.
// Examples: "Target_Account?" → "Target_Account", "Accout_Owner!" → "Accout_Owner"
func normalizeColumnName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// IntentToOperations deterministically maps a QueryIntent to query_builder
// operations. No inference needed — pure code translation.
func IntentToOperations(intent *QueryIntent) []interface{} {
	if intent == nil {
		return nil
	}

	var ops []interface{}

	// Filter
	if intent.FilterColumn != "" && intent.FilterValue != "" {
		op := intent.FilterOperator
		if op == "" {
			op = "="
		}
		ops = append(ops, map[string]interface{}{
			"type":     "filter",
			"column":   intent.FilterColumn,
			"operator": op,
			"value":    intent.FilterValue,
		})
	}

	// Group By
	if intent.GroupColumn != "" {
		ops = append(ops, map[string]interface{}{
			"type":   "group_by",
			"column": intent.GroupColumn,
		})

		// FM-17 fix: Auto-inject COUNT(*) when group_by is present but no
		// aggregation was extracted. GROUP BY without aggregation is almost
		// never the desired behavior — users virtually always want counts.
		if intent.AggFunction == "" {
			intent.AggFunction = "COUNT"
			intent.AggColumn = "*"
		}
	}

	// Aggregate
	if intent.AggFunction != "" {
		aggCol := intent.AggColumn
		if aggCol == "" {
			aggCol = "*"
		}
		aggOp := map[string]interface{}{
			"type":     "aggregate",
			"function": strings.ToUpper(intent.AggFunction),
			"column":   aggCol,
		}
		// Default alias based on function
		aggOp["alias"] = strings.ToLower(intent.AggFunction)
		ops = append(ops, aggOp)
	}

	// Order By
	if intent.OrderColumn != "" {
		dir := intent.OrderDirection
		if dir == "" {
			dir = "DESC"
		}
		ops = append(ops, map[string]interface{}{
			"type":      "order_by",
			"column":    intent.OrderColumn,
			"direction": dir,
		})
	}

	// Select specific columns
	if len(intent.SelectColumns) > 0 {
		// Filter out empty strings
		var validCols []string
		for _, c := range intent.SelectColumns {
			if c != "" {
				validCols = append(validCols, c)
			}
		}
		if len(validCols) > 0 {
			ops = append(ops, map[string]interface{}{
				"type":    "select",
				"columns": validCols,
			})
		}
	}

	return ops
}

// ResolveSelectColumns uses neural embeddings to determine which schema columns
// are relevant to the goal text. Instead of embedding the full goal as one
// vector (which lets high-frequency words like "lead" dominate), it splits
// the goal into phrases and takes the max cosine similarity across phrases
// for each column. This lets specific output terms like "name" and "email"
// match their columns independently of noisy context words.
//
// Falls back gracefully if the embedding sidecar is unavailable.
func ResolveSelectColumns(ctx context.Context, goal string, cacheId string, sampleValues map[string][]string, threshold float64) []string {
	if !inference.GlobalEmbeddingSidecar.IsAvailable() {
		return nil // Caller keeps GBNF selectColumns
	}

	columns := cache.GetCacheColumns(cacheId)
	if len(columns) == 0 {
		return nil
	}

	// Split goal into phrases for independent embedding.
	phrases := splitGoalIntoPhrases(goal)
	if len(phrases) == 0 {
		phrases = []string{goal}
	}
	fmt.Fprintf(os.Stderr, "[ResolveSelectColumns] Phrase-based: %d phrases from goal\n", len(phrases))

	// Build enriched column texts: "email: dalves@walmart.com, jsmith@safeway.com"
	enrichedTexts := make([]string, len(columns))
	for i, col := range columns {
		enriched := col
		if samples, ok := sampleValues[col]; ok && len(samples) > 0 {
			// Take up to 5 sample values
			n := len(samples)
			if n > 5 {
				n = 5
			}
			enriched = col + ": " + strings.Join(samples[:n], ", ")
		}
		enrichedTexts[i] = enriched
	}

	// Batch embed: [phrase1, phrase2, ..., phraseN, enrichedCol1, enrichedCol2, ...]
	allTexts := make([]string, 0, len(phrases)+len(enrichedTexts))
	allTexts = append(allTexts, phrases...)
	allTexts = append(allTexts, enrichedTexts...)

	vecs, err := inference.GlobalEmbeddingSidecar.EmbedBatch(ctx, allTexts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ResolveSelectColumns] Embedding failed (non-fatal): %v\n", err)
		return nil
	}

	if len(vecs) != len(allTexts) {
		fmt.Fprintf(os.Stderr, "[ResolveSelectColumns] Vector count mismatch: got %d, want %d\n", len(vecs), len(allTexts))
		return nil
	}

	phraseVecs := vecs[:len(phrases)]
	colVecs := vecs[len(phrases):]

	// FM-15 fix: Force-include columns that are explicitly mentioned in the goal
	// text. Embedding similarity fails on generic column names like "name" and
	// "email" because these words are too common. When the goal says
	// 'the "name" column' or 'email address', the actual columns score below
	// threshold while irrelevant columns like "Lead_Status" score higher.
	goalLower := strings.ToLower(goal)
	literalMatches := make(map[string]bool)
	for _, col := range columns {
		colLower := strings.ToLower(col)
		// Check for quoted mentions: "name", 'name', or 'name' column
		if strings.Contains(goalLower, `"`+colLower+`"`) ||
			strings.Contains(goalLower, `'`+colLower+`'`) ||
			strings.Contains(goalLower, "'"+colLower+"'") {
			literalMatches[col] = true
			fmt.Fprintf(os.Stderr, "[ResolveSelectColumns] Column %s force-included (literal quoted mention in goal)\n", col)
			continue
		}
		// Check for word-boundary mention: " name " or " email "
		// Only for short column names (≤15 chars) to avoid false positives
		if len(col) <= 15 {
			padded := " " + colLower + " "
			paddedGoal := " " + goalLower + " "
			if strings.Contains(paddedGoal, padded) {
				literalMatches[col] = true
				fmt.Fprintf(os.Stderr, "[ResolveSelectColumns] Column %s force-included (word-boundary mention in goal)\n", col)
			}
		}
	}

	// Score each column: max similarity across all phrase vectors
	type colScore struct {
		name      string
		score     float32
		bestPhrase string
	}
	var scored []colScore
	for i, col := range columns {
		var bestSim float32
		var bestPhr string
		for j, pv := range phraseVecs {
			sim := inference.GlobalEmbeddingSidecar.CosineSimilarity(pv, colVecs[i])
			if sim > bestSim {
				bestSim = sim
				bestPhr = phrases[j]
			}
		}
		fmt.Fprintf(os.Stderr, "[ResolveSelectColumns] Column %-25s → similarity %.4f (phrase: %q)%s\n", col, bestSim, truncate(bestPhr, 40), func() string {
			if literalMatches[col] {
				return " [LITERAL MATCH]"
			}
			return ""
		}())
		if bestSim >= float32(threshold) || literalMatches[col] {
			scored = append(scored, colScore{name: col, score: bestSim, bestPhrase: bestPhr})
		}
	}

	if len(scored) == 0 {
		return nil
	}

	// Sort by score descending, but boost literal matches to ensure they
	// survive the top-N cap. Literal matches get +1.0 to their score for
	// sorting purposes only (they were explicitly mentioned in the goal).
	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			scoreI := scored[i].score
			scoreJ := scored[j].score
			if literalMatches[scored[i].name] {
				scoreI += 1.0
			}
			if literalMatches[scored[j].name] {
				scoreJ += 1.0
			}
			if scoreJ > scoreI {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	// Cap at top 5, but always include all literal matches
	maxCols := 5
	literalCount := len(literalMatches)
	if literalCount > maxCols {
		maxCols = literalCount
	}
	if len(scored) > maxCols {
		scored = scored[:maxCols]
	}

	result := make([]string, len(scored))
	for i, s := range scored {
		result[i] = s.name
	}

	fmt.Fprintf(os.Stderr, "[ResolveSelectColumns] Selected columns: %v (threshold=%.2f)\n", result, threshold)
	return result
}

// splitGoalIntoPhrases breaks a goal string into individual phrases for
// independent embedding. Splits on sentence boundaries, commas, semicolons,
// and common conjunctions. Filters out file-path phrases and very short
// fragments. Always includes the full goal as the last phrase to preserve
// holistic signal for short goals.
func splitGoalIntoPhrases(goal string) []string {
	// First split on sentence boundaries and semicolons
	var rawPhrases []string
	current := goal

	// Split on periods followed by space or end, and semicolons
	for _, sep := range []string{". ", "; ", ".\n"} {
		var parts []string
		for _, chunk := range strings.Split(current, sep) {
			parts = append(parts, chunk)
		}
		if len(parts) > 1 {
			rawPhrases = append(rawPhrases, parts...)
			current = ""
			break
		}
	}
	if current != "" {
		rawPhrases = []string{current}
	}

	// Further split long phrases on commas
	var refined []string
	for _, p := range rawPhrases {
		if len(p) > 60 && strings.Contains(p, ",") {
			parts := strings.Split(p, ",")
			for _, part := range parts {
				refined = append(refined, strings.TrimSpace(part))
			}
		} else {
			refined = append(refined, strings.TrimSpace(p))
		}
	}

	// Filter: remove empty, very short (<4 chars), and file-path-dominated phrases
	var filtered []string
	for _, p := range refined {
		p = strings.TrimSpace(p)
		if len(p) < 4 {
			continue
		}
		// Skip phrases that are predominantly file paths
		lower := strings.ToLower(p)
		if strings.HasPrefix(lower, "read the csv") || strings.HasPrefix(lower, "read the contents") {
			continue
		}
		if strings.Contains(p, "/") && !strings.Contains(p, " ") {
			continue // Pure file path
		}
		filtered = append(filtered, p)
	}

	// Always include full goal as final phrase (holistic fallback)
	filtered = append(filtered, goal)

	return filtered
}


