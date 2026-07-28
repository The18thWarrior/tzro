package executor

import (
	"strings"
	"testing"
)

func TestExtractSQLFromText_ValidSelect(t *testing.T) {
	text := `I should query the cache to find Walmart leads:
SELECT name, email FROM cache_178520201562423 WHERE account_name = 'Walmart'

This would give us the specific records.`

	sql, table := extractSQLFromText(text)
	if table != "cache_178520201562423" {
		t.Errorf("expected cache_178520201562423, got %q", table)
	}
	if sql == "" {
		t.Fatal("expected SQL, got empty string")
	}
	if !strings.Contains(sql, "SELECT") || !strings.Contains(sql, "cache_178520201562423") {
		t.Errorf("SQL doesn't contain expected keywords: %q", sql)
	}
}

func TestExtractSQLFromText_GroupBy(t *testing.T) {
	text := `To find the distribution:
SELECT lead_source, COUNT(*) as cnt FROM cache_1234567890123 GROUP BY lead_source ORDER BY cnt DESC;`

	sql, table := extractSQLFromText(text)
	if table != "cache_1234567890123" {
		t.Errorf("expected cache_1234567890123, got %q", table)
	}
	if sql == "" {
		t.Fatal("expected SQL, got empty string")
	}
}

func TestExtractSQLFromText_NoSQL(t *testing.T) {
	text := "The data contains 253 rows with 22 columns. We should analyze the lead distribution."
	sql, table := extractSQLFromText(text)
	if sql != "" || table != "" {
		t.Errorf("expected empty, got sql=%q table=%q", sql, table)
	}
}

func TestExtractSQLFromText_NonCacheTable(t *testing.T) {
	// Should NOT match non-cache tables — scoped to cache_\d{10,}
	text := "SELECT * FROM users WHERE active = 1"
	sql, table := extractSQLFromText(text)
	if sql != "" || table != "" {
		t.Errorf("should not match non-cache table, got sql=%q table=%q", sql, table)
	}
}
