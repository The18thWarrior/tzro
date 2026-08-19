package executor

// query_intent.go — Embedding-based QueryIntent extraction for the
// Analyze Node query phase.
//
// Uses semantic similarity (cosine distance) against pre-defined canonical
// operation patterns to classify the user's goal into query operations.
// All decisions are made via a single batch embedding call (~10ms),
// replacing the previous 5-step LLM inference pipeline (5-25s).
//
// Key architectural principle: the LLM never sees raw data values.
// Column names come from the schema. Filter values come from the
// goal text via string extraction. Operation classification comes
// from embedding similarity against canonical patterns.

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"tzro/internal/cache"
	"tzro/internal/embeddings"
	"tzro/internal/inference"
)

// QueryIntent is the extraction target for embedding-based intent classification.
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

	// Result limit (0 = no limit)
	Limit int `json:"limit,omitempty"`
}

// --- Embedding Operation Patterns ---
// Canonical phrases for each query operation type. Embedded alongside goal
// phrases and compared via cosine similarity for operation classification.
// Prefixed with "embed" to avoid conflict with deterministic_query.go regex patterns.

var embedGroupPatterns = []string{
	"group by column",
	"breakdown by category",
	"for each unique value",
	"count per category",
	"distribution across groups",
	"categorize records by field",
	"split results by column",
	"aggregate by group",
	"group all records by",
	"count by each",
}

var embedFilterPatterns = []string{
	"find all records where column is X",
	"show items where field equals X",
	"filter records where attribute is X",
	"look up entries matching X",
	"where field equals X",
	"only include rows matching X",
	"search for records with X in field",
	"filter by column is X",
	"select entries where property is X",
	"find rows where column equals X",
	"rows matching X in column",
	"where field is X",
}

var embedDistinctPatterns = []string{
	"list distinct values for each group",
	"distinct values for column",
	"list unique items per category",
	"comma-separated distinct values",
	"unique values for each group",
	"distinct values in column",
}

var embedDescPatterns = []string{
	"top results ranked highest",
	"most frequent first",
	"sorted by count descending",
	"largest values first",
	"highest to lowest",
	"ranked from most to least",
}

var embedAscPatterns = []string{
	"lowest values first",
	"sorted ascending order",
	"smallest to largest",
	"least to most",
	"fewest first",
	"earliest to latest",
}

var embedLimitPatterns = []string{
	"top five results only",
	"first ten items",
	"show only three results",
	"limit to a specific number of results",
	"return the top few ranked",
	"best twenty records",
}

var embedRatioPatterns = []string{
	"percentage of total",
	"percent share by category",
	"ratio of leads",
	"proportion across groups",
	"breakdown with percentages",
	"share of total records",
	"percentage breakdown",
}

// limitNumberRe extracts a number adjacent to limit keywords like "top", "first".
var limitNumberRe = regexp.MustCompile(`(?i)\b(?:top|first|bottom|last|best|worst|show|limit\s+(?:to)?)\s+(\d+)\b`)

// operationScores holds cosine similarity scores from embedding classification.
type operationScores struct {
	groupScore    float32
	filterScore   float32
	distinctScore float32
	ratioScore    float32
	descScore     float32
	ascScore      float32
	limitScore    float32
}

// ExtractQueryIntent uses embedding-based classification to extract structured
// query parameters from the goal text. No LLM inference needed — operations
// are detected by cosine similarity against pre-defined canonical patterns,
// and columns are resolved by literal matching against the schema.
//
// This replaces the previous multi-step GBNF extraction pipeline (Steps 1a-5)
// that used 5 sequential LLM inference calls. The embedding approach is:
//   - ~10ms (vs 5-25s for LLM inference)
//   - Deterministic (same input → same output)
//   - Zero hallucination risk (no generative model involved)
//   - Handles paraphrasing naturally via semantic similarity
//
// Key principle: the LLM never sees raw data values. Column names come from
// the schema. Filter values are extracted from the goal text only.
func ExtractQueryIntent(ctx context.Context, goal string, cacheId string) (*QueryIntent, error) {
	columns := cache.GetCacheColumns(cacheId)
	if len(columns) == 0 {
		return nil, fmt.Errorf("no columns available for cacheId %s", cacheId)
	}

	// Fetch sample values for grounding candidate filter values
	sampleValues := cache.GetCacheSampleValues(cacheId, columns, 15)

	intent := &QueryIntent{}

	// Split goal into phrases for independent embedding comparison (strip meta parentheticals first)
	cleanGoal := stripMetaParentheticals(goal)
	phrases := splitGoalIntoPhrases(cleanGoal)
	if len(phrases) == 0 {
		phrases = []string{goal}
	}

	// Classify operations via embedding similarity.
	scores := classifyOpsViaEmbedding(ctx, phrases)

	fmt.Fprintf(os.Stderr, "[QueryIntent] Embedding scores: group=%.3f filter=%.3f distinct=%.3f desc=%.3f asc=%.3f limit=%.3f\n",
		scores.groupScore, scores.filterScore, scores.distinctScore, scores.descScore, scores.ascScore, scores.limitScore)

	// Thresholds for operation activation.
	const groupThreshold float32 = 0.40
	const filterThreshold float32 = 0.35
	const distinctThreshold float32 = 0.35
	const limitThreshold float32 = 0.45

	goalLower := strings.ToLower(goal)

	// --- GROUP BY ---
	if scores.groupScore >= groupThreshold || strings.Contains(goalLower, "for each") || strings.Contains(goalLower, "group by") || strings.Contains(goalLower, "breakdown by") {
		col := resolveColumnLiteral(goalLower, columns)
		if col != "" {
			intent.GroupColumn = col
			fmt.Fprintf(os.Stderr, "[QueryIntent] Embedding: GROUP BY %s (score=%.3f)\n", col, scores.groupScore)
		}
	}

	// --- FILTERS (Multi-Filter Phrase Scanning with Value Grounding) ---
	seenFilters := make(map[string]bool)
	for _, phrase := range phrases {
		phraseLower := strings.ToLower(phrase)
		col := resolveFilterColumnLiteral(phraseLower, columns, intent.GroupColumn)
		if col != "" && !seenFilters[strings.ToLower(col)] {
			val := extractLiteralValue(phrase, col)
			if val != "" && isValidFilterValue(val, col, sampleValues) {
				seenFilters[strings.ToLower(col)] = true
				clause := FilterClause{
					Column:   col,
					Operator: "=",
					Value:    val,
				}
				intent.Filters = append(intent.Filters, clause)
				if intent.FilterColumn == "" {
					intent.FilterColumn = col
					intent.FilterOperator = "="
					intent.FilterValue = val
				}
				fmt.Fprintf(os.Stderr, "[QueryIntent] Embedding: FILTER %s = %q (from phrase: %q)\n", col, val, phrase)
			}
		}
	}

	// Fallback to full goal filter scan if phrase scanning didn't match any filter
	if len(intent.Filters) == 0 && scores.filterScore >= filterThreshold {
		col := resolveFilterColumnLiteral(goalLower, columns, intent.GroupColumn)
		if col != "" {
			val := extractLiteralValue(cleanGoal, col)
			if val != "" && isValidFilterValue(val, col, sampleValues) {
				clause := FilterClause{
					Column:   col,
					Operator: "=",
					Value:    val,
				}
				intent.Filters = append(intent.Filters, clause)
				intent.FilterColumn = col
				intent.FilterOperator = "="
				intent.FilterValue = val
				fmt.Fprintf(os.Stderr, "[QueryIntent] Embedding fallback: FILTER %s = %q (score=%.3f)\n", col, val, scores.filterScore)
			}
		}
	}

	// --- DISTINCT / GROUP_CONCAT Aggregates ---
	if scores.distinctScore >= distinctThreshold || strings.Contains(goalLower, "distinct") || strings.Contains(goalLower, "unique") {
		distinctCol := resolveDistinctColumnLiteral(goalLower, columns, intent.GroupColumn, intent.FilterColumn)
		if distinctCol != "" {
			intent.AggExtras = append(intent.AggExtras, AggClause{
				Function: "GROUP_CONCAT",
				Column:   distinctCol,
				Distinct: true,
			})
			fmt.Fprintf(os.Stderr, "[QueryIntent] Embedding: AGGREGATE GROUP_CONCAT(DISTINCT %s) (score=%.3f)\n", distinctCol, scores.distinctScore)
		}
	}

	// --- RATIO / PERCENTAGE Aggregates ---
	const ratioThreshold float32 = 0.35
	if scores.ratioScore >= ratioThreshold || strings.Contains(goalLower, "percent") || strings.Contains(goalLower, "%") || strings.Contains(goalLower, "proportion") || strings.Contains(goalLower, "share") {
		intent.AggExtras = append(intent.AggExtras, AggClause{
			Function: "PERCENTAGE",
			Column:   "*",
		})
		fmt.Fprintf(os.Stderr, "[QueryIntent] Embedding: AGGREGATE PERCENTAGE(*) (score=%.3f)\n", scores.ratioScore)
	}

	// --- ORDER direction ---
	if intent.GroupColumn != "" || intent.FilterColumn != "" {
		if scores.descScore > scores.ascScore {
			intent.OrderDirection = "DESC"
		} else {
			intent.OrderDirection = "ASC"
		}
		intent.OrderColumn = intent.GroupColumn
		if intent.OrderColumn == "" {
			intent.OrderColumn = intent.FilterColumn
		}
		fmt.Fprintf(os.Stderr, "[QueryIntent] Embedding: ORDER BY %s %s (desc=%.3f asc=%.3f)\n",
			intent.OrderColumn, intent.OrderDirection, scores.descScore, scores.ascScore)
	}

	// Default primary aggregate to COUNT(*) if GROUP BY is active
	if intent.GroupColumn != "" && intent.AggFunction == "" {
		intent.AggFunction = "COUNT"
		intent.AggColumn = "*"
	}

	// --- LIMIT ---
	if scores.limitScore >= limitThreshold {
		n := extractLimitNumber(goal)
		if n > 0 {
			intent.Limit = n
			fmt.Fprintf(os.Stderr, "[QueryIntent] Embedding: LIMIT %d (score=%.3f)\n", n, scores.limitScore)
		}
	}

	fmt.Fprintf(os.Stderr, "[QueryIntent] Extracted: filter=%s %s %q, group=%s, agg=%s(%s), aggExtras=%v, order=%s %s, limit=%d, select=%v\n",
		intent.FilterColumn, intent.FilterOperator, intent.FilterValue,
		intent.GroupColumn,
		intent.AggFunction, intent.AggColumn,
		intent.AggExtras,
		intent.OrderColumn, intent.OrderDirection,
		intent.Limit,
		intent.SelectColumns)

	return intent, nil
}

// classifyOpsViaEmbedding batch-embeds goal phrases alongside canonical
// operation patterns and returns the max cosine similarity for each category.
// Falls back to bag-of-words similarity when the embedding sidecar is unavailable.
func classifyOpsViaEmbedding(ctx context.Context, phrases []string) operationScores {
	if !inference.GlobalEmbeddingSidecar.IsAvailable() {
		return classifyOpsViaBagOfWords(phrases)
	}

	// Batch embed everything in one call: [phrases..., patterns...]
	allTexts := make([]string, 0,
		len(phrases)+len(embedGroupPatterns)+len(embedFilterPatterns)+len(embedDistinctPatterns)+
			len(embedRatioPatterns)+len(embedDescPatterns)+len(embedAscPatterns)+len(embedLimitPatterns))

	allTexts = append(allTexts, phrases...)
	pEnd := len(phrases)

	allTexts = append(allTexts, embedGroupPatterns...)
	gEnd := pEnd + len(embedGroupPatterns)

	allTexts = append(allTexts, embedFilterPatterns...)
	fEnd := gEnd + len(embedFilterPatterns)

	allTexts = append(allTexts, embedDistinctPatterns...)
	distEnd := fEnd + len(embedDistinctPatterns)

	allTexts = append(allTexts, embedRatioPatterns...)
	rEnd := distEnd + len(embedRatioPatterns)

	allTexts = append(allTexts, embedDescPatterns...)
	dEnd := rEnd + len(embedDescPatterns)

	allTexts = append(allTexts, embedAscPatterns...)
	aEnd := dEnd + len(embedAscPatterns)

	allTexts = append(allTexts, embedLimitPatterns...)

	vecs, err := inference.GlobalEmbeddingSidecar.EmbedBatch(ctx, allTexts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[QueryIntent] Embedding failed, falling back to bag-of-words: %v\n", err)
		return classifyOpsViaBagOfWords(phrases)
	}

	if len(vecs) != len(allTexts) {
		fmt.Fprintf(os.Stderr, "[QueryIntent] Vector count mismatch: got %d, want %d\n", len(vecs), len(allTexts))
		return classifyOpsViaBagOfWords(phrases)
	}

	phraseVecs := vecs[:pEnd]
	groupVecs := vecs[pEnd:gEnd]
	filterVecs := vecs[gEnd:fEnd]
	distinctVecs := vecs[fEnd:distEnd]
	ratioVecs := vecs[distEnd:rEnd]
	descVecs := vecs[rEnd:dEnd]
	ascVecs := vecs[dEnd:aEnd]
	limitVecs := vecs[aEnd:]

	return operationScores{
		groupScore:    maxCategorySim(phraseVecs, groupVecs),
		filterScore:   maxCategorySim(phraseVecs, filterVecs),
		distinctScore: maxCategorySim(phraseVecs, distinctVecs),
		ratioScore:    maxCategorySim(phraseVecs, ratioVecs),
		descScore:     maxCategorySim(phraseVecs, descVecs),
		ascScore:      maxCategorySim(phraseVecs, ascVecs),
		limitScore:    maxCategorySim(phraseVecs, limitVecs),
	}
}

// maxCategorySim computes the maximum cosine similarity between any phrase
// vector and any pattern vector in a category.
func maxCategorySim(phraseVecs, patternVecs [][]float32) float32 {
	var best float32
	for _, pv := range phraseVecs {
		for _, cv := range patternVecs {
			sim := inference.GlobalEmbeddingSidecar.CosineSimilarity(pv, cv)
			if sim > best {
				best = sim
			}
		}
	}
	return best
}

// classifyOpsViaBagOfWords is a fallback when the neural embedding sidecar is
// unavailable. Uses the bag-of-words cosine similarity from the embeddings package.
func classifyOpsViaBagOfWords(phrases []string) operationScores {
	fmt.Fprintf(os.Stderr, "[QueryIntent] Using bag-of-words fallback for operation classification\n")

	bowMax := func(patterns []string) float32 {
		var best float64
		for _, p := range phrases {
			for _, pat := range patterns {
				score := embeddings.CosineSimilarity(p, pat)
				if score > best {
					best = score
				}
			}
		}
		return float32(best)
	}

	return operationScores{
		groupScore:    bowMax(embedGroupPatterns),
		filterScore:   bowMax(embedFilterPatterns),
		distinctScore: bowMax(embedDistinctPatterns),
		ratioScore:    bowMax(embedRatioPatterns),
		descScore:     bowMax(embedDescPatterns),
		ascScore:      bowMax(embedAscPatterns),
		limitScore:    bowMax(embedLimitPatterns),
	}
}

// resolveDistinctColumnLiteral finds a column name associated with distinct/unique
// aggregations in the goal text.
func resolveDistinctColumnLiteral(goalLower string, columns []string, excludeCols ...string) string {
	distinctKeywords := []string{"distinct", "unique", "list"}
	excludeMap := make(map[string]bool)
	for _, c := range excludeCols {
		if c != "" {
			excludeMap[strings.ToLower(c)] = true
		}
	}

	for _, kw := range distinctKeywords {
		idx := strings.Index(goalLower, kw)
		if idx < 0 {
			continue
		}
		window := goalLower[idx:]
		if len(window) > 100 {
			window = window[:100]
		}
		for _, col := range columns {
			if excludeMap[strings.ToLower(col)] {
				continue
			}
			colLower := strings.ToLower(col)
			if strings.Contains(window, colLower) {
				return col
			}
		}
	}
	return ""
}

// resolveColumnLiteral finds a column name that appears literally in the goal
// text (case-insensitive word boundary match). Returns the first match.
// This is the primary column resolution mechanism — embedding is for operation
// classification, literal matching is for column identification.
func resolveColumnLiteral(goalLower string, columns []string) string {
	// Prefer longer column names first (prevents "name" matching before "account_name")
	sorted := make([]string, len(columns))
	copy(sorted, columns)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if len(sorted[j]) > len(sorted[i]) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	paddedGoal := " " + goalLower + " "
	for _, col := range sorted {
		colLower := strings.ToLower(col)
		normCol := strings.TrimRight(normalizeColumnName(colLower), "_")
		// Word boundary check: " sector ", " country "
		if strings.Contains(paddedGoal, " "+colLower+" ") ||
			strings.Contains(paddedGoal, " "+colLower+",") ||
			strings.Contains(paddedGoal, " "+colLower+".") ||
			strings.Contains(paddedGoal, `"`+colLower+`"`) ||
			strings.Contains(paddedGoal, "'"+colLower+"'") {
			return col
		}
		// Also check with underscores replaced by spaces: "target account" → "Target_Account"
		spaced := strings.ReplaceAll(colLower, "_", " ")
		if spaced != colLower && strings.Contains(paddedGoal, " "+spaced+" ") {
			return col
		}
		// Normalized / fuzzy match (e.g. "accout owner" -> "Accout_Owner", "target_account?" -> "Target_Account_")
		if normCol != "" && normCol != colLower {
			normSpaced := strings.ReplaceAll(normCol, "_", " ")
			if strings.Contains(paddedGoal, " "+normCol+" ") || strings.Contains(paddedGoal, " "+normSpaced+" ") ||
				strings.Contains(paddedGoal, normCol) {
				return col
			}
		}
	}
	return ""
}

// resolveFilterColumnLiteral finds a column for filtering, excluding the
// group column. Looks for columns mentioned near filter keywords.
func resolveFilterColumnLiteral(goalLower string, columns []string, excludeCol string) string {
	filterKeywords := []string{"where ", "filter ", "equals ", "matching ", " is ", " = "}

	// First try: find a column near a filter keyword
	for _, kw := range filterKeywords {
		idx := strings.Index(goalLower, kw)
		if idx < 0 {
			continue
		}
		// Look for a column name within 50 chars after the keyword
		window := goalLower[idx:]
		if len(window) > 80 {
			window = window[:80]
		}
		paddedWindow := " " + window + " "
		for _, col := range columns {
			if strings.EqualFold(col, excludeCol) {
				continue
			}
			colLower := strings.ToLower(col)
			normCol := strings.TrimRight(normalizeColumnName(colLower), "_")
			if strings.Contains(paddedWindow, " "+colLower+" ") ||
				strings.Contains(paddedWindow, " "+colLower+"=") ||
				(normCol != "" && strings.Contains(paddedWindow, normCol)) {
				return col
			}
		}
	}

	// Fallback: any column mention that isn't the group column
	for _, col := range columns {
		if strings.EqualFold(col, excludeCol) {
			continue
		}
		colLower := strings.ToLower(col)
		normCol := strings.TrimRight(normalizeColumnName(colLower), "_")
		paddedGoal := " " + goalLower + " "
		if strings.Contains(paddedGoal, " "+colLower+" ") ||
			(normCol != "" && strings.Contains(paddedGoal, normCol)) {
			return col
		}
	}

	return ""
}

// extractLiteralValue extracts a filter value from the goal text by looking
// for quoted strings or values adjacent to the column name. No LLM involved —
// pure string extraction from the user's original text.
func extractLiteralValue(goal string, filterColumn string) string {
	goalLower := strings.ToLower(goal)
	colLower := strings.ToLower(filterColumn)
	normCol := strings.TrimRight(normalizeColumnName(colLower), "_")

	candidates := []string{colLower}
	if normCol != "" && normCol != colLower {
		candidates = append(candidates, normCol)
	}

	for _, cand := range candidates {
		// Pattern 1: quoted value after column mention
		// e.g., 'where Target_Account equals "Yes"' or "Country = 'USA'" or 'Target_Account? column equals "Yes"'
		quotedRe := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(cand) + `[^"'\s]*(?:\s+column)?\s*(?:equals|=|is|matches|like)\s*["']([^"']+)["']`)
		if m := quotedRe.FindStringSubmatch(goalLower); len(m) > 1 {
			return findOriginalCase(goal, m[1])
		}

		// Pattern 2: unquoted value after "column equals/is value"
		unquotedRe := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(cand) + `[^"'\s]*(?:\s+column)?\s*(?:equals|=|is|matches)\s+(\S+)`)
		if m := unquotedRe.FindStringSubmatch(goalLower); len(m) > 1 {
			val := strings.TrimRight(m[1], ".,;)")
			if val != "" && len(val) < 50 {
				return findOriginalCase(goal, val)
			}
		}
	}

	return ""
}

// findOriginalCase finds the original-cased version of a value in the goal text.
func findOriginalCase(goal string, lowerVal string) string {
	idx := strings.Index(strings.ToLower(goal), lowerVal)
	if idx >= 0 && idx+len(lowerVal) <= len(goal) {
		return goal[idx : idx+len(lowerVal)]
	}
	return lowerVal
}

// extractLimitNumber extracts a number from the goal text that appears next to
// limit keywords like "top", "first", "best". Returns 0 if no limit found.
func extractLimitNumber(goal string) int {
	m := limitNumberRe.FindStringSubmatch(goal)
	if len(m) > 1 {
		n, err := strconv.Atoi(m[1])
		if err == nil && n > 0 && n <= 1000 {
			return n
		}
	}
	return 0
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

	// Filters (Multi-filter support)
	if len(intent.Filters) > 0 {
		for _, f := range intent.Filters {
			if f.Column != "" && f.Value != "" {
				op := f.Operator
				if op == "" {
					op = "="
				}
				ops = append(ops, map[string]interface{}{
					"type":     "filter",
					"column":   f.Column,
					"operator": op,
					"value":    f.Value,
				})
			}
		}
	} else if intent.FilterColumn != "" && intent.FilterValue != "" {
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

	// Extra Aggregates (e.g. GROUP_CONCAT)
	// Extra Aggregates (e.g. GROUP_CONCAT, AVG, SUM, PERCENTAGE)
	var metricAggregateAlias string
	for _, extra := range intent.AggExtras {
		if extra.Column == "" {
			continue
		}
		extraOp := map[string]interface{}{
			"type":     "aggregate",
			"function": strings.ToUpper(extra.Function),
			"column":   extra.Column,
			"distinct": extra.Distinct,
		}
		var alias string
		funcUpper := strings.ToUpper(extra.Function)
		if funcUpper == "PERCENTAGE" || funcUpper == "RATIO" {
			alias = strings.ToLower(extra.Function)
		} else if extra.Distinct {
			alias = "distinct_" + strings.ToLower(extra.Column)
		} else {
			alias = strings.ToLower(extra.Function) + "_" + strings.ToLower(extra.Column)
		}
		extraOp["alias"] = alias
		ops = append(ops, extraOp)

		// Track scalar metric aggregates for order precedence
		if funcUpper == "AVG" || funcUpper == "SUM" || funcUpper == "MIN" || funcUpper == "MAX" {
			if metricAggregateAlias == "" {
				metricAggregateAlias = alias
			}
		}
	}

	// Order By
	if intent.OrderColumn != "" {
		dir := intent.OrderDirection
		if dir == "" {
			dir = "DESC"
		}
		orderCol := intent.OrderColumn
		// For GROUP BY + aggregate queries, sort by the aggregate result
		// (e.g., metric aggregate like "avg_deal_size" or "count") instead of group column.
		if intent.GroupColumn != "" && orderCol == intent.GroupColumn {
			if metricAggregateAlias != "" {
				orderCol = metricAggregateAlias
			} else if intent.AggFunction != "" {
				orderCol = strings.ToLower(intent.AggFunction)
			}
		}
		ops = append(ops, map[string]interface{}{
			"type":      "order_by",
			"column":    orderCol,
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

// stripMetaParentheticals removes explanatory parenthetical remarks from goal text
// (e.g., "(note the column name is misspelled)", "(the Accout_Owner column — note...")")
// so instructional meta-language does not pollute filter value extraction.
func stripMetaParentheticals(s string) string {
	re := regexp.MustCompile(`\([^)]*(?:note|misspell|column|ignore|case-insensitive|format|named|spelled)[^)]*\)`)
	return re.ReplaceAllString(s, " ")
}

// isValidFilterValue checks whether a candidate filter value is grounded in actual data.
// Rejects meta-instructions (e.g. "misspelled", "unspecified") and validates that
// string values match actual sample values in the target column when available.
func isValidFilterValue(val string, col string, sampleValues map[string][]string) bool {
	if val == "" {
		return false
	}
	valTrimmed := strings.TrimSpace(val)
	valLower := strings.ToLower(valTrimmed)

	// Explicit meta-instruction blacklist
	metaValues := map[string]bool{
		"misspelled":  true,
		"correct":     true,
		"null":        true,
		"empty":       true,
		"specified":   true,
		"missing":     true,
		"unknown":     true,
		"unspecified": true,
		"note":        true,
		"data":        true,
		"column":      true,
		"file":        true,
		"header":      true,
		"table":       true,
	}
	if metaValues[valLower] {
		// Only permit if it is literally an extant value in the column's sample set
		if samples, ok := sampleValues[col]; ok {
			for _, s := range samples {
				if strings.EqualFold(s, valTrimmed) {
					return true
				}
			}
		}
		return false
	}

	// If sample values exist for this column, verify candidate value is grounded
	if samples, ok := sampleValues[col]; ok && len(samples) > 0 {
		for _, s := range samples {
			if strings.EqualFold(s, valTrimmed) || strings.Contains(strings.ToLower(s), valLower) {
				return true
			}
		}
		// Allow standard numeric filter values (e.g. "100", "0", "42.5")
		if _, err := strconv.ParseFloat(valTrimmed, 64); err == nil {
			return true
		}
		// Allow date-like filter values (e.g. "2024", "2024-01-01")
		if len(valTrimmed) >= 4 && (strings.HasPrefix(valTrimmed, "20") || strings.HasPrefix(valTrimmed, "19")) {
			return true
		}
		return false
	}

	return true
}

