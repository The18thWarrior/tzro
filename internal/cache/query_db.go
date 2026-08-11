package cache

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

// MaterializeDerivedTable creates an idempotent derived table in the ephemeral
// query DB from SQL result JSON. Used to persist GROUP BY and aggregate results
// so downstream tools (top_n) can query them.
//
// The derived table name is a deterministic hash of parentCacheID + sql,
// ensuring repeated identical queries don't create duplicate tables.
// Idempotent: skips insertion if the table already has rows.
//
// ADR-0076: Deterministic Query Path.
func MaterializeDerivedTable(parentCacheID, sql, resultJSON string, taskID string) (string, error) {
	db := QueryDB()
	if db == nil {
		return "", fmt.Errorf("ephemeral query DB not available")
	}

	// Deterministic derived ID from hash of parent + SQL
	hash := sha256.Sum256([]byte(parentCacheID + sql))
	derivedID := "cache_derived_" + hex.EncodeToString(hash[:8]) // 16-char hex suffix

	// Parse result JSON
	var records []map[string]interface{}
	if err := json.Unmarshal([]byte(resultJSON), &records); err != nil {
		// Try extracting from wrapped format
		extracted, extractErr := extractRecordsArray(resultJSON)
		if extractErr != nil {
			return "", fmt.Errorf("failed to parse result JSON: %w", err)
		}
		records = extracted
	}
	if len(records) == 0 {
		return "", fmt.Errorf("no records to materialize")
	}

	// Collect columns from first record
	var columns []string
	for k := range records[0] {
		columns = append(columns, k)
	}

	tableName := sanitizeTableName(derivedID)

	// Build CREATE TABLE — all columns as TEXT for derived tables
	var colDefs []string
	for _, col := range columns {
		colDefs = append(colDefs, fmt.Sprintf("%s TEXT", sanitizeColumnName(col)))
	}
	createSQL := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", tableName, strings.Join(colDefs, ", "))

	tx, err := db.Begin()
	if err != nil {
		return "", fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Create the table
	if _, err := tx.Exec(createSQL); err != nil {
		return "", fmt.Errorf("failed to create derived table: %w", err)
	}

	// Idempotency check: skip insertion if table already has rows
	var existingRows int
	if err := tx.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName)).Scan(&existingRows); err == nil && existingRows > 0 {
		// Table already populated — commit (to finalize CREATE IF NOT EXISTS) and return
		tx.Commit()
		fmt.Fprintf(os.Stderr, "[Cache/MaterializeDerived] Skipping insert — %s already has %d rows\n", derivedID, existingRows)
		return derivedID, nil
	}

	// Prepare INSERT
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
		return "", fmt.Errorf("failed to prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, record := range records {
		values := make([]interface{}, len(columns))
		for i, col := range columns {
			val, exists := record[col]
			if !exists || val == nil {
				values[i] = nil
			} else {
				values[i] = fmt.Sprintf("%v", val)
			}
		}
		if _, err := stmt.Exec(values...); err != nil {
			fmt.Fprintf(os.Stderr, "[Cache/MaterializeDerived] Row insert warning: %v\n", err)
		}
	}

	// Register metadata
	createdAt := time.Now().Unix()
	if _, err := tx.Exec(`INSERT OR REPLACE INTO _cache_tables (table_name, task_id, created_at) VALUES (?, ?, ?)`,
		derivedID, taskID, createdAt); err != nil {
		return "", fmt.Errorf("failed to insert metadata: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("failed to commit: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[Cache/MaterializeDerived] Created %s with %d rows from %s\n", derivedID, len(records), parentCacheID)
	return derivedID, nil
}

// GetCacheColumns returns the column names from a materialized cache table.
// Returns nil if the table or query DB is unavailable (non-fatal).
func GetCacheColumns(cacheID string) []string {
	db := QueryDB()
	if db == nil {
		return nil
	}
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info([%s])", cacheID))
	if err != nil {
		return nil
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var cid int
		var name, dtype string
		var notnull int
		var dfltValue *string
		var pk int
		if err := rows.Scan(&cid, &name, &dtype, &notnull, &dfltValue, &pk); err != nil {
			continue
		}
		cols = append(cols, name)
	}
	return cols
}

// GetCacheSampleValues returns up to `limit` distinct values per column for a
// materialized cache table. Used to anchor the analyze query phase prompt with
// concrete data values, preventing the model from guessing filter strings.
//
// Red-team FM-10 fix: value-aware query composition.
func GetCacheSampleValues(cacheID string, columns []string, limit int) map[string][]string {
	db := QueryDB()
	if db == nil {
		return nil
	}
	if limit <= 0 {
		limit = 15
	}

	result := make(map[string][]string, len(columns))
	for _, col := range columns {
		// Skip the internal _rowid column if present
		if col == "_rowid" {
			continue
		}
		query := fmt.Sprintf("SELECT DISTINCT [%s] FROM [%s] WHERE [%s] IS NOT NULL AND [%s] != '' LIMIT %d",
			col, cacheID, col, col, limit)
		rows, err := db.Query(query)
		if err != nil {
			continue
		}
		var values []string
		for rows.Next() {
			var val string
			if err := rows.Scan(&val); err != nil {
				continue
			}
			// Skip very long values (>50 chars) — they're not useful for filter matching
			if len(val) <= 50 {
				values = append(values, val)
			}
		}
		rows.Close()
		if len(values) > 0 {
			result[col] = values
		}
	}
	return result
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
	payload := rawPayload

	// First attempt: try direct JSON parse
	var parsed interface{}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		// FM-20 fix v3: If direct parse fails, strip non-JSON prefix.
		// The FM-19 summary prefix "[Filter result: N rows...]" looks like a
		// JSON array start but isn't. json.MarshalIndent produces "[\n  {\n"
		// (with whitespace between '[' and '{'), so simple "[{" won't match.
		//
		// Strategy: find the last occurrence of "]\n\n" (end of prefix line +
		// blank line separator) and try parsing from there. Fall back to
		// scanning for '{"' as a universal JSON object marker.
		found := false

		// Try 1: Split on double-newline after the prefix bracket
		if idx := strings.Index(payload, "]\n\n"); idx > 0 {
			trimmed := strings.TrimSpace(payload[idx+3:])
			if json.Unmarshal([]byte(trimmed), &parsed) == nil {
				found = true
			}
		}

		// Try 2: Find first '{' followed by '"' (JSON object start)
		if !found {
			if idx := strings.Index(payload, `{"`); idx > 0 {
				// Walk back to find the array '[' if present
				arrayStart := strings.LastIndex(payload[:idx], "[")
				startIdx := idx
				if arrayStart > 0 {
					// Check if only whitespace between '[' and '{"'
					between := strings.TrimSpace(payload[arrayStart+1 : idx])
					if between == "" {
						startIdx = arrayStart
					}
				}
				trimmed := payload[startIdx:]
				if json.Unmarshal([]byte(trimmed), &parsed) == nil {
					found = true
				}
			}
		}

		if !found {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
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
	cleaned := sanitizeTableName(name)
	// SQLite reserved words that can appear as CSV/JSON column names.
	// When used unquoted in CREATE TABLE or queries, they cause syntax errors.
	// Suffix with underscore to disambiguate (e.g., "Index" → "Index_").
	reserved := map[string]bool{
		"index": true, "order": true, "group": true, "select": true,
		"table": true, "column": true, "where": true, "from": true,
		"insert": true, "update": true, "delete": true, "create": true,
		"drop": true, "alter": true, "values": true, "set": true,
		"into": true, "join": true, "on": true, "as": true,
		"limit": true, "offset": true, "having": true, "between": true,
		"like": true, "in": true, "is": true, "not": true,
		"null": true, "case": true, "when": true, "then": true,
		"else": true, "end": true, "and": true, "or": true,
		"default": true, "check": true, "primary": true, "key": true,
		"unique": true, "foreign": true, "references": true, "constraint": true,
		"transaction": true, "begin": true, "commit": true, "rollback": true,
		"replace": true, "exists": true, "distinct": true, "all": true,
	}
	if reserved[strings.ToLower(cleaned)] {
		cleaned = cleaned + "_"
	}
	return cleaned
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
