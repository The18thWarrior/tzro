package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// setupTestQueryDB creates an in-memory ephemeral query DB for testing.
// Returns a cleanup function that closes and resets the singleton.
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
	SetQueryDBForTesting(db)
	return func() {
		db.Close()
		SetQueryDBForTesting(nil)
	}
}

// materializeTestData is a helper that materializes a standard test dataset.
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
	if err := MaterializeTable(cacheID, rawPayload, columnTypes, "test_task"); err != nil {
		t.Fatalf("MaterializeTable failed: %v", err)
	}
}

// ========================
// Slice 1: MaterializeTable
// ========================

func TestMaterializeTable_BasicArray(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	rawPayload := `[
		{"Name": "Alice", "Age": 30, "Active": true},
		{"Name": "Bob", "Age": 25, "Active": false},
		{"Name": "Charlie", "Age": 35, "Active": true}
	]`
	columnTypes := map[string]string{
		"Name":   "TEXT",
		"Age":    "INTEGER",
		"Active": "INTEGER",
	}

	err := MaterializeTable("cache_test_001", rawPayload, columnTypes, "task_abc")
	if err != nil {
		t.Fatalf("MaterializeTable failed: %v", err)
	}

	db := QueryDB()
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM cache_test_001").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count rows: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 rows, got %d", count)
	}

	var tableName, taskID string
	var createdAt int64
	err = db.QueryRow("SELECT table_name, task_id, created_at FROM _cache_tables WHERE table_name = ?", "cache_test_001").Scan(&tableName, &taskID, &createdAt)
	if err != nil {
		t.Fatalf("failed to read metadata: %v", err)
	}
	if taskID != "task_abc" {
		t.Errorf("expected task_id 'task_abc', got '%s'", taskID)
	}
	if createdAt == 0 {
		t.Error("expected non-zero created_at")
	}

	var age int
	err = db.QueryRow("SELECT Age FROM cache_test_001 WHERE Name = 'Alice'").Scan(&age)
	if err != nil {
		t.Fatalf("failed to query age: %v", err)
	}
	if age != 30 {
		t.Errorf("expected Age 30, got %d", age)
	}
}

func TestMaterializeTable_NestedJSONObject(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	rawPayload := `{"records": [
		{"Name": "Alice", "Revenue": 1000.50},
		{"Name": "Bob", "Revenue": 2000.75}
	]}`
	columnTypes := map[string]string{
		"Name":    "TEXT",
		"Revenue": "REAL",
	}

	err := MaterializeTable("cache_test_002", rawPayload, columnTypes, "")
	if err != nil {
		t.Fatalf("MaterializeTable failed: %v", err)
	}

	db := QueryDB()
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM cache_test_002").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count rows: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 rows, got %d", count)
	}
}

func TestMaterializeTable_EmptyPayload(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	err := MaterializeTable("cache_test_empty", "[]", map[string]string{}, "")
	if err == nil {
		t.Error("expected error for empty payload, got nil")
	}
}

func TestMaterializeTable_InvalidJSON(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	err := MaterializeTable("cache_test_invalid", "not-json", map[string]string{}, "")
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestMaterializeTable_NullCoercion(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	rawPayload := `[
		{"Name": "Alice", "Age": 30},
		{"Name": "Bob", "Age": "not-a-number"}
	]`
	columnTypes := map[string]string{
		"Name": "TEXT",
		"Age":  "INTEGER",
	}

	err := MaterializeTable("cache_test_null", rawPayload, columnTypes, "")
	if err != nil {
		t.Fatalf("MaterializeTable failed: %v", err)
	}

	db := QueryDB()
	var age sql.NullInt64
	err = db.QueryRow("SELECT Age FROM cache_test_null WHERE Name = 'Bob'").Scan(&age)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	if age.Valid {
		t.Errorf("expected NULL age for Bob (coercion failure), got %d", age.Int64)
	}
}

// ========================
// Slice 2: ExecuteSQL
// ========================

func TestExecuteSQL_BasicSelect(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()
	materializeTestData(t, "cache_test_select")

	ctx := context.Background()
	result, err := ExecuteSQL(ctx, "cache_test_select", "SELECT * FROM cache_test_select")
	if err != nil {
		t.Fatalf("ExecuteSQL failed: %v", err)
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &rows); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if len(rows) != 5 {
		t.Errorf("expected 5 rows, got %d", len(rows))
	}
}

func TestExecuteSQL_WhereFilter(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()
	materializeTestData(t, "cache_test_where")

	ctx := context.Background()
	result, err := ExecuteSQL(ctx, "cache_test_where", "SELECT Name FROM cache_test_where WHERE Sector = 'Tech'")
	if err != nil {
		t.Fatalf("ExecuteSQL failed: %v", err)
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &rows); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 Tech rows, got %d", len(rows))
	}
}

func TestExecuteSQL_GroupBy(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()
	materializeTestData(t, "cache_test_group")

	ctx := context.Background()
	result, err := ExecuteSQL(ctx, "cache_test_group",
		"SELECT Sector, COUNT(*) as cnt FROM cache_test_group GROUP BY Sector ORDER BY cnt DESC")
	if err != nil {
		t.Fatalf("ExecuteSQL failed: %v", err)
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &rows); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3 groups (Tech, Finance, Health), got %d", len(rows))
	}
	// First group should be Tech or Finance (both have count 2)
	if cnt, ok := rows[0]["cnt"].(float64); ok {
		if int(cnt) != 2 {
			t.Errorf("expected top group count 2, got %v", cnt)
		}
	}
}

func TestExecuteSQL_EmptyResult(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()
	materializeTestData(t, "cache_test_empty_result")

	ctx := context.Background()
	result, err := ExecuteSQL(ctx, "cache_test_empty_result",
		"SELECT * FROM cache_test_empty_result WHERE Name = 'Nobody'")
	if err != nil {
		t.Fatalf("ExecuteSQL failed: %v", err)
	}
	if result != "[]" {
		t.Errorf("expected '[]', got '%s'", result)
	}
}

// ========================
// Slice 3: Safety Layer 1
// ========================

func TestExecuteSQL_RejectNonSelect(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()
	materializeTestData(t, "cache_test_reject")

	ctx := context.Background()
	tests := []struct {
		name string
		sql  string
	}{
		{"DROP", "DROP TABLE cache_test_reject"},
		{"INSERT", "INSERT INTO cache_test_reject VALUES ('X', 1, 'Y')"},
		{"UPDATE", "UPDATE cache_test_reject SET Name = 'Evil' WHERE 1=1"},
		{"DELETE", "DELETE FROM cache_test_reject"},
		{"ALTER", "ALTER TABLE cache_test_reject ADD COLUMN evil TEXT"},
		{"CREATE", "CREATE TABLE evil (id INTEGER)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ExecuteSQL(ctx, "cache_test_reject", tt.sql)
			if err == nil {
				t.Errorf("expected error for %s statement, got nil", tt.name)
			}
			if !strings.Contains(err.Error(), "only SELECT") {
				t.Errorf("expected 'only SELECT' error, got: %v", err)
			}
		})
	}
}

// ========================
// Slice 4: Safety Layer 2
// ========================

func TestExecuteSQL_RejectNonCacheTables(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	ctx := context.Background()
	tests := []struct {
		name string
		sql  string
	}{
		{"direct table", "SELECT * FROM fact_memories"},
		{"join", "SELECT * FROM cache_test JOIN kg_nodes ON 1=1"},
		{"subquery FROM", "SELECT * FROM node_states WHERE id IN (SELECT id FROM cache_test)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ExecuteSQL(ctx, "cache_test", tt.sql)
			if err == nil {
				t.Errorf("expected error for non-cache table reference, got nil")
			}
			if !strings.Contains(err.Error(), "disallowed table") {
				t.Errorf("expected 'disallowed table' error, got: %v", err)
			}
		})
	}
}

func TestExecuteSQL_AllowCacheTablesOnly(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()
	materializeTestData(t, "cache_test_allow")

	ctx := context.Background()
	// This should pass validation (table starts with cache_)
	_, err := ExecuteSQL(ctx, "cache_test_allow", "SELECT COUNT(*) FROM cache_test_allow")
	if err != nil {
		t.Fatalf("expected cache_ table to be allowed, got: %v", err)
	}
}

// ========================
// Slice 5: Row Cap
// ========================

func TestExecuteSQL_RowCap(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	// Generate 600 rows
	var records []map[string]interface{}
	for i := 0; i < 600; i++ {
		records = append(records, map[string]interface{}{
			"ID":   float64(i),
			"Name": fmt.Sprintf("Row_%d", i),
		})
	}
	payload, _ := json.Marshal(records)
	columnTypes := map[string]string{"ID": "INTEGER", "Name": "TEXT"}
	if err := MaterializeTable("cache_test_rowcap", string(payload), columnTypes, ""); err != nil {
		t.Fatalf("MaterializeTable failed: %v", err)
	}

	ctx := context.Background()
	result, err := ExecuteSQL(ctx, "cache_test_rowcap", "SELECT * FROM cache_test_rowcap")
	if err != nil {
		t.Fatalf("ExecuteSQL failed: %v", err)
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &rows); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	// Should be 500 data rows + 1 pagination note = 501
	if len(rows) != 501 {
		t.Errorf("expected 501 rows (500 data + 1 note), got %d", len(rows))
	}

	// Last row should be the pagination note
	lastRow := rows[len(rows)-1]
	if note, ok := lastRow["_note"].(string); !ok || !strings.Contains(note, "pagination") {
		t.Errorf("expected pagination note in last row, got: %v", lastRow)
	}
}

func TestExecuteSQL_UserLimitRespected(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()
	materializeTestData(t, "cache_test_userlimit")

	ctx := context.Background()
	result, err := ExecuteSQL(ctx, "cache_test_userlimit", "SELECT * FROM cache_test_userlimit LIMIT 2")
	if err != nil {
		t.Fatalf("ExecuteSQL failed: %v", err)
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &rows); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows with user LIMIT, got %d", len(rows))
	}
}

// ========================
// Slice 7: DropTaskTables
// ========================

func TestDropTaskTables(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	// Materialize two tables for the same task
	payload1 := `[{"Name": "Alice"}]`
	payload2 := `[{"Name": "Bob"}]`
	colTypes := map[string]string{"Name": "TEXT"}

	MaterializeTable("cache_drop_1", payload1, colTypes, "task_to_drop")
	MaterializeTable("cache_drop_2", payload2, colTypes, "task_to_drop")
	MaterializeTable("cache_keep_1", payload1, colTypes, "other_task")

	// Drop tables for task_to_drop
	DropTaskTables("task_to_drop")

	db := QueryDB()

	// Dropped tables should not exist
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM _cache_tables WHERE task_id = 'task_to_drop'").Scan(&count)
	if err != nil {
		t.Fatalf("metadata query failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 metadata rows for dropped task, got %d", count)
	}

	// Verify tables are actually gone
	_, err = db.Query("SELECT * FROM cache_drop_1")
	if err == nil {
		t.Error("expected table cache_drop_1 to be dropped")
	}

	// Other task's table should still exist
	err = db.QueryRow("SELECT COUNT(*) FROM cache_keep_1").Scan(&count)
	if err != nil {
		t.Fatalf("other task's table should still exist: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row in kept table, got %d", count)
	}
}

// ========================
// Slice 8: SweepExpiredTables
// ========================

func TestSweepExpiredTables(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	db := QueryDB()

	// Create a table and manually set its created_at to 2 days ago
	payload := `[{"Name": "Old"}]`
	colTypes := map[string]string{"Name": "TEXT"}
	MaterializeTable("cache_old", payload, colTypes, "")
	twoDaysAgo := time.Now().Add(-48 * time.Hour).Unix()
	db.Exec("UPDATE _cache_tables SET created_at = ? WHERE table_name = 'cache_old'", twoDaysAgo)

	// Create a fresh table
	MaterializeTable("cache_fresh", payload, colTypes, "")

	// Sweep
	SweepExpiredTables()

	// Old table should be gone
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM _cache_tables WHERE table_name = 'cache_old'").Scan(&count)
	if err != nil {
		t.Fatalf("metadata query failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected old table metadata to be swept, got %d", count)
	}

	// Fresh table should remain
	err = db.QueryRow("SELECT COUNT(*) FROM _cache_tables WHERE table_name = 'cache_fresh'").Scan(&count)
	if err != nil {
		t.Fatalf("metadata query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected fresh table metadata to remain, got %d", count)
	}
}

// ========================
// Slice 9: Type Mapping
// ========================

func TestTypeMapping_EnvelopeFieldTypes(t *testing.T) {
	result := envelopeFieldTypesToSQLite(map[string]string{
		"Name":    "string",
		"Revenue": "float64",
		"Count":   "float64",
		"Active":  "bool",
	})

	expected := map[string]string{
		"Name":    "TEXT",
		"Revenue": "REAL",
		"Count":   "REAL",
		"Active":  "INTEGER",
	}

	for k, v := range expected {
		if result[k] != v {
			t.Errorf("field %s: expected %s, got %s", k, v, result[k])
		}
	}
}

func TestTypeMapping_ExtractFromCacheEnvelope(t *testing.T) {
	envelopeJSON := `{"fieldTypes": {"Name": "string", "Age": "float64", "Active": "bool"}}`
	result := extractColumnTypesFromEnvelope(envelopeJSON)

	if result["Name"] != "TEXT" {
		t.Errorf("expected TEXT for Name, got %s", result["Name"])
	}
	if result["Age"] != "REAL" {
		t.Errorf("expected REAL for Age, got %s", result["Age"])
	}
	if result["Active"] != "INTEGER" {
		t.Errorf("expected INTEGER for Active, got %s", result["Active"])
	}
}

func TestTypeMapping_ExtractFromDataProfile(t *testing.T) {
	profileJSON := `{"columns": [
		{"name": "Name", "type": "string"},
		{"name": "Revenue", "type": "float"},
		{"name": "Count", "type": "integer"},
		{"name": "Active", "type": "boolean"}
	]}`
	result := extractColumnTypesFromEnvelope(profileJSON)

	expected := map[string]string{
		"Name":    "TEXT",
		"Revenue": "REAL",
		"Count":   "INTEGER",
		"Active":  "INTEGER",
	}

	for k, v := range expected {
		if result[k] != v {
			t.Errorf("field %s: expected %s, got %s", k, v, result[k])
		}
	}
}

// ========================
// Slice 10: CTE Queries
// ========================

func TestExecuteSQL_CTEAllowed(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()
	materializeTestData(t, "cache_test_cte")

	ctx := context.Background()
	cteQuery := `WITH tech_workers AS (
		SELECT * FROM cache_test_cte WHERE Sector = 'Tech'
	)
	SELECT Name, Age FROM tech_workers ORDER BY Age`

	result, err := ExecuteSQL(ctx, "cache_test_cte", cteQuery)
	if err != nil {
		t.Fatalf("CTE query should be allowed, got: %v", err)
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &rows); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 tech workers, got %d", len(rows))
	}
}

// ========================
// Slice 11: SQL Comment Stripping
// ========================

func TestStripSQLComments(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"line comment", "SELECT * -- this is a comment\nFROM cache_test", "SELECT * \nFROM cache_test"},
		{"block comment", "SELECT /* columns */ * FROM cache_test", "SELECT  * FROM cache_test"},
		{"no comments", "SELECT * FROM cache_test", "SELECT * FROM cache_test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripSQLComments(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestValidateStatementType_CommentBypass(t *testing.T) {
	// Ensure comments can't be used to bypass statement type check
	err := validateStatementType("-- SELECT\nDROP TABLE cache_test")
	if err == nil {
		t.Error("expected error when comment hides real statement type")
	}
}

func TestExtractTableNames(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		expected []string
	}{
		{"simple FROM", "SELECT * FROM cache_123", []string{"cache_123"}},
		{"with JOIN", "SELECT * FROM cache_a JOIN cache_b ON 1=1", []string{"cache_a", "cache_b"}},
		{"with alias", "SELECT * FROM cache_test AS t WHERE t.x = 1", []string{"cache_test"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTableNames(tt.sql)
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d tables, got %d: %v", len(tt.expected), len(result), result)
			}
			for i, exp := range tt.expected {
				if i < len(result) && result[i] != exp {
					t.Errorf("table %d: expected %s, got %s", i, exp, result[i])
				}
			}
		})
	}
}

// ========================
// Helpers
// ========================

func mustMarshalJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	return string(b)
}

var _ = os.Remove // suppress unused import
var _ = time.Now  // suppress unused import

// ========================
// P0 Fix Tests: Column Name Sanitization
// ========================

func TestSanitizeEnvelopeFieldNames(t *testing.T) {
	env := CacheEnvelope{
		Fields:     []string{"Name", "Target_Account?", "Email.Address", "Age"},
		FieldTypes: map[string]string{"Name": "string", "Target_Account?": "string", "Email.Address": "string", "Age": "float64"},
		SampleRecord: map[string]interface{}{
			"Name":            "Alice",
			"Target_Account?": "Yes",
			"Email.Address":   "alice@example.com",
			"Age":             30,
		},
		EnumValues: map[string][]string{
			"Target_Account?": {"Yes", "No"},
		},
	}

	result := sanitizeEnvelopeFieldNames(env)

	// Check Fields
	expectedFields := map[string]bool{
		"Name":            true,
		"Target_Account_": true,
		"Email_Address":   true,
		"Age":             true,
	}
	for _, f := range result.Fields {
		if !expectedFields[f] {
			t.Errorf("unexpected field name: %s", f)
		}
	}

	// Check FieldTypes
	if _, ok := result.FieldTypes["Target_Account_"]; !ok {
		t.Error("FieldTypes should have sanitized key 'Target_Account_'")
	}
	if _, ok := result.FieldTypes["Target_Account?"]; ok {
		t.Error("FieldTypes should NOT have original key 'Target_Account?'")
	}

	// Check SampleRecord
	if _, ok := result.SampleRecord["Target_Account_"]; !ok {
		t.Error("SampleRecord should have sanitized key 'Target_Account_'")
	}

	// Check EnumValues
	if vals, ok := result.EnumValues["Target_Account_"]; !ok {
		t.Error("EnumValues should have sanitized key 'Target_Account_'")
	} else if len(vals) != 2 {
		t.Errorf("expected 2 enum values, got %d", len(vals))
	}
}

func TestMaterializeTable_SpecialCharColumns(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	rawPayload := `[
		{"Name": "Alice", "Target_Account?": "Yes", "Count#": 10},
		{"Name": "Bob", "Target_Account?": "No", "Count#": 20}
	]`
	columnTypes := map[string]string{
		"Name":            "TEXT",
		"Target_Account?": "TEXT",
		"Count#":          "INTEGER",
	}

	cacheID := fmt.Sprintf("cache_%d", time.Now().UnixNano())
	err := MaterializeTable(cacheID, rawPayload, columnTypes, "test_task")
	if err != nil {
		t.Fatalf("MaterializeTable failed: %v", err)
	}

	// Query using sanitized column names
	result, err := ExecuteSQL(context.Background(), cacheID,
		fmt.Sprintf("SELECT Target_Account_, Count_ FROM %s WHERE Target_Account_ = 'Yes'", cacheID))
	if err != nil {
		t.Fatalf("SQL query with sanitized column names failed: %v", err)
	}

	// Parse and verify
	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &rows); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}

	// Verify the envelope also uses sanitized names
	envJSON, _ := createCacheEnvelope(rawPayload)
	if strings.Contains(envJSON, "Target_Account?") {
		t.Error("envelope should NOT contain unsanitized column name 'Target_Account?'")
	}
	if !strings.Contains(envJSON, "Target_Account_") {
		t.Error("envelope should contain sanitized column name 'Target_Account_'")
	}
}

// =======================================
// Slice 1: MaterializeDerivedTable (ADR-0076)
// =======================================

func TestMaterializeDerivedTable_Basic(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	// Materialize a base table first
	materializeTestData(t, "cache_base_123")

	// Simulate a GROUP BY result
	resultJSON := `[
		{"Sector": "Tech", "count": 2},
		{"Sector": "Finance", "count": 2},
		{"Sector": "Health", "count": 1}
	]`
	sql := "SELECT Sector, COUNT(*) as count FROM [cache_base_123] GROUP BY Sector"

	derivedID, err := MaterializeDerivedTable("cache_base_123", sql, resultJSON, "test_task")
	if err != nil {
		t.Fatalf("MaterializeDerivedTable failed: %v", err)
	}

	// Derived ID should be deterministic and non-empty
	if derivedID == "" {
		t.Fatal("expected non-empty derived cacheID")
	}
	if !strings.HasPrefix(derivedID, "cache_derived_") {
		t.Errorf("expected cache_derived_ prefix, got: %s", derivedID)
	}

	// Query the derived table
	db := QueryDB()
	rows, err := db.Query(fmt.Sprintf("SELECT * FROM [%s]", derivedID))
	if err != nil {
		t.Fatalf("failed to query derived table: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}
	if count != 3 {
		t.Errorf("expected 3 rows in derived table, got %d", count)
	}
}

func TestMaterializeDerivedTable_DeterministicName(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	resultJSON := `[{"col": "val"}]`
	sql := "SELECT col FROM [cache_base] GROUP BY col"

	id1, err := MaterializeDerivedTable("cache_base", sql, resultJSON, "task1")
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	// Same inputs → same derived ID
	id2, err := MaterializeDerivedTable("cache_base", sql, resultJSON, "task1")
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}

	if id1 != id2 {
		t.Errorf("expected deterministic ID: %s != %s", id1, id2)
	}

	// Different SQL → different derived ID
	id3, err := MaterializeDerivedTable("cache_base", "SELECT col FROM [cache_base]", resultJSON, "task1")
	if err != nil {
		t.Fatalf("third call failed: %v", err)
	}
	if id1 == id3 {
		t.Errorf("different SQL should produce different derived ID: both = %s", id1)
	}
}

func TestMaterializeDerivedTable_Idempotent(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	resultJSON := `[
		{"Sector": "Tech", "count": 2},
		{"Sector": "Finance", "count": 2}
	]`
	sql := "SELECT Sector, COUNT(*) as count FROM [cache_test] GROUP BY Sector"

	// First call
	derivedID, err := MaterializeDerivedTable("cache_test", sql, resultJSON, "task1")
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	// Second call — should succeed without error and not duplicate rows
	_, err = MaterializeDerivedTable("cache_test", sql, resultJSON, "task1")
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}

	// Verify row count is still 2 (not 4)
	db := QueryDB()
	var rowCount int
	err = db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM [%s]", derivedID)).Scan(&rowCount)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if rowCount != 2 {
		t.Errorf("expected 2 rows (idempotent), got %d", rowCount)
	}
}

func TestMaterializeDerivedTable_RegistersMetadata(t *testing.T) {
	cleanup := setupTestQueryDB(t)
	defer cleanup()

	resultJSON := `[{"x": 1}]`
	sql := "SELECT x FROM [cache_meta_test]"

	derivedID, err := MaterializeDerivedTable("cache_meta_test", sql, resultJSON, "my_task_123")
	if err != nil {
		t.Fatalf("MaterializeDerivedTable failed: %v", err)
	}

	// Check metadata table
	db := QueryDB()
	var tableName, taskID string
	err = db.QueryRow("SELECT table_name, task_id FROM _cache_tables WHERE table_name = ?", derivedID).
		Scan(&tableName, &taskID)
	if err != nil {
		t.Fatalf("metadata query failed: %v", err)
	}
	if tableName != derivedID {
		t.Errorf("expected table_name=%s, got %s", derivedID, tableName)
	}
	if taskID != "my_task_123" {
		t.Errorf("expected task_id=my_task_123, got %s", taskID)
	}
}

