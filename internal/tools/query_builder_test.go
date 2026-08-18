package tools

import (
	"strings"
	"testing"
)

// --- Slice 1 RED: query_builder SQL assembly tests ---

func TestBuildSQL_SingleFilter(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "filter", Column: "account_name", Operator: "=", Value: "Walmart"},
		},
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	// Should produce: SELECT * FROM [cache_1234567890] WHERE [account_name] = 'Walmart' LIMIT 100
	if !strings.Contains(sql, "WHERE [account_name] = 'Walmart'") {
		t.Errorf("expected WHERE clause, got: %s", sql)
	}
	if !strings.Contains(sql, "FROM [cache_1234567890]") {
		t.Errorf("expected FROM with bracket-escaped cache ID, got: %s", sql)
	}
	if !strings.Contains(sql, "LIMIT 100") {
		t.Errorf("expected default LIMIT 100, got: %s", sql)
	}
}

func TestBuildSQL_GroupByWithCount(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "group_by", Column: "Country"},
			{Type: "aggregate", Function: "COUNT", Alias: "lead_count"},
			{Type: "order_by", Column: "lead_count", Direction: "DESC"},
		},
		Limit: 5,
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	if !strings.Contains(sql, "GROUP BY COALESCE(NULLIF([Country], ''), '(Unspecified)')") {
		t.Errorf("expected GROUP BY clause, got: %s", sql)
	}
	if !strings.Contains(sql, "COUNT(*)") {
		t.Errorf("expected COUNT(*) aggregate, got: %s", sql)
	}
	if !strings.Contains(sql, "AS lead_count") {
		t.Errorf("expected alias, got: %s", sql)
	}
	if !strings.Contains(sql, "ORDER BY lead_count DESC") {
		t.Errorf("expected ORDER BY DESC, got: %s", sql)
	}
	if !strings.Contains(sql, "LIMIT 5") {
		t.Errorf("expected LIMIT 5, got: %s", sql)
	}
}

func TestBuildSQL_PercentageWindowFunction(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "group_by", Column: "Sector"},
			{Type: "aggregate", Function: "COUNT", Alias: "lead_count"},
			{Type: "aggregate", Function: "PERCENTAGE", Alias: "pct_of_total"},
			{Type: "order_by", Column: "lead_count", Direction: "DESC"},
		},
		Limit: 10,
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	if !strings.Contains(sql, "ROUND(COUNT(*) * 100.0 / SUM(COUNT(*)) OVER(), 2) AS pct_of_total") {
		t.Errorf("expected window percentage expression, got: %s", sql)
	}
	if !strings.Contains(sql, "GROUP BY COALESCE(NULLIF([Sector], ''), '(Unspecified)')") {
		t.Errorf("expected imputed group by, got: %s", sql)
	}
}

func TestBuildSQL_ComplexComposite(t *testing.T) {
	// lead_target_account_analysis: filter + group_by + COUNT + GROUP_CONCAT(DISTINCT) + order_by
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "filter", Column: "Target_Account?", Operator: "=", Value: "Yes"},
			{Type: "group_by", Column: "Primary_Incumbent_CDN"},
			{Type: "aggregate", Function: "COUNT", Alias: "lead_count"},
			{Type: "aggregate", Function: "GROUP_CONCAT", Column: "account_name", Distinct: true, Alias: "companies"},
			{Type: "order_by", Column: "lead_count", Direction: "DESC"},
		},
		Limit: 100,
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	// Check WHERE with special-char column name
	if !strings.Contains(sql, "WHERE [Target_Account?] = 'Yes'") {
		t.Errorf("expected WHERE with bracket-escaped special column, got: %s", sql)
	}
	if !strings.Contains(sql, "GROUP BY COALESCE(NULLIF([Primary_Incumbent_CDN], ''), '(Unspecified)')") {
		t.Errorf("expected GROUP BY, got: %s", sql)
	}
	if !strings.Contains(sql, "COUNT(*)") {
		t.Errorf("expected COUNT(*), got: %s", sql)
	}
	if !strings.Contains(sql, "GROUP_CONCAT(DISTINCT [account_name])") {
		t.Errorf("expected GROUP_CONCAT(DISTINCT ...), got: %s", sql)
	}
	if !strings.Contains(sql, "ORDER BY lead_count DESC") {
		t.Errorf("expected ORDER BY, got: %s", sql)
	}
}

func TestBuildSQL_FilterWithLIKE(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "filter", Column: "account_name", Operator: "LIKE", Value: "%Walmart%"},
		},
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	if !strings.Contains(sql, "WHERE [account_name] LIKE '%Walmart%'") {
		t.Errorf("expected LIKE clause, got: %s", sql)
	}
}

func TestBuildSQL_SelectSpecificColumns(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "select", Columns: []string{"name", "email"}},
			{Type: "filter", Column: "account_name", Operator: "=", Value: "Walmart"},
		},
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	if !strings.Contains(sql, "SELECT [name], [email]") {
		t.Errorf("expected specific column selection, got: %s", sql)
	}
}

func TestBuildSQL_EmptyOperations(t *testing.T) {
	spec := QuerySpec{
		CacheID:    "cache_1234567890",
		Operations: []Operation{},
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	if !strings.Contains(sql, "SELECT * FROM [cache_1234567890] LIMIT 100") {
		t.Errorf("expected SELECT * with default LIMIT, got: %s", sql)
	}
}

func TestBuildSQL_EmptyCacheID(t *testing.T) {
	spec := QuerySpec{
		CacheID:    "",
		Operations: []Operation{},
	}

	_, err := BuildSQL(spec)
	if err == nil {
		t.Error("expected error for empty cacheId")
	}
}

func TestBuildSQL_InvalidOperator(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "filter", Column: "name", Operator: "DROP TABLE", Value: "x"},
		},
	}

	_, err := BuildSQL(spec)
	if err == nil {
		t.Error("expected error for invalid operator")
	}
}

func TestBuildSQL_InvalidAggregateFunction(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "aggregate", Function: "EXEC", Column: "x"},
		},
	}

	_, err := BuildSQL(spec)
	if err == nil {
		t.Error("expected error for invalid aggregate function")
	}
}

func TestBuildSQL_MultipleFilters(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "filter", Column: "Country", Operator: "=", Value: "USA"},
			{Type: "filter", Column: "Sector", Operator: "=", Value: "eCommerce"},
		},
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	if !strings.Contains(sql, "WHERE [Country] = 'USA' AND [Sector] = 'eCommerce'") {
		t.Errorf("expected AND-joined filters, got: %s", sql)
	}
}

func TestBuildSQL_OrderByDefaultDirection(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "order_by", Column: "name"},
		},
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	if !strings.Contains(sql, "ORDER BY name DESC") {
		t.Errorf("expected default DESC direction, got: %s", sql)
	}
}

func TestBuildSQL_AggregateWithoutAlias(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "group_by", Column: "Country"},
			{Type: "aggregate", Function: "COUNT"},
		},
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	// Should produce COUNT(*) without AS clause
	if !strings.Contains(sql, "COUNT(*)") {
		t.Errorf("expected COUNT(*), got: %s", sql)
	}
	if strings.Contains(sql, "COUNT(*) AS") {
		t.Errorf("expected no alias for aggregate when not specified, got: %s", sql)
	}
}

func TestBuildSQL_IsNullFilter(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "filter", Column: "Sector", Operator: "IS NULL"},
		},
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	if !strings.Contains(sql, "WHERE [Sector] IS NULL") {
		t.Errorf("expected IS NULL, got: %s", sql)
	}
}

func TestBuildSQL_CustomLimit(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "filter", Column: "name", Operator: "=", Value: "test"},
		},
		Limit: 25,
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	if !strings.Contains(sql, "LIMIT 25") {
		t.Errorf("expected LIMIT 25, got: %s", sql)
	}
}

func TestBuildSQL_SQLInjectionPrevention(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "filter", Column: "name", Operator: "=", Value: "'; DROP TABLE users; --"},
		},
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	// Value should be escaped (single quotes doubled)
	if !strings.Contains(sql, "''") {
		t.Errorf("expected escaped single quotes, got: %s", sql)
	}
}

func TestNewQueryBuilderTool_Schema(t *testing.T) {
	// Verify the registered tool has a valid schema with required fields
	tool := NewQueryBuilderTool()
	schema, err := tool.GetSchema()
	if err != nil {
		t.Fatalf("GetSchema failed: %v", err)
	}

	if !strings.Contains(schema, "cacheId") {
		t.Error("schema missing cacheId")
	}
	if !strings.Contains(schema, "operations") {
		t.Error("schema missing operations")
	}
}

func TestBuildSQL_GroupByCoalesceImputation(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "group_by", Column: "Primary_Incumbent_CDN"},
			{Type: "aggregate", Function: "COUNT", Alias: "target_lead_count"},
			{Type: "order_by", Column: "target_lead_count", Direction: "DESC"},
		},
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	// Grouping columns should have COALESCE(NULLIF(col, ''), '(Unspecified)') imputation
	expectedSelect := "COALESCE(NULLIF([Primary_Incumbent_CDN], ''), '(Unspecified)') AS [Primary_Incumbent_CDN]"
	if !strings.Contains(sql, expectedSelect) {
		t.Errorf("expected SELECT clause to contain imputed column %q, got: %s", expectedSelect, sql)
	}

	expectedGroupBy := "GROUP BY COALESCE(NULLIF([Primary_Incumbent_CDN], ''), '(Unspecified)')"
	if !strings.Contains(sql, expectedGroupBy) {
		t.Errorf("expected GROUP BY clause %q, got: %s", expectedGroupBy, sql)
	}
}

// --- Slice 1 RED (Run 32 fix): COUNT(*) wildcard must not produce COUNT([*]) ---

// TestBuildSQL_CountStar_NoColumnBrackets verifies that a COUNT(*) aggregate
// is emitted as COUNT(*) and not COUNT([*]), which SQLite rejects with
// "no such column: *".
func TestBuildSQL_CountStar_NoColumnBrackets(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "group_by", Column: "Country"},
			{Type: "aggregate", Function: "COUNT", Column: "*", Alias: "count"},
		},
	}
	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}
	if strings.Contains(sql, "COUNT([*])") {
		t.Errorf("COUNT([*]) must not appear in SQL — SQLite rejects it. Got: %s", sql)
	}
	if !strings.Contains(sql, "COUNT(*)") {
		t.Errorf("expected COUNT(*) in SQL, got: %s", sql)
	}
}

func TestBuildSQL_MultiFilter_MultiAggregate(t *testing.T) {
	spec := QuerySpec{
		CacheID: "cache_1234567890",
		Operations: []Operation{
			{Type: "filter", Column: "Country", Operator: "=", Value: "USA"},
			{Type: "filter", Column: "Target_Account", Operator: "=", Value: "Yes"},
			{Type: "group_by", Column: "Sector"},
			{Type: "aggregate", Function: "COUNT", Column: "*", Alias: "lead_count"},
			{Type: "aggregate", Function: "PERCENTAGE", Column: "*", Alias: "percentage"},
			{Type: "aggregate", Function: "AVG", Column: "Deal_Size", Alias: "avg_deal_size"},
			{Type: "aggregate", Function: "GROUP_CONCAT", Column: "Company", Distinct: true, Alias: "distinct_companies"},
			{Type: "order_by", Column: "avg_deal_size", Direction: "DESC"},
		},
		Limit: 25,
	}

	sql, err := BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	// Verify multi-filter AND joining
	if !strings.Contains(sql, "WHERE [Country] = 'USA' AND [Target_Account] = 'Yes'") {
		t.Errorf("expected multi-filter WHERE clause, got: %s", sql)
	}

	// Verify multi-aggregate SELECT list
	if !strings.Contains(sql, "COUNT(*) AS lead_count") {
		t.Errorf("expected COUNT(*) in SELECT, got: %s", sql)
	}
	if !strings.Contains(sql, "ROUND(COUNT(*) * 100.0 / SUM(COUNT(*)) OVER(), 2) AS percentage") {
		t.Errorf("expected percentage window expression, got: %s", sql)
	}
	if !strings.Contains(sql, "AVG([Deal_Size]) AS avg_deal_size") {
		t.Errorf("expected AVG([Deal_Size]) in SELECT, got: %s", sql)
	}
	if !strings.Contains(sql, "GROUP_CONCAT(DISTINCT [Company]) AS distinct_companies") {
		t.Errorf("expected GROUP_CONCAT(DISTINCT [Company]), got: %s", sql)
	}

	// Verify grouping and ordering
	if !strings.Contains(sql, "GROUP BY COALESCE(NULLIF([Sector], ''), '(Unspecified)')") {
		t.Errorf("expected imputed group by, got: %s", sql)
	}
	if !strings.Contains(sql, "ORDER BY avg_deal_size DESC") {
		t.Errorf("expected ORDER BY avg_deal_size DESC, got: %s", sql)
	}
	if !strings.Contains(sql, "LIMIT 25") {
		t.Errorf("expected LIMIT 25, got: %s", sql)
	}
}


