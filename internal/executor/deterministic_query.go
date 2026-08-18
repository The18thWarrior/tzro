package executor

// deterministic_query.go — Confidence-gated deterministic query path for
// Analyze Nodes. Extracts structured intent from per-phrase regex scanning,
// scores extraction confidence, and routes between deterministic SQL execution
// and the existing Thought Chain fallback.
//
// ADR-0076: Deterministic Query Path.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tzro/internal/cache"
	"tzro/internal/embeddings"
	"tzro/internal/tools"
)

// FilterClause represents a single WHERE condition with column, operator, and value.
type FilterClause struct {
	Column   string `json:"column"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

// AggClause represents an additional aggregate function (beyond the primary AggFunction).
type AggClause struct {
	Function string `json:"function"`
	Column   string `json:"column"`
	Distinct bool   `json:"distinct,omitempty"`
}

// RegexIntentMatch is a single intent signal extracted by the per-phrase regex scanner.
type RegexIntentMatch struct {
	Type      string // "filter", "group_by", "aggregate", "order"
	Column    string // raw extracted column text
	Value     string // for filters
	Operator  string // for filters
	Function  string // for aggregates: "COUNT", "GROUP_CONCAT"
	Distinct  bool   // for aggregates
	Direction string // for order
	Phrase    string // source phrase for debugging
}

// QueryConfidence scores how reliably a QueryIntent was extracted.
type QueryConfidence struct {
	Score     float64           // 0.0-1.0
	Archetype string            // "lookup", "aggregate", "filter_aggregate", "unknown"
	Gaps      []string          // missing fields
	Sources   map[string]string // field → "regex" | "model" | "embedding" | "default"
}

// ExtractDeterministicQueryIntent extracts structured QueryIntent using the neural
// embedding sidecar (with bag-of-words fallback) and computes its QueryConfidence.
func ExtractDeterministicQueryIntent(ctx context.Context, goal string, cacheID string) (*QueryIntent, QueryConfidence, error) {
	intent, err := ExtractQueryIntent(ctx, goal, cacheID)
	if err != nil {
		return nil, QueryConfidence{Archetype: "unknown", Score: 0.0}, err
	}

	sources := map[string]string{}
	if intent.GroupColumn != "" {
		sources["groupColumn"] = "embedding"
	}
	if intent.AggFunction != "" {
		sources["aggFunction"] = "embedding"
	}
	for _, f := range intent.Filters {
		sources["filter:"+f.Column] = "embedding"
	}
	if len(intent.Filters) == 0 && intent.FilterColumn != "" {
		sources["filter:"+intent.FilterColumn] = "embedding"
	}
	for _, agg := range intent.AggExtras {
		sources["aggExtra:"+agg.Column] = "embedding"
	}
	if intent.OrderDirection != "" {
		sources["orderDirection"] = "embedding"
	}

	confidence := ScoreIntent(intent, sources)
	return intent, confidence, nil
}

// IntentToQuerySpec converts a QueryIntent to a QuerySpec for BuildSQL.
// Supports multi-filter and extra aggregations.
func IntentToQuerySpec(intent *QueryIntent, cacheID string) tools.QuerySpec {
	var ops []tools.Operation

	// Filters (multi-filter support)
	for _, f := range intent.Filters {
		op := f.Operator
		if op == "" {
			op = "="
		}
		ops = append(ops, tools.Operation{
			Type:     "filter",
			Column:   f.Column,
			Operator: op,
			Value:    f.Value,
		})
	}

	// Legacy single-filter backward compatibility
	if len(intent.Filters) == 0 && intent.FilterColumn != "" && intent.FilterValue != "" {
		op := intent.FilterOperator
		if op == "" {
			op = "="
		}
		ops = append(ops, tools.Operation{
			Type:     "filter",
			Column:   intent.FilterColumn,
			Operator: op,
			Value:    intent.FilterValue,
		})
	}

	// Group By
	if intent.GroupColumn != "" {
		ops = append(ops, tools.Operation{
			Type:   "group_by",
			Column: intent.GroupColumn,
		})

		// Auto-inject COUNT(*) when group_by present but no aggregation specified
		if intent.AggFunction == "" {
			intent.AggFunction = "COUNT"
			intent.AggColumn = "*"
		}
	}

	// Primary Aggregate
	if intent.AggFunction != "" {
		aggCol := intent.AggColumn
		if aggCol == "" || aggCol == "*" {
			aggCol = "" // BuildSQL generates COUNT(*) when column is empty
		}
		ops = append(ops, tools.Operation{
			Type:     "aggregate",
			Function: strings.ToUpper(intent.AggFunction),
			Column:   aggCol,
			Alias:    strings.ToLower(intent.AggFunction),
		})
	}

	// Extra Aggregates (GROUP_CONCAT, AVG, PERCENTAGE, etc.)
	for _, agg := range intent.AggExtras {
		alias := strings.ToLower(agg.Function)
		if agg.Column != "" && agg.Column != "*" {
			alias += "_" + strings.ToLower(agg.Column)
		}
		if strings.ToUpper(agg.Function) == "PERCENTAGE" || strings.ToUpper(agg.Function) == "RATIO" {
			alias = "percentage"
		}
		ops = append(ops, tools.Operation{
			Type:     "aggregate",
			Function: strings.ToUpper(agg.Function),
			Column:   agg.Column,
			Distinct: agg.Distinct,
			Alias:    alias,
		})
	}

	// Order By
	if intent.OrderColumn != "" || intent.OrderDirection != "" {
		dir := intent.OrderDirection
		if dir == "" {
			dir = "DESC"
		}
		orderCol := intent.OrderColumn
		if orderCol == "" && intent.AggFunction != "" {
			// Default: order by the primary aggregate alias
			orderCol = strings.ToLower(intent.AggFunction)
		}
		if orderCol != "" {
			ops = append(ops, tools.Operation{
				Type:      "order_by",
				Column:    orderCol,
				Direction: dir,
			})
		}
	}

	// Select specific columns
	if len(intent.SelectColumns) > 0 {
		var validCols []string
		for _, c := range intent.SelectColumns {
			if c != "" {
				validCols = append(validCols, c)
			}
		}
		if len(validCols) > 0 {
			ops = append(ops, tools.Operation{
				Type:    "select",
				Columns: validCols,
			})
		}
	}

	return tools.QuerySpec{
		CacheID:    cacheID,
		Operations: ops,
		Limit:      100,
	}
}

// ScoreIntent computes the Query Confidence for a populated QueryIntent.
func ScoreIntent(intent *QueryIntent, sources map[string]string) QueryConfidence {
	qc := QueryConfidence{
		Sources: sources,
	}

	if intent == nil {
		qc.Archetype = "unknown"
		return qc
	}

	// Detect archetype
	hasFilter := len(intent.Filters) > 0 || (intent.FilterColumn != "" && intent.FilterValue != "")
	hasGroup := intent.GroupColumn != ""
	hasSelect := len(intent.SelectColumns) > 0

	switch {
	case hasFilter && hasGroup:
		qc.Archetype = "filter_aggregate"
	case hasGroup:
		qc.Archetype = "aggregate"
	case hasFilter && hasSelect:
		qc.Archetype = "lookup"
	case hasFilter:
		qc.Archetype = "lookup"
	default:
		qc.Archetype = "unknown"
	}

	// Score from sources
	for field, source := range sources {
		switch source {
		case "regex":
			qc.Score += 0.25
		case "model":
			qc.Score += 0.15
		case "embedding":
			qc.Score += 0.20
		case "default":
			qc.Score += 0.10
		}
		// Embedding-resolved columns get a slight penalty
		if source == "embedding" && (strings.HasPrefix(field, "groupColumn") || strings.HasPrefix(field, "filter:")) {
			qc.Score -= 0.05
		}
	}

	// Check for required fields per archetype
	switch qc.Archetype {
	case "lookup":
		if !hasFilter {
			qc.Gaps = append(qc.Gaps, "no filter detected")
			qc.Score -= 0.30
		}
		if !hasSelect {
			qc.Gaps = append(qc.Gaps, "no selectColumns detected")
			// Don't penalize as hard — lookup without select still works (returns all columns)
		}
	case "aggregate":
		if !hasGroup {
			qc.Gaps = append(qc.Gaps, "no groupColumn detected")
			qc.Score -= 0.30
		}
	case "filter_aggregate":
		if !hasFilter {
			qc.Gaps = append(qc.Gaps, "no filter detected")
			qc.Score -= 0.30
		}
		if !hasGroup {
			qc.Gaps = append(qc.Gaps, "no groupColumn detected")
			qc.Score -= 0.30
		}
	case "unknown":
		if qc.Score > 0.40 {
			qc.Score = 0.40
		}
	}

	// Clamp
	if qc.Score < 0 {
		qc.Score = 0
	}
	if qc.Score > 1.0 {
		qc.Score = 1.0
	}

	return qc
}

// formatIntentHint produces a structured text hint for the warm thought chain.
func formatIntentHint(intent *QueryIntent, confidence QueryConfidence) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[PRE-EXTRACTED INTENT (partial, confidence=%.2f)]\n", confidence.Score))

	if intent.GroupColumn != "" {
		source := confidence.Sources["groupColumn"]
		sb.WriteString(fmt.Sprintf("- groupColumn: %q (source: %s)\n", intent.GroupColumn, source))
	}
	if intent.AggFunction != "" {
		source := confidence.Sources["aggFunction"]
		sb.WriteString(fmt.Sprintf("- aggFunction: %s(%s) (source: %s)\n", intent.AggFunction, intent.AggColumn, source))
	}
	for _, f := range intent.Filters {
		source := confidence.Sources["filter:"+f.Column]
		sb.WriteString(fmt.Sprintf("- filter: [%s] %s '%s' (source: %s)\n", f.Column, f.Operator, f.Value, source))
	}
	for _, agg := range intent.AggExtras {
		sb.WriteString(fmt.Sprintf("- extra agg: %s(%s) distinct=%v\n", agg.Function, agg.Column, agg.Distinct))
	}
	if intent.OrderDirection != "" {
		sb.WriteString(fmt.Sprintf("- orderDirection: %s\n", intent.OrderDirection))
	}
	if len(confidence.Gaps) > 0 {
		sb.WriteString(fmt.Sprintf("- Gaps: %s\n", strings.Join(confidence.Gaps, ", ")))
	}

	return sb.String()
}

// resolveColumnsWithEmbedding batch-resolves unresolved column texts against
// schema columns using neural embedding similarity.
//
// For each unresolved text, embeds it alongside all schema columns, computes
// cosine similarity, and returns the best match above threshold.
//
// Accepts an EmbeddingEngine parameter for testability — production code
// passes inference.GlobalEmbeddingSidecar.
//
// ADR-0076: Deterministic Query Path — Tier 2 column resolution.
func resolveColumnsWithEmbedding(ctx context.Context, engine embeddings.EmbeddingEngine, unresolvedTexts []string, columns []string, threshold float64) map[string]string {
	if engine == nil {
		return map[string]string{}
	}
	if len(unresolvedTexts) == 0 || len(columns) == 0 {
		return map[string]string{}
	}

	resolved := map[string]string{}

	// Embed all unresolved texts
	unresolvedVecs := make([][]float32, len(unresolvedTexts))
	for i, text := range unresolvedTexts {
		vec, err := engine.Embed(ctx, text)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[resolveColumnsWithEmbedding] Failed to embed %q: %v\n", text, err)
			continue
		}
		unresolvedVecs[i] = vec
	}

	// Embed all column names
	colVecs := make([][]float32, len(columns))
	for i, col := range columns {
		vec, err := engine.Embed(ctx, col)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[resolveColumnsWithEmbedding] Failed to embed column %q: %v\n", col, err)
			continue
		}
		colVecs[i] = vec
	}

	// For each unresolved text, find the best matching column
	for i, text := range unresolvedTexts {
		if unresolvedVecs[i] == nil {
			continue
		}

		var bestCol string
		var bestSim float32
		for j, col := range columns {
			if colVecs[j] == nil {
				continue
			}
			sim := engine.CosineSimilarity(unresolvedVecs[i], colVecs[j])
			if sim > bestSim {
				bestSim = sim
				bestCol = col
			}
		}

		if bestCol != "" && float64(bestSim) >= threshold {
			resolved[text] = bestCol
			fmt.Fprintf(os.Stderr, "[resolveColumnsWithEmbedding] Resolved %q → %q (similarity=%.3f)\n",
				text, bestCol, bestSim)
		} else {
			fmt.Fprintf(os.Stderr, "[resolveColumnsWithEmbedding] No match for %q above threshold %.2f (best=%.3f for %q)\n",
				text, threshold, bestSim, bestCol)
		}
	}

	return resolved
}

// executeDeterministicQuery runs the compiled SQL path for a high-confidence intent.
// Returns (result string, demoted bool, error).
// If demoted=true, the caller should fall through to the warm thought chain path.
//
// The function:
// 1. Converts intent to QuerySpec via IntentToQuerySpec
// 2. Builds SQL via BuildSQL
// 3. Executes via cache.ExecuteSQL
// 4. Validates: empty filter results → demote to warm path
// 5. Materializes derived tables for GROUP BY results
//
// ADR-0076: Deterministic Query Path.
func executeDeterministicQuery(ctx context.Context, intent *QueryIntent, cacheID string) (string, bool, error) {
	if intent == nil {
		return "", true, fmt.Errorf("nil intent")
	}

	spec := IntentToQuerySpec(intent, cacheID)
	sql, err := tools.BuildSQL(spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[DeterministicQuery] BuildSQL failed: %v\n", err)
		return "", true, nil // Demote — don't hard-fail
	}

	fmt.Fprintf(os.Stderr, "[DeterministicQuery] Executing SQL: %s\n", sql)

	result, err := cache.ExecuteSQL(ctx, cacheID, sql)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[DeterministicQuery] ExecuteSQL failed: %v\n", err)
		return "", true, nil // Demote — SQL error
	}

	// Post-execution validation: empty results from filter queries → demote
	hasFilter := len(intent.Filters) > 0 || (intent.FilterColumn != "" && intent.FilterValue != "")
	if hasFilter && isEmptyResult(result) {
		fmt.Fprintf(os.Stderr, "[DeterministicQuery] Empty filter result → demoting to warm path\n")
		return result, true, nil
	}

	// Materialize derived table for GROUP BY results
	hasGroupBy := intent.GroupColumn != ""
	if hasGroupBy && !isEmptyResult(result) {
		derivedID, mErr := cache.MaterializeDerivedTable(cacheID, sql, result, "")
		if mErr == nil {
			fmt.Fprintf(os.Stderr, "[DeterministicQuery] Materialized derived table: %s\n", derivedID)
		} else {
			fmt.Fprintf(os.Stderr, "[DeterministicQuery] Derived table warning (non-fatal): %v\n", mErr)
		}
	}

	fmt.Fprintf(os.Stderr, "[DeterministicQuery] Success — %d bytes of results\n", len(result))
	return result, false, nil
}

// isEmptyResult checks if an SQL result represents an empty/no-data response.
func isEmptyResult(result string) bool {
	result = strings.TrimSpace(result)
	if result == "" || result == "[]" || result == "null" {
		return true
	}
	// Check for "0 rows" indicator
	if strings.Contains(result, "\"rows\":0") || strings.Contains(result, "\"rows\": 0") {
		return true
	}
	// Count JSON objects — if result is a JSON array with no objects
	if strings.HasPrefix(result, "[") && strings.Count(result, "{") == 0 {
		return true
	}
	return false
}


