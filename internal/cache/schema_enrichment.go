package cache

import (
	"database/sql"
	"fmt"
)

// SchemaEnrichment provides per-column metadata for data task query generation (FM2).
// Enrichments help the local model write correct queries by exposing cardinality,
// non-null counts, and top values — information that the raw column name alone
// doesn't convey.
type SchemaEnrichment struct {
	ColumnName   string       `json:"columnName"`
	DataType     string       `json:"dataType"`
	NonNullCount int          `json:"nonNullCount"`
	TotalCount   int          `json:"totalCount"`
	Cardinality  int          `json:"cardinality"`             // unique value count
	TopValues    []ValueCount `json:"topValues,omitempty"`     // top N values with counts
}

// ValueCount pairs a column value with its occurrence count.
type ValueCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// EnrichSchema analyzes a materialized cache table and produces per-column
// schema enrichment metadata. This runs against the ephemeral query database
// where cache data is materialized as SQLite tables.
//
// Returns enrichment for each column with: total count, non-null count,
// cardinality (distinct values), and top 5 values for TEXT columns.
func EnrichSchema(db *sql.DB, tableName string) ([]SchemaEnrichment, error) {
	// Get column names and types via PRAGMA
	pragma := fmt.Sprintf("PRAGMA table_info(%s)", tableName)
	rows, err := db.Query(pragma)
	if err != nil {
		return nil, fmt.Errorf("failed to query table info: %w", err)
	}
	defer rows.Close()

	type colInfo struct {
		name     string
		dataType string
	}
	var columns []colInfo

	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return nil, fmt.Errorf("failed to scan column info: %w", err)
		}
		columns = append(columns, colInfo{name: name, dataType: colType})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating column info: %w", err)
	}

	enrichments := make([]SchemaEnrichment, 0, len(columns))

	for _, col := range columns {
		e := SchemaEnrichment{
			ColumnName: col.name,
			DataType:   col.dataType,
		}

		// Query metrics for this column
		metricsQuery := fmt.Sprintf(
			`SELECT COUNT(*) as total, COUNT("%s") as non_null, COUNT(DISTINCT "%s") as cardinality FROM "%s"`,
			col.name, col.name, tableName,
		)
		row := db.QueryRow(metricsQuery)
		if err := row.Scan(&e.TotalCount, &e.NonNullCount, &e.Cardinality); err != nil {
			return nil, fmt.Errorf("failed to query metrics for column %s: %w", col.name, err)
		}

		// Get top values for TEXT columns (skip for numeric types with high cardinality)
		if isTextType(col.dataType) && e.Cardinality > 0 && e.Cardinality <= 1000 {
			topQuery := fmt.Sprintf(
				`SELECT "%s", COUNT(*) as cnt FROM "%s" WHERE "%s" IS NOT NULL GROUP BY "%s" ORDER BY cnt DESC LIMIT 5`,
				col.name, tableName, col.name, col.name,
			)
			topRows, err := db.Query(topQuery)
			if err == nil {
				defer topRows.Close()
				for topRows.Next() {
					var vc ValueCount
					if err := topRows.Scan(&vc.Value, &vc.Count); err == nil {
						e.TopValues = append(e.TopValues, vc)
					}
				}
			}
		}

		enrichments = append(enrichments, e)
	}

	return enrichments, nil
}

// isTextType returns true if the SQLite column type is text-like.
func isTextType(colType string) bool {
	switch colType {
	case "TEXT", "text", "VARCHAR", "varchar", "CHAR", "char":
		return true
	default:
		return false
	}
}
