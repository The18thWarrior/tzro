package executor

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"tzro/internal/cache"
	"tzro/internal/tools"

	_ "modernc.org/sqlite"
)

// --- Test helpers for ephemeral query DB ---

func setupTestQueryDB(t *testing.T) func() {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory query DB: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS _cache_tables (
		table_name TEXT PRIMARY KEY,
		task_id TEXT,
		created_at INTEGER
	)`)
	if err != nil {
		t.Fatalf("failed to create metadata table: %v", err)
	}
	cache.SetQueryDBForTesting(db)
	return func() {
		db.Close()
		cache.SetQueryDBForTesting(nil)
	}
}

func materializeTestData(t *testing.T, cacheID string) {
	t.Helper()
	rawPayload := `[
		{"Name": "Alice", "Age": 30, "Sector": "Tech"},
		{"Name": "Bob", "Age": 25, "Sector": "Finance"},
		{"Name": "Charlie", "Age": 35, "Sector": "Tech"},
		{"Name": "Diana", "Age": 28, "Sector": "Health"},
		{"Name": "Eve", "Age": 32, "Sector": "Finance"}
	]`
	columnTypes := map[string]string{
		"Name":   "TEXT",
		"Age":    "INTEGER",
		"Sector": "TEXT",
	}
	if err := cache.MaterializeTable(cacheID, rawPayload, columnTypes, "test_task"); err != nil {
		t.Fatalf("MaterializeTable failed: %v", err)
	}
}

// =======================================
// Slice 3: Multi-filter QueryIntent + IntentToQuerySpec (ADR-0076)
// =======================================

func TestIntentToQuerySpec_FilterAndGroupBy(t *testing.T) {
	intent := &QueryIntent{
		Filters: []FilterClause{
			{Column: "account_name", Operator: "=", Value: "Walmart"},
		},
		GroupColumn: "Country",
		AggFunction: "COUNT",
		AggColumn:   "*",
	}

	spec := IntentToQuerySpec(intent, "cache_123")
	if spec.CacheID != "cache_123" {
		t.Errorf("expected cacheID=cache_123, got %s", spec.CacheID)
	}

	sql, err := tools.BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	// Should have WHERE + GROUP BY + COUNT
	if !sqlContains(sql, "WHERE") {
		t.Errorf("expected WHERE clause, got: %s", sql)
	}
	if !sqlContains(sql, "GROUP BY") {
		t.Errorf("expected GROUP BY clause, got: %s", sql)
	}
	if !sqlContains(sql, "COUNT") {
		t.Errorf("expected COUNT aggregate, got: %s", sql)
	}
}

func TestIntentToQuerySpec_MultiFilter(t *testing.T) {
	intent := &QueryIntent{
		Filters: []FilterClause{
			{Column: "Target_Account_", Operator: "=", Value: "Yes"},
			{Column: "Country", Operator: "=", Value: "USA"},
		},
		GroupColumn: "Sector",
		AggFunction: "COUNT",
		AggColumn:   "*",
	}

	spec := IntentToQuerySpec(intent, "cache_multi")
	sql, err := tools.BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	// Should have compound WHERE with AND
	if !sqlContains(sql, "Target_Account_") || !sqlContains(sql, "Country") {
		t.Errorf("expected both filter columns in SQL, got: %s", sql)
	}
	if !sqlContains(sql, "AND") {
		t.Errorf("expected AND in compound WHERE, got: %s", sql)
	}
}

func TestIntentToQuerySpec_GroupConcatDistinct(t *testing.T) {
	intent := &QueryIntent{
		GroupColumn: "Accout_Owner",
		AggFunction: "COUNT",
		AggColumn:   "*",
		AggExtras: []AggClause{
			{Function: "GROUP_CONCAT", Column: "Lead_Source", Distinct: true},
		},
	}

	spec := IntentToQuerySpec(intent, "cache_gc")
	sql, err := tools.BuildSQL(spec)
	if err != nil {
		t.Fatalf("BuildSQL failed: %v", err)
	}

	if !sqlContains(sql, "GROUP_CONCAT(DISTINCT") {
		t.Errorf("expected GROUP_CONCAT(DISTINCT ...) in SQL, got: %s", sql)
	}
}

func TestIntentToOperations_BackwardCompatible(t *testing.T) {
	// Existing single-filter intent (using legacy fields) should still produce correct output
	intent := &QueryIntent{
		FilterColumn:   "Country",
		FilterOperator: "=",
		FilterValue:    "USA",
		GroupColumn:    "Sector",
	}

	ops := IntentToOperations(intent)
	if len(ops) == 0 {
		t.Fatal("expected operations, got none")
	}

	// Should have at least filter + group_by + auto-injected COUNT
	hasFilter := false
	hasGroupBy := false
	for _, op := range ops {
		m, ok := op.(map[string]interface{})
		if !ok {
			continue
		}
		switch m["type"] {
		case "filter":
			hasFilter = true
		case "group_by":
			hasGroupBy = true
		}
	}
	if !hasFilter {
		t.Error("expected filter operation in backward-compatible output")
	}
	if !hasGroupBy {
		t.Error("expected group_by operation in backward-compatible output")
	}
}

// =======================================
// Slice 4: Unified Regex Intent Scanner (ADR-0076)
// =======================================

func TestRegexExtract_LeadLookup(t *testing.T) {
	goal := `Read the CSV file at helpers/LeadSuccess.csv. Find all leads where the account_name is "Walmart". Return each lead's full name and email.`
	columns := []string{"account_name", "full_name", "email", "Country", "Sector"}

	matches := extractIntentFromPhrases(goal, columns)

	hasFilter := false
	hasGroupBy := false
	for _, m := range matches {
		if m.Type == "filter" && m.Column == "account_name" && m.Value == "Walmart" {
			hasFilter = true
		}
		if m.Type == "group_by" {
			hasGroupBy = true
		}
	}
	if !hasFilter {
		t.Error("expected filter(account_name, Walmart) match")
	}
	if hasGroupBy {
		t.Error("should NOT have group_by match for a lookup query")
	}
}

func TestRegexExtract_CountByCountry(t *testing.T) {
	goal := `Read the CSV file at helpers/LeadSuccess.csv. Count the total number of leads for each unique value in the Country column. Sort the results by count in descending order.`
	columns := []string{"Country", "Sector", "Lead_Source", "account_name"}

	matches := extractIntentFromPhrases(goal, columns)

	hasGroupBy := false
	hasCount := false
	hasOrder := false
	hasFilter := false
	for _, m := range matches {
		if m.Type == "group_by" && m.Column == "Country" {
			hasGroupBy = true
		}
		if m.Type == "aggregate" && m.Function == "COUNT" {
			hasCount = true
		}
		if m.Type == "order" {
			hasOrder = true
		}
		if m.Type == "filter" {
			hasFilter = true
		}
	}
	if !hasGroupBy {
		t.Error("expected group_by(Country)")
	}
	if !hasCount {
		t.Error("expected aggregate(COUNT)")
	}
	if !hasOrder {
		t.Error("expected order(DESC)")
	}
	if hasFilter {
		t.Error("should NOT have filter match")
	}
}

func TestRegexExtract_SectorBreakdown(t *testing.T) {
	goal := `Read the CSV file at helpers/LeadSuccess.csv. Group all leads by the Sector column and provide a count for each sector. Sort by count descending.`
	columns := []string{"Sector", "Country", "Lead_Source"}

	matches := extractIntentFromPhrases(goal, columns)

	hasGroupBy := false
	for _, m := range matches {
		if m.Type == "group_by" && m.Column == "Sector" {
			hasGroupBy = true
		}
	}
	if !hasGroupBy {
		t.Error("expected group_by(Sector)")
	}
}

func TestRegexExtract_SourceByOwner(t *testing.T) {
	goal := `Read the CSV file at helpers/LeadSuccess.csv. For each unique Account Owner (the Accout_Owner column — note the column name is misspelled in the data), count their total number of leads and list the distinct Lead_Source values for their leads. Sort by total lead count descending.`
	columns := []string{"Accout_Owner", "Lead_Source", "Country", "Sector"}

	matches := extractIntentFromPhrases(goal, columns)

	hasGroupBy := false
	hasCount := false
	hasGroupConcat := false
	for _, m := range matches {
		if m.Type == "group_by" {
			hasGroupBy = true
		}
		if m.Type == "aggregate" && m.Function == "COUNT" {
			hasCount = true
		}
		if m.Type == "aggregate" && m.Function == "GROUP_CONCAT" && m.Distinct {
			hasGroupConcat = true
		}
	}
	if !hasGroupBy {
		t.Error("expected group_by match")
	}
	if !hasCount {
		t.Error("expected COUNT aggregate match")
	}
	if !hasGroupConcat {
		t.Error("expected GROUP_CONCAT DISTINCT aggregate match")
	}
}

func TestRegexExtract_TargetAccount(t *testing.T) {
	goal := `Read the CSV file at helpers/LeadSuccess.csv. Find all leads where the Target_Account? column equals "Yes". Group these leads by Primary_Incumbent_CDN. For each group, count the total number of leads and calculate the average deal size from the Deal_Size column. Sort by lead count descending.`
	columns := []string{"Target_Account_", "Primary_Incumbent_CDN", "Deal_Size", "Country"}

	matches := extractIntentFromPhrases(goal, columns)

	hasFilter := false
	hasGroupBy := false
	for _, m := range matches {
		if m.Type == "filter" && m.Value == "Yes" {
			hasFilter = true
		}
		if m.Type == "group_by" {
			hasGroupBy = true
		}
	}
	if !hasFilter {
		t.Error("expected filter match with value=Yes")
	}
	if !hasGroupBy {
		t.Error("expected group_by match")
	}
}

func TestRegexExtract_NoFalsePositives(t *testing.T) {
	goal := `Read the CSV file at helpers/LeadSuccess.csv`
	columns := []string{"Country", "Sector"}

	matches := extractIntentFromPhrases(goal, columns)

	// The only match should come from the full goal fallback phrase,
	// but since the goal is all file-path content, nothing should match
	for _, m := range matches {
		if m.Type == "filter" || m.Type == "group_by" {
			t.Errorf("unexpected match on file-path-only goal: %+v", m)
		}
	}
}

// sqlContains is a helper for substring check in SQL assertions.
func sqlContains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// =======================================
// Slice 6: Query Confidence Scoring (ADR-0076)
// =======================================

func TestScoreIntent_HighConfidenceAggregate(t *testing.T) {
	intent := &QueryIntent{
		GroupColumn:    "Country",
		AggFunction:   "COUNT",
		AggColumn:     "*",
		OrderDirection: "DESC",
	}
	sources := map[string]string{
		"groupColumn":    "regex",
		"aggFunction":    "regex",
		"orderDirection": "regex",
	}

	qc := ScoreIntent(intent, sources)
	if qc.Score < 0.70 {
		t.Errorf("expected score ≥ 0.70 for high-confidence aggregate, got %.2f", qc.Score)
	}
	if qc.Archetype != "aggregate" {
		t.Errorf("expected archetype=aggregate, got %s", qc.Archetype)
	}
}

func TestScoreIntent_WarmEmbeddingColumn(t *testing.T) {
	intent := &QueryIntent{
		GroupColumn: "Accout_Owner",
		AggFunction: "COUNT",
		AggColumn:   "*",
	}
	sources := map[string]string{
		"groupColumn": "embedding", // resolved via embedding, slight penalty
		"aggFunction": "regex",
	}

	qc := ScoreIntent(intent, sources)
	// Embedding-resolved column: +0.20 - 0.05 = +0.15 net, plus regex agg +0.25
	// Should be in warm range (0.50-0.69)
	if qc.Score >= 0.70 {
		t.Errorf("expected score < 0.70 for embedding-resolved column, got %.2f", qc.Score)
	}
	if qc.Score < 0.30 {
		t.Errorf("expected score ≥ 0.30, got %.2f", qc.Score)
	}
}

func TestScoreIntent_ColdUnknown(t *testing.T) {
	intent := &QueryIntent{} // empty
	sources := map[string]string{}

	qc := ScoreIntent(intent, sources)
	if qc.Score >= 0.50 {
		t.Errorf("expected score < 0.50 for empty intent, got %.2f", qc.Score)
	}
	if qc.Archetype != "unknown" {
		t.Errorf("expected archetype=unknown, got %s", qc.Archetype)
	}
}

func TestScoreIntent_LookupWithSelectColumns(t *testing.T) {
	intent := &QueryIntent{
		Filters: []FilterClause{
			{Column: "account_name", Operator: "=", Value: "Walmart"},
		},
		SelectColumns: []string{"full_name", "email"},
	}
	sources := map[string]string{
		"filter:account_name": "regex",
		"selectColumns":       "embedding",
	}

	qc := ScoreIntent(intent, sources)
	if qc.Score < 0.40 {
		t.Errorf("expected score ≥ 0.40 for lookup with selectColumns, got %.2f", qc.Score)
	}
	if qc.Archetype != "lookup" {
		t.Errorf("expected archetype=lookup, got %s", qc.Archetype)
	}
}

func TestFormatIntentHint_IncludesGaps(t *testing.T) {
	intent := &QueryIntent{
		GroupColumn: "Country",
	}
	sources := map[string]string{
		"groupColumn": "regex",
	}
	qc := ScoreIntent(intent, sources)
	hint := formatIntentHint(intent, qc)

	if !strings.Contains(hint, "Country") {
		t.Error("hint should include the group column")
	}
	if !strings.Contains(hint, "confidence=") {
		t.Error("hint should include confidence score")
	}
}

// =======================================
// Slice 5: Neural Embedding Column Resolution (ADR-0076)
// =======================================

// mockEmbeddingEngine is a controllable mock for testing resolveColumnsWithEmbedding.
// It maps text → vector, so similarity scores can be controlled precisely.
type mockEmbeddingEngine struct {
	vectors map[string][]float32
}

func (m *mockEmbeddingEngine) Embed(_ context.Context, text string) ([]float32, error) {
	if vec, ok := m.vectors[text]; ok {
		return vec, nil
	}
	// Return a zero vector for unknown texts
	return make([]float32, 4), nil
}

func (m *mockEmbeddingEngine) CosineSimilarity(v1, v2 []float32) float32 {
	if len(v1) != len(v2) || len(v1) == 0 {
		return 0.0
	}
	var dot, n1, n2 float32
	for i := range v1 {
		dot += v1[i] * v2[i]
		n1 += v1[i] * v1[i]
		n2 += v2[i] * v2[i]
	}
	if n1 == 0 || n2 == 0 {
		return 0.0
	}
	return dot / (sqrt32(n1) * sqrt32(n2))
}

func sqrt32(x float32) float32 {
	if x <= 0 {
		return 0
	}
	var r float32 = 1.0
	for i := 0; i < 10; i++ {
		r = (r + x/r) / 2
	}
	return r
}

func TestResolveColumns_EmbeddingFallback(t *testing.T) {
	// "Account Owner" should resolve to "Accout_Owner" via high similarity
	mock := &mockEmbeddingEngine{
		vectors: map[string][]float32{
			"Account Owner": {0.9, 0.1, 0.0, 0.0},
			"name":          {0.0, 0.0, 0.8, 0.1},
			"Accout_Owner":  {0.88, 0.12, 0.0, 0.0}, // very close to "Account Owner"
			"email":         {0.0, 0.0, 0.1, 0.9},
		},
	}

	columns := []string{"name", "Accout_Owner", "email"}
	unresolved := []string{"Account Owner"}

	resolved := resolveColumnsWithEmbedding(context.Background(), mock, unresolved, columns, 0.50)

	if got, ok := resolved["Account Owner"]; !ok {
		t.Error("expected 'Account Owner' to be resolved")
	} else if got != "Accout_Owner" {
		t.Errorf("expected 'Account Owner' → 'Accout_Owner', got '%s'", got)
	}
}

func TestResolveColumns_NilEngine(t *testing.T) {
	// nil engine should return empty map (graceful degradation)
	resolved := resolveColumnsWithEmbedding(context.Background(), nil,
		[]string{"Account Owner"}, []string{"Accout_Owner"}, 0.50)

	if len(resolved) != 0 {
		t.Errorf("expected empty map for nil engine, got %v", resolved)
	}
}

func TestResolveColumns_BelowThreshold(t *testing.T) {
	// All vectors are orthogonal — no similarity above threshold
	mock := &mockEmbeddingEngine{
		vectors: map[string][]float32{
			"Account Owner": {1.0, 0.0, 0.0, 0.0},
			"name":          {0.0, 1.0, 0.0, 0.0},
			"Accout_Owner":  {0.0, 0.0, 1.0, 0.0}, // orthogonal to "Account Owner"
			"email":         {0.0, 0.0, 0.0, 1.0},
		},
	}

	columns := []string{"name", "Accout_Owner", "email"}
	unresolved := []string{"Account Owner"}

	resolved := resolveColumnsWithEmbedding(context.Background(), mock, unresolved, columns, 0.50)

	if len(resolved) != 0 {
		t.Errorf("expected empty map (below threshold), got %v", resolved)
	}
}

// =======================================
// Slice 7: Deterministic Query Path Execution (ADR-0076)
// =======================================

func TestDeterministicPath_AggregateQuery(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	// Materialize a test table
	materializeTestData(t, "cache_agg_test")

	intent := &QueryIntent{
		GroupColumn: "Sector",
		AggFunction: "COUNT",
		AggColumn:   "*",
		OrderDirection: "DESC",
	}

	result, demoted, err := executeDeterministicQuery(context.Background(), intent, "cache_agg_test")
	if err != nil {
		t.Fatalf("executeDeterministicQuery failed: %v", err)
	}
	if demoted {
		t.Error("expected demoted=false for aggregate query with results")
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
	// Should contain sector groups
	if !strings.Contains(result, "Tech") || !strings.Contains(result, "Finance") {
		t.Errorf("expected Tech and Finance in results, got: %s", result)
	}
}

func TestDeterministicPath_FilterQuery(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	materializeTestData(t, "cache_filter_test")

	intent := &QueryIntent{
		Filters: []FilterClause{
			{Column: "Name", Operator: "=", Value: "Alice"},
		},
	}

	result, demoted, err := executeDeterministicQuery(context.Background(), intent, "cache_filter_test")
	if err != nil {
		t.Fatalf("executeDeterministicQuery failed: %v", err)
	}
	if demoted {
		t.Error("expected demoted=false for filter query with results")
	}
	if !strings.Contains(result, "Alice") {
		t.Errorf("expected Alice in results, got: %s", result)
	}
}

func TestDeterministicPath_EmptyResultDemotes(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	materializeTestData(t, "cache_empty_test")

	intent := &QueryIntent{
		Filters: []FilterClause{
			{Column: "Name", Operator: "=", Value: "NONEXISTENT_PERSON"},
		},
	}

	_, demoted, err := executeDeterministicQuery(context.Background(), intent, "cache_empty_test")
	if err != nil {
		t.Fatalf("executeDeterministicQuery failed: %v", err)
	}
	if !demoted {
		t.Error("expected demoted=true when filter returns no results")
	}
}

func TestDeterministicPath_MaterializesDerived(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	materializeTestData(t, "cache_derive_test")

	intent := &QueryIntent{
		GroupColumn:    "Sector",
		AggFunction:   "COUNT",
		AggColumn:     "*",
		OrderDirection: "DESC",
	}

	_, demoted, err := executeDeterministicQuery(context.Background(), intent, "cache_derive_test")
	if err != nil {
		t.Fatalf("executeDeterministicQuery failed: %v", err)
	}
	if demoted {
		t.Error("expected demoted=false")
	}

	// Verify derived table was created
	db := cache.QueryDB()
	rows, err := db.Query("SELECT table_name FROM _cache_tables WHERE table_name LIKE 'cache_derived_%'")
	if err != nil {
		t.Fatalf("failed to query metadata: %v", err)
	}
	defer rows.Close()

	hasDerived := false
	for rows.Next() {
		hasDerived = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration error: %v", err)
	}
	if !hasDerived {
		t.Error("expected a derived table to be created for GROUP BY query")
	}
}


