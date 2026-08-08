package executor

import "testing"

func TestValidateAggregateRowCount_DetectsOvercount(t *testing.T) {
	sql := "SELECT country, COUNT(*) as count FROM leads GROUP BY country"
	resultRows := []map[string]interface{}{
		{"country": "USA", "count": 150.0},
		{"country": "UK", "count": 100.0},
		{"country": "Germany", "count": 63.0},
	}

	msg := ValidateAggregateRowCount(sql, resultRows, 253)

	if msg == "" {
		t.Fatal("Expected validation failure (313 > 253), got empty message")
	}
	if !aggContains(msg, "313") || !aggContains(msg, "253") {
		t.Errorf("Expected message to contain aggregate (313) and total (253), got: %s", msg)
	}
}

func TestValidateAggregateRowCount_PassesValidQuery(t *testing.T) {
	sql := "SELECT country, COUNT(*) as count FROM leads GROUP BY country"
	resultRows := []map[string]interface{}{
		{"country": "USA", "count": 100.0},
		{"country": "UK", "count": 80.0},
		{"country": "Germany", "count": 50.0},
	}

	msg := ValidateAggregateRowCount(sql, resultRows, 253)

	if msg != "" {
		t.Errorf("Expected no validation error (230 <= 253), got: %s", msg)
	}
}

func TestValidateAggregateRowCount_SkipsNonAggregate(t *testing.T) {
	sql := "SELECT * FROM leads WHERE country = 'USA'"
	resultRows := []map[string]interface{}{
		{"id": 1.0, "country": "USA"},
	}

	msg := ValidateAggregateRowCount(sql, resultRows, 253)

	if msg != "" {
		t.Errorf("Expected no validation for non-aggregate query, got: %s", msg)
	}
}

func TestValidateAggregateRowCount_SkipsUnknownTotal(t *testing.T) {
	sql := "SELECT country, COUNT(*) as count FROM leads GROUP BY country"
	resultRows := []map[string]interface{}{
		{"country": "USA", "count": 500.0},
	}

	// knownTotalRows = 0 means we don't know the total — skip validation
	msg := ValidateAggregateRowCount(sql, resultRows, 0)

	if msg != "" {
		t.Errorf("Expected no validation when total is unknown, got: %s", msg)
	}
}

func TestValidateAggregateRowCount_NoCountColumn(t *testing.T) {
	sql := "SELECT country, AVG(age) as avg_age FROM leads GROUP BY country"
	resultRows := []map[string]interface{}{
		{"country": "USA", "avg_age": 35.5},
		{"country": "UK", "avg_age": 42.1},
	}

	msg := ValidateAggregateRowCount(sql, resultRows, 253)

	if msg != "" {
		t.Errorf("Expected no validation when no count column is found, got: %s", msg)
	}
}

func aggContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && aggContainsSubstring(s, substr))
}

func aggContainsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
