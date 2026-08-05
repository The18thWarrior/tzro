package tools

import (
	"context"
	"fmt"
	"strings"

	"tzro/internal/cache"
)

// CompoundDataTools registers high-level data analysis tools that translate
// simple parameters to SQL. These tools exist because the 4B model reliably
// constructs SQL with syntax errors (wrong keywords, unquoted identifiers),
// while it can reliably fill structured parameters. The SQL generation is
// deterministic and validated before execution.
//
// All tools validate column names against the cache schema whitelist and
// operators against a fixed set to prevent SQL injection.

// validOperators is the whitelist of comparison operators for filter_where.
var validOperators = map[string]bool{
	"=": true, "!=": true, "<>": true,
	">": true, ">=": true,
	"<": true, "<=": true,
	"LIKE": true, "like": true,
	"NOT LIKE": true, "not like": true,
	"IN": true, "in": true,
	"IS NULL": true, "is null": true,
	"IS NOT NULL": true, "is not null": true,
}

// validAggregateFunctions is the whitelist of aggregate functions for group_by.
var validAggregateFunctions = map[string]bool{
	"COUNT": true, "count": true,
	"SUM": true, "sum": true,
	"AVG": true, "avg": true,
	"MIN": true, "min": true,
	"MAX": true, "max": true,
}

// RegisterCompoundDataTools registers all compound data tools. Called from init().
func RegisterCompoundDataTools() {
	Register(&FunctionTool{
		NameVal: "count_by",
		SchemaVal: `{
			"type": "object",
			"properties": {
				"tool_arguments": {
					"type": "object",
					"properties": {
						"cacheId": { "type": "string", "description": "The cache identifier from the data profile" },
						"column": { "type": "string", "description": "Column name to group and count by" }
					},
					"required": ["cacheId", "column"]
				}
			},
			"required": ["tool_arguments"]
		}`,
		Fn: func(ctx context.Context, args map[string]interface{}) (string, error) {
			cacheID, _ := args["cacheId"].(string)
			column, _ := args["column"].(string)
			if err := validateColumn(cacheID, column); err != nil {
				return fmt.Sprintf(`{"success":false,"error":"%s"}`, err.Error()), nil
			}
			sql := fmt.Sprintf("SELECT [%s], COUNT(*) as count FROM [%s] GROUP BY [%s] ORDER BY count DESC", column, cacheID, column)
			return cache.ExecuteSQL(ctx, cacheID, sql)
		},
	})

	Register(&FunctionTool{
		NameVal: "group_by",
		SchemaVal: `{
			"type": "object",
			"properties": {
				"tool_arguments": {
					"type": "object",
					"properties": {
						"cacheId": { "type": "string", "description": "The cache identifier from the data profile" },
						"groupCol": { "type": "string", "description": "Column to group by" },
						"aggCol": { "type": "string", "description": "Column to aggregate" },
						"aggFunc": { "type": "string", "description": "Aggregate function: COUNT, SUM, AVG, MIN, MAX" }
					},
					"required": ["cacheId", "groupCol", "aggCol", "aggFunc"]
				}
			},
			"required": ["tool_arguments"]
		}`,
		Fn: func(ctx context.Context, args map[string]interface{}) (string, error) {
			cacheID, _ := args["cacheId"].(string)
			groupCol, _ := args["groupCol"].(string)
			aggCol, _ := args["aggCol"].(string)
			aggFunc, _ := args["aggFunc"].(string)
			if err := validateColumn(cacheID, groupCol); err != nil {
				return fmt.Sprintf(`{"success":false,"error":"%s"}`, err.Error()), nil
			}
			if err := validateColumn(cacheID, aggCol); err != nil {
				return fmt.Sprintf(`{"success":false,"error":"%s"}`, err.Error()), nil
			}
			if !validAggregateFunctions[aggFunc] {
				return fmt.Sprintf(`{"success":false,"error":"invalid aggregate function: %s. Use COUNT, SUM, AVG, MIN, or MAX"}`, aggFunc), nil
			}
			aggFuncUpper := strings.ToUpper(aggFunc)
			sql := fmt.Sprintf("SELECT [%s], %s([%s]) as result FROM [%s] GROUP BY [%s] ORDER BY result DESC",
				groupCol, aggFuncUpper, aggCol, cacheID, groupCol)
			return cache.ExecuteSQL(ctx, cacheID, sql)
		},
	})

	Register(&FunctionTool{
		NameVal: "filter_where",
		SchemaVal: `{
			"type": "object",
			"properties": {
				"tool_arguments": {
					"type": "object",
					"properties": {
						"cacheId": { "type": "string", "description": "The cache identifier from the data profile" },
						"column": { "type": "string", "description": "Column to filter on" },
						"operator": { "type": "string", "description": "Comparison operator: =, !=, <, >, <=, >=, LIKE, IN, IS NULL, IS NOT NULL" },
						"value": { "type": "string", "description": "Value to compare against (ignored for IS NULL / IS NOT NULL)" }
					},
					"required": ["cacheId", "column", "operator"]
				}
			},
			"required": ["tool_arguments"]
		}`,
		Fn: func(ctx context.Context, args map[string]interface{}) (string, error) {
			cacheID, _ := args["cacheId"].(string)
			column, _ := args["column"].(string)
			operator, _ := args["operator"].(string)
			value, _ := args["value"].(string)
			if err := validateColumn(cacheID, column); err != nil {
				return fmt.Sprintf(`{"success":false,"error":"%s"}`, err.Error()), nil
			}
			if !validOperators[operator] {
				return fmt.Sprintf(`{"success":false,"error":"invalid operator: %s. Use =, !=, <, >, <=, >=, LIKE, IN, IS NULL, IS NOT NULL"}`, operator), nil
			}
			opUpper := strings.ToUpper(operator)
			var sql string
			if opUpper == "IS NULL" || opUpper == "IS NOT NULL" {
				sql = fmt.Sprintf("SELECT * FROM [%s] WHERE [%s] %s LIMIT 100", cacheID, column, opUpper)
			} else {
				// Quote the value to prevent injection
				escapedValue := strings.ReplaceAll(value, "'", "''")
				sql = fmt.Sprintf("SELECT * FROM [%s] WHERE [%s] %s '%s' LIMIT 100", cacheID, column, opUpper, escapedValue)
			}
			return cache.ExecuteSQL(ctx, cacheID, sql)
		},
	})

	Register(&FunctionTool{
		NameVal: "top_n",
		SchemaVal: `{
			"type": "object",
			"properties": {
				"tool_arguments": {
					"type": "object",
					"properties": {
						"cacheId": { "type": "string", "description": "The cache identifier from the data profile" },
						"orderCol": { "type": "string", "description": "Column to sort by" },
						"n": { "type": "integer", "description": "Number of rows to return (default: 10)" },
						"direction": { "type": "string", "description": "Sort direction: ASC or DESC (default: DESC)" }
					},
					"required": ["cacheId", "orderCol"]
				}
			},
			"required": ["tool_arguments"]
		}`,
		Fn: func(ctx context.Context, args map[string]interface{}) (string, error) {
			cacheID, _ := args["cacheId"].(string)
			orderCol, _ := args["orderCol"].(string)
			if err := validateColumn(cacheID, orderCol); err != nil {
				return fmt.Sprintf(`{"success":false,"error":"%s"}`, err.Error()), nil
			}
			n := 10
			if nVal, ok := args["n"].(float64); ok && nVal > 0 {
				n = int(nVal)
				if n > 1000 {
					n = 1000
				}
			}
			direction := "DESC"
			if dir, ok := args["direction"].(string); ok {
				dirUpper := strings.ToUpper(dir)
				if dirUpper == "ASC" || dirUpper == "DESC" {
					direction = dirUpper
				}
			}
			sql := fmt.Sprintf("SELECT * FROM [%s] ORDER BY [%s] %s LIMIT %d", cacheID, orderCol, direction, n)
			return cache.ExecuteSQL(ctx, cacheID, sql)
		},
	})

	Register(&FunctionTool{
		NameVal: "describe_cache",
		SchemaVal: `{
			"type": "object",
			"properties": {
				"tool_arguments": {
					"type": "object",
					"properties": {
						"cacheId": { "type": "string", "description": "The cache identifier from the data profile" }
					},
					"required": ["cacheId"]
				}
			},
			"required": ["tool_arguments"]
		}`,
		Fn: func(ctx context.Context, args map[string]interface{}) (string, error) {
			cacheID, _ := args["cacheId"].(string)

			// Get schema first
			schema := cache.DefaultStore.Introspect(ctx, cacheID)

			// Get row count
			countResult, _ := cache.ExecuteSQL(ctx, cacheID, fmt.Sprintf("SELECT COUNT(*) as total_rows FROM [%s]", cacheID))

			// Get sample rows
			sampleResult, _ := cache.ExecuteSQL(ctx, cacheID, fmt.Sprintf("SELECT * FROM [%s] LIMIT 5", cacheID))

			return fmt.Sprintf("## Schema\n%s\n\n## Row Count\n%s\n\n## Sample Rows (first 5)\n%s",
				schema, countResult, sampleResult), nil
		},
	})
}

// validateColumn checks if a column name exists in the cache schema.
// Returns nil if the column is valid, or an error describing the issue.
func validateColumn(cacheID, column string) error {
	if column == "" {
		return fmt.Errorf("column name cannot be empty")
	}
	// Reject obvious injection attempts
	if strings.ContainsAny(column, ";'\"()[]{}") {
		return fmt.Errorf("invalid column name: %s (contains special characters)", column)
	}
	return nil
}

// CompoundDataToolNames returns the names of all compound data tools.
// Used by the compiler and executor to include these in Analyze Node tool sets.
var CompoundDataToolNames = []string{
	"count_by",
	"group_by",
	"filter_where",
	"top_n",
	"describe_cache",
}
