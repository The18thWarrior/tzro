package cache

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestEnrichSchema_BasicMetrics(t *testing.T) {
	db := setupTestDB(t)

	// Create and populate a test table
	db.Exec(`CREATE TABLE test_cache (
		id INTEGER,
		name TEXT,
		value REAL
	)`)
	db.Exec(`INSERT INTO test_cache VALUES (1, 'alice', 10.5)`)
	db.Exec(`INSERT INTO test_cache VALUES (2, 'bob', 20.0)`)
	db.Exec(`INSERT INTO test_cache VALUES (3, NULL, 30.5)`)

	enrichments, err := EnrichSchema(db, "test_cache")
	if err != nil {
		t.Fatalf("EnrichSchema failed: %v", err)
	}

	if len(enrichments) != 3 {
		t.Fatalf("expected 3 column enrichments, got %d", len(enrichments))
	}

	// Find the 'name' column enrichment
	var nameEnrich *SchemaEnrichment
	for i := range enrichments {
		if enrichments[i].ColumnName == "name" {
			nameEnrich = &enrichments[i]
			break
		}
	}
	if nameEnrich == nil {
		t.Fatal("expected enrichment for 'name' column")
	}

	if nameEnrich.TotalCount != 3 {
		t.Errorf("expected TotalCount=3, got %d", nameEnrich.TotalCount)
	}
	if nameEnrich.NonNullCount != 2 {
		t.Errorf("expected NonNullCount=2 (one NULL row), got %d", nameEnrich.NonNullCount)
	}
	if nameEnrich.Cardinality != 2 {
		t.Errorf("expected Cardinality=2 (alice, bob), got %d", nameEnrich.Cardinality)
	}
}

func TestEnrichSchema_TopValues(t *testing.T) {
	db := setupTestDB(t)

	db.Exec(`CREATE TABLE test_cache (category TEXT)`)
	db.Exec(`INSERT INTO test_cache VALUES ('tech')`)
	db.Exec(`INSERT INTO test_cache VALUES ('tech')`)
	db.Exec(`INSERT INTO test_cache VALUES ('tech')`)
	db.Exec(`INSERT INTO test_cache VALUES ('finance')`)
	db.Exec(`INSERT INTO test_cache VALUES ('finance')`)
	db.Exec(`INSERT INTO test_cache VALUES ('health')`)

	enrichments, err := EnrichSchema(db, "test_cache")
	if err != nil {
		t.Fatalf("EnrichSchema failed: %v", err)
	}

	if len(enrichments) != 1 {
		t.Fatalf("expected 1 enrichment, got %d", len(enrichments))
	}

	e := enrichments[0]
	if len(e.TopValues) == 0 {
		t.Fatal("expected top values for TEXT column")
	}

	// First top value should be 'tech' with count 3
	if e.TopValues[0].Value != "tech" {
		t.Errorf("expected first top value 'tech', got %q", e.TopValues[0].Value)
	}
	if e.TopValues[0].Count != 3 {
		t.Errorf("expected first top value count 3, got %d", e.TopValues[0].Count)
	}
}

func TestEnrichSchema_EmptyTable(t *testing.T) {
	db := setupTestDB(t)

	db.Exec(`CREATE TABLE test_cache (id INTEGER, name TEXT)`)

	enrichments, err := EnrichSchema(db, "test_cache")
	if err != nil {
		t.Fatalf("EnrichSchema failed: %v", err)
	}

	if len(enrichments) != 2 {
		t.Fatalf("expected 2 enrichments, got %d", len(enrichments))
	}

	for _, e := range enrichments {
		if e.TotalCount != 0 {
			t.Errorf("expected TotalCount=0 for empty table, got %d (col=%s)", e.TotalCount, e.ColumnName)
		}
		if e.NonNullCount != 0 {
			t.Errorf("expected NonNullCount=0 for empty table, got %d (col=%s)", e.NonNullCount, e.ColumnName)
		}
		if e.Cardinality != 0 {
			t.Errorf("expected Cardinality=0 for empty table, got %d (col=%s)", e.Cardinality, e.ColumnName)
		}
	}
}
