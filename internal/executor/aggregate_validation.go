package executor

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
)

// Aggregate Row-Count Validation (FM-5)
//
// A deterministic post-query sanity check for Analyze Node Thought Chains.
// After sql_cached_data returns results from an aggregate query (GROUP BY,
// COUNT, SUM), compares the aggregated row counts against the known total.
// When the aggregate exceeds the base table total, returns a corrective
// feedback message for injection into the Thought Chain.

// aggregatePattern matches SQL queries that use aggregate functions or GROUP BY.
var aggregatePattern = regexp.MustCompile(`(?i)\b(GROUP\s+BY|COUNT\s*\(|SUM\s*\()`)

// ValidateAggregateRowCount checks whether the result of an aggregate SQL query
// has consistent row counts relative to the known base table total.
//
// Parameters:
//   - sql: the SQL query that was executed
//   - resultRows: the parsed JSON rows from sql_cached_data
//   - knownTotalRows: the total row count from introspect_cache (0 = unknown)
//
// Returns a corrective feedback message if the aggregate is inconsistent,
// or empty string if no issue is detected.
func ValidateAggregateRowCount(sql string, resultRows []map[string]interface{}, knownTotalRows int) string {
	// Only validate aggregate queries
	if !aggregatePattern.MatchString(sql) {
		return ""
	}

	// Need a known baseline to validate against
	if knownTotalRows <= 0 {
		return ""
	}

	// Sum up count-like values from the result rows. Look for common column
	// names that contain aggregate counts: count, count(*), total, cnt, etc.
	countColumnNames := []string{"count", "count(*)", "cnt", "total", "num", "count_1"}
	totalAggregate := 0
	foundCountColumn := false

	for _, row := range resultRows {
		for key, val := range row {
			keyLower := strings.ToLower(key)
			isCountCol := false
			for _, cn := range countColumnNames {
				if keyLower == cn || strings.Contains(keyLower, "count") {
					isCountCol = true
					break
				}
			}
			if !isCountCol {
				continue
			}

			foundCountColumn = true
			switch v := val.(type) {
			case float64:
				totalAggregate += int(math.Round(v))
			case int:
				totalAggregate += v
			case json.Number:
				if n, err := v.Int64(); err == nil {
					totalAggregate += int(n)
				}
			}
		}
	}

	if !foundCountColumn {
		return "" // No count columns found — can't validate
	}

	// Validate: aggregate total should not exceed the known base total
	if totalAggregate > knownTotalRows {
		msg := fmt.Sprintf(
			"WARNING: Your aggregate query returned a total count of %d, but the base table only has %d rows. "+
				"This indicates an error in your GROUP BY clause (likely joining on the wrong column or missing a WHERE clause). "+
				"Fix your SQL query and re-run.",
			totalAggregate, knownTotalRows,
		)
		fmt.Fprintf(os.Stderr, "[Probe] Aggregate row-count validation FAILED: aggregate=%d > total=%d\n",
			totalAggregate, knownTotalRows)
		return msg
	}

	return ""
}

// extractRowCountFromIntrospect attempts to extract a total row count from
// introspect_cache output. It looks for patterns like "X rows" or structured
// JSON with a "total_rows" field. Returns 0 if not found.
func extractRowCountFromIntrospect(introspectOutput string) int {
	// Try parsing as JSON with a totalRows or rowCount field
	var parsed map[string]interface{}
	if json.Unmarshal([]byte(introspectOutput), &parsed) == nil {
		for _, key := range []string{"totalRows", "total_rows", "rowCount", "row_count", "recordCount"} {
			if v, ok := parsed[key]; ok {
				switch n := v.(type) {
				case float64:
					return int(math.Round(n))
				case json.Number:
					if i, err := n.Int64(); err == nil {
						return int(i)
					}
				}
			}
		}
	}

	// Try regex patterns like "253 rows" or "Total: 253 records"
	rowCountPattern := regexp.MustCompile(`(\d+)\s+(?:rows|records|entries|items)`)
	if matches := rowCountPattern.FindStringSubmatch(introspectOutput); len(matches) > 1 {
		var count int
		if _, err := fmt.Sscanf(matches[1], "%d", &count); err == nil && count > 0 {
			return count
		}
	}

	return 0
}

// ExtractRowCountFromCountQuery extracts the row count from a SELECT COUNT(*)
// result. Returns 0 if the result doesn't contain a count value.
func ExtractRowCountFromCountQuery(sql string, resultRows []map[string]interface{}) int {
	// Only match COUNT(*) queries that aren't grouped
	sqlUpper := strings.ToUpper(sql)
	if !strings.Contains(sqlUpper, "COUNT(*)") {
		return 0
	}
	if strings.Contains(sqlUpper, "GROUP BY") {
		return 0 // Grouped counts aren't base totals
	}

	// Extract the count value from the single result row
	if len(resultRows) != 1 {
		return 0
	}

	for _, val := range resultRows[0] {
		switch v := val.(type) {
		case float64:
			return int(math.Round(v))
		case json.Number:
			if n, err := v.Int64(); err == nil {
				return int(n)
			}
		}
	}

	return 0
}
