package executor

import (
	"strings"
	"testing"

	"tzro/internal/embeddings"
)

// --- Embedding-based QueryIntent extraction tests ---

// TestClassifyOpsViaBagOfWords_GroupingGoal verifies that a grouping goal
// scores high on group patterns and low on filter patterns.
func TestClassifyOpsViaBagOfWords_GroupingGoal(t *testing.T) {
	phrases := []string{"group leads by sector", "count each sector", "calculate percentage of total leads"}

	scores := classifyOpsViaBagOfWords(phrases)

	if scores.groupScore < 0.1 {
		t.Errorf("expected groupScore > 0.1 for grouping goal, got %.3f", scores.groupScore)
	}
	// Filter score should be lower than group score for a pure grouping goal
	if scores.filterScore > scores.groupScore {
		t.Errorf("expected filterScore (%.3f) < groupScore (%.3f) for grouping goal",
			scores.filterScore, scores.groupScore)
	}
}

// TestClassifyOpsViaBagOfWords_FilterGoal verifies that a filter goal
// scores high on filter patterns.
func TestClassifyOpsViaBagOfWords_FilterGoal(t *testing.T) {
	phrases := []string{"find leads where Target_Account equals Yes"}

	scores := classifyOpsViaBagOfWords(phrases)

	if scores.filterScore < 0.1 {
		t.Errorf("expected filterScore > 0.1 for filter goal, got %.3f", scores.filterScore)
	}
}

// TestResolveColumnLiteral_FindsSector verifies literal column matching.
func TestResolveColumnLiteral_FindsSector(t *testing.T) {
	columns := []string{"name", "email", "Sector", "Country", "Target_Account"}
	goal := "group leads by sector and count each"

	col := resolveColumnLiteral(goal, columns)

	if col != "Sector" {
		t.Errorf("expected Sector, got %q", col)
	}
}

// TestResolveColumnLiteral_PrefersLongerMatch verifies that longer column
// names are preferred over shorter ones (e.g., "account_name" over "name").
func TestResolveColumnLiteral_PrefersLongerMatch(t *testing.T) {
	columns := []string{"name", "account_name", "email"}
	goal := "find the account_name for each lead"

	col := resolveColumnLiteral(goal, columns)

	if col != "account_name" {
		t.Errorf("expected account_name (longer match), got %q", col)
	}
}

// TestResolveColumnLiteral_UnderscoreToSpace verifies that "target account"
// matches "Target_Account" via underscore-to-space normalization.
func TestResolveColumnLiteral_UnderscoreToSpace(t *testing.T) {
	columns := []string{"Target_Account", "Country", "Sector"}
	goal := "find leads where target account equals yes"

	col := resolveColumnLiteral(goal, columns)

	if col != "Target_Account" {
		t.Errorf("expected Target_Account, got %q", col)
	}
}

// TestExtractLiteralValue_QuotedValue verifies extraction of quoted filter values.
func TestExtractLiteralValue_QuotedValue(t *testing.T) {
	goal := `Find leads where Target_Account equals "Yes" and group by Country`

	val := extractLiteralValue(goal, "Target_Account")

	if val != "Yes" {
		t.Errorf("expected 'Yes', got %q", val)
	}
}

// TestExtractLiteralValue_UnquotedValue verifies extraction of unquoted filter values.
func TestExtractLiteralValue_UnquotedValue(t *testing.T) {
	goal := "Find leads where Country is USA"

	val := extractLiteralValue(goal, "Country")

	if val != "USA" {
		t.Errorf("expected 'USA', got %q", val)
	}
}

// TestExtractLiteralValue_NoValue verifies that no value is extracted
// when the goal doesn't mention a specific filter value for the column.
func TestExtractLiteralValue_NoValue(t *testing.T) {
	goal := "Group leads by Sector and count each"

	val := extractLiteralValue(goal, "Sector")

	if val != "" {
		t.Errorf("expected empty string for grouping-only goal, got %q", val)
	}
}

// TestExtractLimitNumber verifies limit number extraction from goal text.
func TestExtractLimitNumber_TopN(t *testing.T) {
	tests := []struct {
		goal string
		want int
	}{
		{"Show the top 5 countries by lead count", 5},
		{"first 10 results sorted by count", 10},
		{"best 3 sectors by revenue", 3},
		{"group by sector and show percentages", 0},
		{"count all leads by country", 0},
	}

	for _, tt := range tests {
		got := extractLimitNumber(tt.goal)
		if got != tt.want {
			t.Errorf("extractLimitNumber(%q) = %d, want %d", tt.goal, got, tt.want)
		}
	}
}

// TestEmbeddingsPackage_Integration verifies that the embeddings package
// is importable and the bag-of-words similarity function works.
func TestEmbeddingsPackage_Integration(t *testing.T) {
	sim := embeddings.CosineSimilarity("group by column", "group leads by sector")
	if sim < 0 || sim > 1.0 {
		t.Errorf("embeddings.CosineSimilarity returned out-of-range value: %.4f", sim)
	}
}

func TestResolveDistinctColumnLiteral(t *testing.T) {
	columns := []string{"Accout_Owner", "Lead_Source", "account_name", "Country"}
	goal := "For each unique Account Owner, count total leads and list the distinct Lead_Source values."

	col := resolveDistinctColumnLiteral(strings.ToLower(goal), columns, "Accout_Owner")
	if col != "Lead_Source" {
		t.Errorf("expected Lead_Source, got %q", col)
	}
}

func TestIntentToOperations_AggExtras(t *testing.T) {
	intent := &QueryIntent{
		GroupColumn: "Accout_Owner",
		AggFunction: "COUNT",
		AggColumn:   "*",
		AggExtras: []AggClause{
			{
				Function: "GROUP_CONCAT",
				Column:   "Lead_Source",
				Distinct: true,
			},
		},
	}

	ops := IntentToOperations(intent)
	if len(ops) < 3 {
		t.Fatalf("expected at least 3 operations (group_by, count, group_concat), got %d: %v", len(ops), ops)
	}

	foundDistinct := false
	for _, op := range ops {
		m, ok := op.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] == "aggregate" && m["function"] == "GROUP_CONCAT" && m["column"] == "Lead_Source" && m["distinct"] == true {
			foundDistinct = true
			break
		}
	}
	if !foundDistinct {
		t.Errorf("expected GROUP_CONCAT(DISTINCT Lead_Source) in ops, got %v", ops)
	}
}

func TestIntentToOperations_MultiFilter(t *testing.T) {
	intent := &QueryIntent{
		Filters: []FilterClause{
			{Column: "Country", Operator: "=", Value: "USA"},
			{Column: "Sector", Operator: "=", Value: "Healthcare"},
		},
		GroupColumn: "Lead_Source",
		AggFunction: "COUNT",
	}

	ops := IntentToOperations(intent)
	filterCount := 0
	for _, op := range ops {
		m, ok := op.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] == "filter" {
			filterCount++
		}
	}

	if filterCount != 2 {
		t.Errorf("expected 2 filter operations in IntentToOperations, got %d: %v", filterCount, ops)
	}
}

func TestIntentToOperations_CompoundAggregatesOrder(t *testing.T) {
	intent := &QueryIntent{
		GroupColumn: "Sector",
		AggFunction: "COUNT",
		AggColumn:   "*",
		AggExtras: []AggClause{
			{
				Function: "PERCENTAGE",
				Column:   "*",
			},
			{
				Function: "AVG",
				Column:   "Deal_Size",
			},
			{
				Function: "GROUP_CONCAT",
				Column:   "Company",
				Distinct: true,
			},
		},
		OrderColumn:    "Sector",
		OrderDirection: "DESC",
	}

	ops := IntentToOperations(intent)
	
	// Check percentage alias
	var percentageAlias, avgAlias, orderColumn string
	for _, op := range ops {
		m, ok := op.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] == "aggregate" {
			if m["function"] == "PERCENTAGE" {
				percentageAlias, _ = m["alias"].(string)
			}
			if m["function"] == "AVG" {
				avgAlias, _ = m["alias"].(string)
			}
		}
		if m["type"] == "order_by" {
			orderColumn, _ = m["column"].(string)
		}
	}

	if percentageAlias != "percentage" {
		t.Errorf("expected percentage alias 'percentage', got %q", percentageAlias)
	}
	if avgAlias != "avg_deal_size" {
		t.Errorf("expected avg alias 'avg_deal_size', got %q", avgAlias)
	}
	if orderColumn != "avg_deal_size" {
		t.Errorf("expected order by metric aggregate 'avg_deal_size', got %q", orderColumn)
	}
}


