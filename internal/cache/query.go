package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"tzro/internal/memory"
)

const (
	sqlQueryTimeout = 5 * time.Second
	sqlMaxRows      = 500
)

// ExecuteSQL runs a SQL query against the ephemeral query database.
// It applies 3 safety layers before execution and returns results as
// a JSON array of objects, capped at 500 rows.
//
// Safety layers:
//  1. Statement type check — only SELECT is permitted
//  2. Table allowlist — only cache_* tables allowed in FROM/JOIN
//  3. Query timeout — 5-second context deadline
//
// If the materialized table is missing (TTL'd or post-restart), it
// attempts lazy re-materialization from the prod DB before failing.
func ExecuteSQL(ctx context.Context, cacheID, sqlQuery string) (string, error) {
	// Red-team FM-12 fix: Normalize PostgreSQL dialect to SQLite.
	// Cloud models (used for validator escalation) often emit PostgreSQL syntax.
	sqlQuery = normalizeSQLDialect(sqlQuery)

	// Safety Layer 1: statement type check
	if err := validateStatementType(sqlQuery); err != nil {
		return "", err
	}

	// Safety Layer 2: table allowlist
	if err := validateTableReferences(sqlQuery); err != nil {
		return "", err
	}

	db := QueryDB()
	if db == nil {
		return "", fmt.Errorf("ephemeral query DB not available")
	}

	// Safety Layer 3: timeout
	queryCtx, cancel := context.WithTimeout(ctx, sqlQueryTimeout)
	defer cancel()

	// Apply row cap if no LIMIT clause exists
	execQuery := sqlQuery
	if !hasLimitClause(sqlQuery) {
		execQuery = sqlQuery + " LIMIT 501" // fetch 501 to detect overflow
	}

	rows, err := db.QueryContext(queryCtx, execQuery)
	if err != nil {
		// Check for "no such table" → lazy re-materialization
		if strings.Contains(err.Error(), "no such table") {
			if reErr := rematerializeFromProd(cacheID); reErr != nil {
				return "", fmt.Errorf("table not found and re-materialization failed: %w (original: %v)", reErr, err)
			}
			// Retry query after re-materialization
			rows, err = db.QueryContext(queryCtx, execQuery)
			if err != nil {
				return "", fmt.Errorf("query failed after re-materialization: %w", err)
			}
		} else {
			return "", fmt.Errorf("SQL error: %w", err)
		}
	}
	defer rows.Close()

	result, totalRows, err := rowsToJSON(rows, sqlMaxRows+1)
	if err != nil {
		return "", err
	}

	// Handle row cap overflow
	if totalRows > sqlMaxRows {
		result = result[:sqlMaxRows]
		result = append(result, map[string]interface{}{
			"_note": "Showing 500 of more rows. Use LIMIT/OFFSET for pagination.",
		})
	}

	if len(result) == 0 {
		return "[]", nil
	}

	resBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal results: %w", err)
	}
	return string(resBytes), nil
}

// validateStatementType ensures only SELECT and WITH (CTE) statements are allowed.
func validateStatementType(sqlStr string) error {
	normalized := stripSQLComments(strings.TrimSpace(sqlStr))
	upper := strings.ToUpper(strings.TrimSpace(normalized))

	allowed := []string{"SELECT", "WITH"}
	for _, prefix := range allowed {
		if strings.HasPrefix(upper, prefix) {
			return nil
		}
	}
	// Truncate for error message
	display := normalized
	if len(display) > 50 {
		display = display[:50]
	}
	return fmt.Errorf("only SELECT statements are permitted, got: %s", display)
}

// validateTableReferences extracts table names from FROM and JOIN clauses
// and ensures they all have a cache_ prefix or are CTE-defined names.
func validateTableReferences(sqlStr string) error {
	tables := extractTableNames(sqlStr)
	cteNames := extractCTENames(sqlStr)
	for _, table := range tables {
		lower := strings.ToLower(table)
		// Allow cache_* tables, _cache_tables metadata, and CTE-defined names
		if !strings.HasPrefix(lower, "cache_") && lower != "_cache_tables" && !cteNames[lower] {
			return fmt.Errorf("query references disallowed table: %s (only cache_* tables permitted)", table)
		}
	}
	return nil
}

// extractTableNames uses regex to find table names after FROM and JOIN keywords.
func extractTableNames(sqlStr string) []string {
	// Match: FROM table_name, JOIN table_name
	// Handles optional aliasing (AS alias)
	re := regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
	matches := re.FindAllStringSubmatch(sqlStr, -1)

	seen := make(map[string]bool)
	var tables []string
	for _, match := range matches {
		if len(match) > 1 {
			table := match[1]
			lower := strings.ToLower(table)
			// Skip SQL keywords that might be falsely matched
			if lower == "select" || lower == "where" || lower == "on" || lower == "as" {
				continue
			}
			if !seen[lower] {
				seen[lower] = true
				tables = append(tables, table)
			}
		}
	}
	return tables
}

// extractCTENames extracts names defined in WITH clauses (CTE definitions).
func extractCTENames(sqlStr string) map[string]bool {
	result := make(map[string]bool)
	// Match: WITH name AS, name AS (after commas in multi-CTE)
	cteRe := regexp.MustCompile(`(?i)(?:WITH|,)\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+AS\s*\(`)
	matches := cteRe.FindAllStringSubmatch(sqlStr, -1)
	for _, match := range matches {
		if len(match) > 1 {
			result[strings.ToLower(match[1])] = true
		}
	}
	return result
}

// stripSQLComments removes SQL comments (-- and /* */) from a SQL string.
func stripSQLComments(sqlStr string) string {
	// Remove block comments
	blockRe := regexp.MustCompile(`/\*.*?\*/`)
	result := blockRe.ReplaceAllString(sqlStr, "")

	// Remove line comments
	lineRe := regexp.MustCompile(`--[^\n]*`)
	result = lineRe.ReplaceAllString(result, "")

	return strings.TrimSpace(result)
}

// hasLimitClause checks if the SQL query already contains a LIMIT clause.
func hasLimitClause(sqlStr string) bool {
	upper := strings.ToUpper(sqlStr)
	return regexp.MustCompile(`\bLIMIT\b`).MatchString(upper)
}

// rowsToJSON converts sql.Rows to a slice of maps, up to maxRows.
// Returns the results and the total number of rows read.
func rowsToJSON(rows *sql.Rows, maxRows int) ([]map[string]interface{}, int, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get columns: %w", err)
	}

	var results []map[string]interface{}
	count := 0

	for rows.Next() {
		if count >= maxRows {
			break
		}
		values := make([]interface{}, len(columns))
		ptrs := make([]interface{}, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, 0, fmt.Errorf("failed to scan row: %w", err)
		}

		row := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]
			// Convert []byte to string for readability
			if b, ok := val.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = val
			}
		}
		results = append(results, row)
		count++
	}

	return results, count, nil
}

// rematerializeFromProd fetches raw payload and envelope from the prod DB
// and re-materializes the table in the ephemeral query DB.
func rematerializeFromProd(cacheID string) error {
	if memory.DB == nil {
		return fmt.Errorf("production database not available")
	}
	db := memory.DB.RawDB()
	if db == nil {
		return fmt.Errorf("production database not available")
	}

	var rawPayload, envelopeJSON string
	err := db.QueryRow(
		"SELECT COALESCE(raw_payload, ''), COALESCE(envelope_json, '') FROM disk_cache WHERE cache_id = ?",
		cacheID,
	).Scan(&rawPayload, &envelopeJSON)
	if err != nil {
		return fmt.Errorf("cache_id '%s' not found in production DB: %w", cacheID, err)
	}

	if rawPayload == "" {
		// Check for file_path reference
		var filePath string
		db.QueryRow("SELECT COALESCE(file_path, '') FROM disk_cache WHERE cache_id = ?", cacheID).Scan(&filePath)
		if filePath != "" {
			store := &sqlCacheStore{}
			rawPayload = store.readFileAsJSON(filePath)
			if strings.HasPrefix(rawPayload, "Error:") {
				return fmt.Errorf("failed to read file reference: %s", rawPayload)
			}
		}
		if rawPayload == "" {
			return fmt.Errorf("no raw payload or file reference for cache_id '%s'", cacheID)
		}
	}

	columnTypes := extractColumnTypesFromEnvelope(envelopeJSON)
	return MaterializeTable(cacheID, rawPayload, columnTypes, "")
}

// UpdateTableTaskID associates a materialized table with the executing task.
// Called by the sql_cached_data handler on first query, since the task context
// isn't available at materialization time in the cache layer.
func UpdateTableTaskID(cacheID, taskID string) {
	db := QueryDB()
	if db == nil {
		return
	}
	db.Exec("UPDATE _cache_tables SET task_id = ? WHERE table_name = ? AND (task_id = '' OR task_id IS NULL)",
		taskID, cacheID)
}

// readFileAsJSON is exported for use by rematerializeFromProd.
func readFileAsJSON_bridge(filePath string) string {
	store := &sqlCacheStore{}
	return store.readFileAsJSON(filePath)
}

// Exported for backward compatibility during transition.
// Remove after all callers migrate to ExecuteSQL.
var _ = fmt.Sprintf // suppress unused import
var _ = os.Stderr   // suppress unused import

// normalizeSQLDialect rewrites PostgreSQL-specific syntax to SQLite equivalents.
// Cloud models (used for validator escalation) often emit PostgreSQL dialect
// which is incompatible with the SQLite ephemeral query DB.
//
// Red-team FM-12 fix.
func normalizeSQLDialect(sql string) string {
	// STRING_AGG(expr, sep) → GROUP_CONCAT(expr, sep)
	stringAggRe := regexp.MustCompile(`(?i)\bSTRING_AGG\s*\(`)
	sql = stringAggRe.ReplaceAllString(sql, "GROUP_CONCAT(")

	// SQLite quirk: GROUP_CONCAT(DISTINCT col, separator) is invalid —
	// DISTINCT aggregates accept only one argument. Rewrite to drop the separator.
	// Match: GROUP_CONCAT(DISTINCT expr, 'sep') → GROUP_CONCAT(DISTINCT expr)
	distinctSepRe := regexp.MustCompile(`(?i)GROUP_CONCAT\s*\(\s*DISTINCT\s+([^,)]+?)\s*,\s*'[^']*'\s*\)`)
	sql = distinctSepRe.ReplaceAllString(sql, "GROUP_CONCAT(DISTINCT $1)")

	// ILIKE → LIKE (SQLite LIKE is case-insensitive for ASCII by default)
	ilikeRe := regexp.MustCompile(`(?i)\bILIKE\b`)
	sql = ilikeRe.ReplaceAllString(sql, "LIKE")

	// BOOLEAN literals: TRUE/FALSE → 1/0
	trueRe := regexp.MustCompile(`(?i)\bTRUE\b`)
	falseRe := regexp.MustCompile(`(?i)\bFALSE\b`)
	sql = trueRe.ReplaceAllString(sql, "1")
	sql = falseRe.ReplaceAllString(sql, "0")

	return sql
}
