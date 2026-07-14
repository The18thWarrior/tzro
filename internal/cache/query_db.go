package cache

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"tzro/internal/config"

	_ "modernc.org/sqlite"
)

var (
	queryDB     *sql.DB
	queryDBOnce sync.Once
	queryDBMu   sync.Mutex
)

// QueryDB returns the ephemeral query database connection, creating it lazily.
// The ephemeral DB is a separate file from tzro.db — SQL from the model
// physically cannot access production tables.
func QueryDB() *sql.DB {
	queryDBOnce.Do(func() {
		dbPath := config.ResolvePath(filepath.Join(".tzro", "cache", "query.db"))
		os.MkdirAll(filepath.Dir(dbPath), 0755)
		db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Cache/QueryDB] Failed to open ephemeral DB: %v\n", err)
			return
		}
		// Create metadata table
		db.Exec(`CREATE TABLE IF NOT EXISTS _cache_tables (
			table_name TEXT PRIMARY KEY,
			task_id TEXT,
			created_at INTEGER
		)`)
		queryDB = db
	})
	return queryDB
}

// CloseQueryDB closes the ephemeral database connection and resets the singleton
// so it can be re-opened on next access.
func CloseQueryDB() {
	queryDBMu.Lock()
	defer queryDBMu.Unlock()
	if queryDB != nil {
		queryDB.Close()
		queryDB = nil
		queryDBOnce = sync.Once{} // Reset so next call re-opens
	}
}

// SetQueryDBForTesting allows tests to inject an in-memory database.
func SetQueryDBForTesting(db *sql.DB) {
	queryDBMu.Lock()
	defer queryDBMu.Unlock()
	queryDB = db
	queryDBOnce = sync.Once{}
	if db != nil {
		// Mark as already initialized so QueryDB() returns the injected DB
		queryDBOnce.Do(func() {})
	}
}

// MaterializeTable creates a table in the ephemeral query DB from a JSON
// array of records using the provided column type metadata.
//
// Parameters:
//   - cacheID: used as the table name (e.g., "cache_1784005696353229000")
//   - rawPayload: JSON string containing an array of record objects
//   - columnTypes: map of column name → SQLite type ("TEXT", "INTEGER", "REAL")
//   - taskID: the owning task ID for lifecycle tracking
//
// Column values that fail type coercion are inserted as NULL.
func MaterializeTable(cacheID, rawPayload string, columnTypes map[string]string, taskID string) error {
	db := QueryDB()
	if db == nil {
		return fmt.Errorf("ephemeral query DB not available")
	}

	records, err := extractRecordsArray(rawPayload)
	if err != nil {
		return fmt.Errorf("failed to extract records: %w", err)
	}
	if len(records) == 0 {
		return fmt.Errorf("no records to materialize")
	}

	// Collect column names from the first record
	firstRecord := records[0]
	var columns []string
	for k := range firstRecord {
		columns = append(columns, k)
	}
	if len(columns) == 0 {
		return fmt.Errorf("no columns found in first record")
	}

	// Sanitize table name
	tableName := sanitizeTableName(cacheID)

	// Build CREATE TABLE statement
	var colDefs []string
	for _, col := range columns {
		colType := "TEXT" // default
		if ct, ok := columnTypes[col]; ok {
			colType = ct
		}
		colDefs = append(colDefs, fmt.Sprintf("%s %s", sanitizeColumnName(col), colType))
	}
	createSQL := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", tableName, strings.Join(colDefs, ", "))

	// Begin transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Create table
	if _, err := tx.Exec(createSQL); err != nil {
		return fmt.Errorf("failed to create table: %w", err)
	}

	// Prepare INSERT statement
	placeholders := make([]string, len(columns))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		tableName,
		strings.Join(sanitizeColumnNames(columns), ", "),
		strings.Join(placeholders, ", "))

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("failed to prepare insert: %w", err)
	}
	defer stmt.Close()

	// Batch insert records
	for _, record := range records {
		values := make([]interface{}, len(columns))
		for i, col := range columns {
			val, exists := record[col]
			if !exists || val == nil {
				values[i] = nil
				continue
			}
			colType := columnTypes[col]
			values[i] = coerceValue(val, colType)
		}
		if _, err := stmt.Exec(values...); err != nil {
			// Log but continue — individual row failures are non-fatal
			fmt.Fprintf(os.Stderr, "[Cache/Materialize] Row insert warning: %v\n", err)
		}
	}

	// Insert metadata row
	createdAt := time.Now().Unix()
	if _, err := tx.Exec(`INSERT OR REPLACE INTO _cache_tables (table_name, task_id, created_at) VALUES (?, ?, ?)`,
		cacheID, taskID, createdAt); err != nil {
		return fmt.Errorf("failed to insert metadata: %w", err)
	}

	return tx.Commit()
}

// DropTaskTables removes all materialized cache tables owned by the given taskID.
func DropTaskTables(taskID string) {
	db := QueryDB()
	if db == nil {
		return
	}

	// Collect table names first to avoid iterating rows while modifying
	rows, err := db.Query("SELECT table_name FROM _cache_tables WHERE task_id = ?", taskID)
	if err != nil {
		return
	}
	var tableNames []string
	for rows.Next() {
		var tableName string
		rows.Scan(&tableName)
		tableNames = append(tableNames, tableName)
	}
	rows.Close()

	for _, tableName := range tableNames {
		db.Exec("DROP TABLE IF EXISTS " + sanitizeTableName(tableName))
		db.Exec("DELETE FROM _cache_tables WHERE table_name = ?", tableName)
	}
}

// CacheTableTTL is the maximum age for ephemeral cache tables.
const CacheTableTTL = 24 * time.Hour

// SweepExpiredTables drops tables older than the TTL from the ephemeral DB.
func SweepExpiredTables() {
	db := QueryDB()
	if db == nil {
		return
	}

	cutoff := time.Now().Add(-CacheTableTTL).Unix()
	rows, err := db.Query("SELECT table_name FROM _cache_tables WHERE created_at < ?", cutoff)
	if err != nil {
		return
	}
	var tableNames []string
	for rows.Next() {
		var tableName string
		rows.Scan(&tableName)
		tableNames = append(tableNames, tableName)
	}
	rows.Close()

	for _, tableName := range tableNames {
		db.Exec("DROP TABLE IF EXISTS " + sanitizeTableName(tableName))
		db.Exec("DELETE FROM _cache_tables WHERE table_name = ?", tableName)
	}

	if len(tableNames) > 0 {
		fmt.Fprintf(os.Stderr, "[Cache/TTL] Swept %d expired tables (older than %v)\n", len(tableNames), CacheTableTTL)
	}
}

// --- Helpers ---

// extractRecordsArray parses rawPayload as JSON and extracts the records array.
// Handles both top-level arrays and objects wrapping an array (e.g., {"records": [...]}).
func extractRecordsArray(rawPayload string) ([]map[string]interface{}, error) {
	var parsed interface{}
	if err := json.Unmarshal([]byte(rawPayload), &parsed); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	switch v := parsed.(type) {
	case []interface{}:
		return interfaceSliceToRecords(v)
	case map[string]interface{}:
		// Look for a nested array (e.g. "records", or first array value)
		if recs, ok := v["records"].([]interface{}); ok {
			return interfaceSliceToRecords(recs)
		}
		for _, val := range v {
			if arr, ok := val.([]interface{}); ok {
				return interfaceSliceToRecords(arr)
			}
		}
		return nil, fmt.Errorf("no records array found in JSON object")
	default:
		return nil, fmt.Errorf("unsupported JSON root type: %T", parsed)
	}
}

func interfaceSliceToRecords(arr []interface{}) ([]map[string]interface{}, error) {
	if len(arr) == 0 {
		return nil, nil
	}
	var records []map[string]interface{}
	for _, item := range arr {
		if obj, ok := item.(map[string]interface{}); ok {
			records = append(records, obj)
		}
	}
	return records, nil
}

// coerceValue attempts to coerce a JSON value to the target SQLite type.
// Returns nil (NULL) on coercion failure.
func coerceValue(val interface{}, targetType string) interface{} {
	if val == nil {
		return nil
	}

	switch strings.ToUpper(targetType) {
	case "INTEGER":
		switch v := val.(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case bool:
			if v {
				return int64(1)
			}
			return int64(0)
		case string:
			// Try to parse as integer
			var i int64
			if _, err := fmt.Sscanf(v, "%d", &i); err == nil {
				return i
			}
			return nil // coercion failure → NULL
		default:
			return nil
		}
	case "REAL":
		switch v := val.(type) {
		case float64:
			return v
		case int64:
			return float64(v)
		case string:
			var f float64
			if _, err := fmt.Sscanf(v, "%f", &f); err == nil {
				return f
			}
			return nil
		default:
			return nil
		}
	default: // TEXT
		switch v := val.(type) {
		case string:
			return v
		default:
			return fmt.Sprintf("%v", v)
		}
	}
}

// sanitizeTableName ensures the table name is safe for SQL.
// Only allows alphanumeric characters and underscores.
var tableNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func sanitizeTableName(name string) string {
	if tableNameRe.MatchString(name) {
		return name
	}
	// Remove non-alphanumeric/underscore characters
	cleaned := regexp.MustCompile(`[^a-zA-Z0-9_]`).ReplaceAllString(name, "_")
	if cleaned == "" || cleaned[0] >= '0' && cleaned[0] <= '9' {
		cleaned = "t_" + cleaned
	}
	return cleaned
}

func sanitizeColumnName(name string) string {
	return sanitizeTableName(name) // same rules
}

func sanitizeColumnNames(names []string) []string {
	result := make([]string, len(names))
	for i, n := range names {
		result[i] = sanitizeColumnName(n)
	}
	return result
}

// sanitizeEnvelopeFieldNames remaps all field name references in a CacheEnvelope
// to use the sanitized column names (matching the SQL table schema). Without this,
// the envelope reports original names like "Target_Account?" but the SQL table
// column is "Target_Account_", causing every model-generated query to fail.
func sanitizeEnvelopeFieldNames(env CacheEnvelope) CacheEnvelope {
	// Remap Fields slice
	sanitized := make([]string, len(env.Fields))
	for i, f := range env.Fields {
		sanitized[i] = sanitizeColumnName(f)
	}
	env.Fields = sanitized

	// Remap FieldTypes map
	newFieldTypes := make(map[string]string, len(env.FieldTypes))
	for k, v := range env.FieldTypes {
		newFieldTypes[sanitizeColumnName(k)] = v
	}
	env.FieldTypes = newFieldTypes

	// Remap SampleRecord map
	newSample := make(map[string]interface{}, len(env.SampleRecord))
	for k, v := range env.SampleRecord {
		newSample[sanitizeColumnName(k)] = v
	}
	env.SampleRecord = newSample

	// Remap EnumValues map
	newEnums := make(map[string][]string, len(env.EnumValues))
	for k, v := range env.EnumValues {
		newEnums[sanitizeColumnName(k)] = v
	}
	env.EnumValues = newEnums

	return env
}

// envelopeFieldTypesToSQLite converts CacheEnvelope.FieldTypes to SQLite column types.
func envelopeFieldTypesToSQLite(fieldTypes map[string]string) map[string]string {
	result := make(map[string]string, len(fieldTypes))
	for k, v := range fieldTypes {
		result[k] = goTypeToSQLite(v)
	}
	return result
}

// goTypeToSQLite maps Go/profiler type strings to SQLite column types.
func goTypeToSQLite(t string) string {
	switch strings.ToLower(t) {
	case "integer", "int", "int64":
		return "INTEGER"
	case "float", "float64", "real":
		return "REAL"
	case "boolean", "bool":
		return "INTEGER"
	default:
		return "TEXT"
	}
}

// extractColumnTypesFromEnvelope parses the envelope JSON and extracts column
// types suitable for SQLite DDL, handling both CacheEnvelope format (FieldTypes)
// and DataProfile format (Columns[].Type).
func extractColumnTypesFromEnvelope(envelopeJSON string) map[string]string {
	result := make(map[string]string)

	// Try CacheEnvelope format first
	var envelope struct {
		FieldTypes map[string]string `json:"fieldTypes"`
	}
	if err := json.Unmarshal([]byte(envelopeJSON), &envelope); err == nil && len(envelope.FieldTypes) > 0 {
		return envelopeFieldTypesToSQLite(envelope.FieldTypes)
	}

	// Try DataProfile format
	var profile struct {
		Columns []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"columns"`
	}
	if err := json.Unmarshal([]byte(envelopeJSON), &profile); err == nil && len(profile.Columns) > 0 {
		for _, col := range profile.Columns {
			result[col.Name] = goTypeToSQLite(col.Type)
		}
		return result
	}

	return result
}
