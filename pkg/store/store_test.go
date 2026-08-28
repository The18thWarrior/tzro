package store

import (
	"strings"
	"testing"
)

func TestStore_PutAndGetBlob(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	body := "func Add(a, b int) int {\n\treturn a + b\n}"
	hash, err := s.PutBlob("math.go", 10, 12, body)
	if err != nil {
		t.Fatalf("PutBlob failed: %v", err)
	}
	if len(hash) == 0 {
		t.Fatalf("expected non-empty hash")
	}

	blob, err := s.GetBlob(hash)
	if err != nil {
		t.Fatalf("GetBlob failed: %v", err)
	}
	if blob.Body != body {
		t.Errorf("expected body %q, got %q", body, blob.Body)
	}
	if blob.FilePath != "math.go" {
		t.Errorf("expected filepath math.go, got %s", blob.FilePath)
	}
}

func TestStore_IndexAndSearchSymbols(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	err = s.IndexSymbol("ValidateToken", "function", "auth/jwt.go", 45, "a8f19c")
	if err != nil {
		t.Fatalf("IndexSymbol failed: %v", err)
	}

	results, err := s.SearchSymbols("Validate", 10)
	if err != nil {
		t.Fatalf("SearchSymbols failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Symbol != "ValidateToken" {
		t.Errorf("expected ValidateToken, got %s", results[0].Symbol)
	}
	if results[0].Hash != "a8f19c" {
		t.Errorf("expected hash a8f19c, got %s", results[0].Hash)
	}
}

func TestImportTabular_Basic(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	columns := []string{"id", "name", "role"}
	rows := [][]string{
		{"1", "Alice", "admin"},
		{"2", "Bob", "user"},
		{"3", "Charlie", "developer"},
	}

	if err := s.ImportTabular("tbl_test1", columns, rows); err != nil {
		t.Fatalf("ImportTabular: %v", err)
	}

	results, cols, err := s.QuerySQL("SELECT * FROM tbl_test1 ORDER BY id")
	if err != nil {
		t.Fatalf("QuerySQL: %v", err)
	}

	if len(cols) != 3 {
		t.Errorf("expected 3 columns, got %d", len(cols))
	}
	if len(results) != 3 {
		t.Errorf("expected 3 rows, got %d", len(results))
	}
	if results[0]["name"] != "Alice" {
		t.Errorf("expected Alice, got %s", results[0]["name"])
	}
}

func TestImportTabular_Reimport(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	columns := []string{"id", "value"}
	rows1 := [][]string{{"1", "old"}, {"2", "data"}}
	rows2 := [][]string{{"1", "new"}, {"2", "data"}, {"3", "added"}}

	if err := s.ImportTabular("tbl_reimport", columns, rows1); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if err := s.ImportTabular("tbl_reimport", columns, rows2); err != nil {
		t.Fatalf("reimport: %v", err)
	}

	results, _, err := s.QuerySQL("SELECT * FROM tbl_reimport ORDER BY id")
	if err != nil {
		t.Fatalf("QuerySQL: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 rows after reimport, got %d", len(results))
	}
	if results[0]["value"] != "new" {
		t.Errorf("expected reimported value 'new', got %s", results[0]["value"])
	}
}

func TestImportTabular_RejectsSystemTable(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	err = s.ImportTabular("content_blobs", []string{"a"}, [][]string{{"1"}})
	if err == nil {
		t.Error("expected error when importing into system table")
	}
}

func TestImportTabular_SanitizesColumnNames(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	columns := []string{"user-id", "first.name", "role (primary)"}
	rows := [][]string{{"1", "Alice", "admin"}}

	if err := s.ImportTabular("tbl_sanitize", columns, rows); err != nil {
		t.Fatalf("ImportTabular: %v", err)
	}

	results, cols, err := s.QuerySQL("SELECT * FROM tbl_sanitize")
	if err != nil {
		t.Fatalf("QuerySQL: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 row, got %d", len(results))
	}
	for _, col := range cols {
		if strings.ContainsAny(col, "-.() ") {
			t.Errorf("column name %q should have been sanitized", col)
		}
	}
}

func TestQuerySQL_SelectOnly(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	rejectCases := []string{
		"DROP TABLE content_blobs",
		"DELETE FROM tbl_test",
		"INSERT INTO tbl_test VALUES (1)",
		"UPDATE tbl_test SET id = 1",
	}
	for _, q := range rejectCases {
		_, _, err := s.QuerySQL(q)
		if err == nil {
			t.Errorf("expected error for non-SELECT query: %s", q)
		}
	}
}

func TestQuerySQL_RejectsSystemTableAccess(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	_, _, err = s.QuerySQL("SELECT * FROM content_blobs")
	if err == nil {
		t.Error("expected error when querying system table")
	}
	if err != nil && !strings.Contains(err.Error(), "system table") {
		t.Errorf("expected system table error, got: %v", err)
	}
}

func TestQuerySQL_WithWhereClause(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	columns := []string{"id", "name", "score"}
	rows := [][]string{
		{"1", "Alice", "95"},
		{"2", "Bob", "72"},
		{"3", "Charlie", "88"},
	}
	if err := s.ImportTabular("tbl_scores", columns, rows); err != nil {
		t.Fatalf("ImportTabular: %v", err)
	}

	results, _, err := s.QuerySQL("SELECT name, score FROM tbl_scores WHERE score > '80' ORDER BY name")
	if err != nil {
		t.Fatalf("QuerySQL: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 rows with score > 80, got %d", len(results))
	}
}
