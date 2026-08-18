package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tzro/internal/cache"
)

// query_builder.go — Composite data analysis tool that accepts structured
// operations and deterministically assembles SQL for cache queries.
//
// Replaces raw sql_cached_data generation (which the 4B model consistently
// fails at) with a structured, composable interface. The model provides
// intent via typed operations; this tool handles SQL construction.
//
// ADR-0074: Structured Query Composition

// QuerySpec describes a complete analytical query as composable operations.
type QuerySpec struct {
	CacheID    string      `json:"cacheId"`
	Operations []Operation `json:"operations"`
	Limit      int         `json:"limit,omitempty"` // default 100, max 1000
}

// Operation is a single composable step in a query pipeline.
type Operation struct {
	Type      string   `json:"type"`                // filter, group_by, aggregate, order_by, select
	Column    string   `json:"column,omitempty"`     // target column
	Operator  string   `json:"operator,omitempty"`   // for filter: =, !=, LIKE, IS NULL, etc.
	Value     string   `json:"value,omitempty"`      // for filter comparison value
	Function  string   `json:"function,omitempty"`   // for aggregate: COUNT, SUM, AVG, MIN, MAX, GROUP_CONCAT
	Distinct  bool     `json:"distinct,omitempty"`   // for aggregate: DISTINCT modifier
	Alias     string   `json:"alias,omitempty"`      // for aggregate: output column alias
	Direction string   `json:"direction,omitempty"`  // for order_by: ASC/DESC (default: DESC)
	Columns   []string `json:"columns,omitempty"`    // for select: specific columns to return
}

// validQueryAggregateFunctions extends the compound_data whitelist with GROUP_CONCAT, PERCENTAGE, and RATIO.
var validQueryAggregateFunctions = map[string]bool{
	"COUNT":        true, "count":        true,
	"SUM":          true, "sum":          true,
	"AVG":          true, "avg":          true,
	"MIN":          true, "min":          true,
	"MAX":          true, "max":          true,
	"GROUP_CONCAT": true, "group_concat": true,
	"PERCENTAGE":   true, "percentage":   true,
	"RATIO":        true, "ratio":        true,
}

// BuildSQL constructs a SQL SELECT statement from a QuerySpec.
// Returns (sql, error). The SQL uses bracket-escaped column names
// to safely handle special characters (e.g., "Target_Account?").
//
// Exported for testing — the query_builder tool wraps this with cache.ExecuteSQL.
func BuildSQL(spec QuerySpec) (string, error) {
	if spec.CacheID == "" {
		return "", fmt.Errorf("cacheId is required")
	}

	limit := spec.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	// Collect clause components from operations
	// Collect clause components from operations
	var (
		selectCols        []string
		filters           []string
		groupByCols       []string
		groupBySelectCols []string
		aggregates        []string
		orderClauses      []string
	)

	for _, op := range spec.Operations {
		switch op.Type {
		case "filter":
			clause, err := buildFilterClause(op)
			if err != nil {
				return "", fmt.Errorf("filter operation error: %w", err)
			}
			filters = append(filters, clause)

		case "group_by":
			if op.Column == "" {
				return "", fmt.Errorf("group_by operation requires a column")
			}
			imputedExpr := fmt.Sprintf("COALESCE(NULLIF([%s], ''), '(Unspecified)')", op.Column)
			groupByCols = append(groupByCols, imputedExpr)
			groupBySelectCols = append(groupBySelectCols, fmt.Sprintf("%s AS [%s]", imputedExpr, op.Column))

		case "aggregate":
			agg, err := buildAggregateClause(op)
			if err != nil {
				return "", fmt.Errorf("aggregate operation error: %w", err)
			}
			aggregates = append(aggregates, agg)

		case "order_by":
			clause := buildOrderByClause(op)
			orderClauses = append(orderClauses, clause)

		case "select":
			for _, col := range op.Columns {
				selectCols = append(selectCols, fmt.Sprintf("[%s]", col))
			}

		default:
			return "", fmt.Errorf("unknown operation type: %s", op.Type)
		}
	}

	// Assemble SELECT clause
	var selectClause string
	if len(groupBySelectCols) > 0 {
		// When GROUP BY is used, SELECT = imputed group columns + aggregates
		parts := make([]string, 0, len(groupBySelectCols)+len(aggregates))
		parts = append(parts, groupBySelectCols...)
		parts = append(parts, aggregates...)
		if len(parts) == 0 {
			selectClause = "*"
		} else {
			selectClause = strings.Join(parts, ", ")
		}
	} else if len(selectCols) > 0 {
		selectClause = strings.Join(selectCols, ", ")
	} else {
		selectClause = "*"
	}

	// Build the full SQL statement
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("SELECT %s FROM [%s]", selectClause, spec.CacheID))

	if len(filters) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(filters, " AND "))
	}

	if len(groupByCols) > 0 {
		sb.WriteString(" GROUP BY ")
		sb.WriteString(strings.Join(groupByCols, ", "))
	}

	if len(orderClauses) > 0 {
		sb.WriteString(" ORDER BY ")
		sb.WriteString(strings.Join(orderClauses, ", "))
	}

	sb.WriteString(fmt.Sprintf(" LIMIT %d", limit))

	return sb.String(), nil
}

// buildFilterClause constructs a single WHERE condition.
func buildFilterClause(op Operation) (string, error) {
	if op.Column == "" {
		return "", fmt.Errorf("filter requires a column")
	}

	opUpper := strings.ToUpper(strings.TrimSpace(op.Operator))
	if !validOperators[op.Operator] && !validOperators[opUpper] {
		return "", fmt.Errorf("invalid operator: %s", op.Operator)
	}

	// IS NULL / IS NOT NULL — no value needed
	if opUpper == "IS NULL" || opUpper == "IS NOT NULL" {
		return fmt.Sprintf("[%s] %s", op.Column, opUpper), nil
	}

	// Standard comparison — escape value for SQL injection prevention
	escapedValue := strings.ReplaceAll(op.Value, "'", "''")
	// Also escape semicolons to prevent statement injection
	escapedValue = strings.ReplaceAll(escapedValue, ";", "")

	return fmt.Sprintf("[%s] %s '%s'", op.Column, opUpper, escapedValue), nil
}

// buildAggregateClause constructs a single aggregate expression.
func buildAggregateClause(op Operation) (string, error) {
	funcUpper := strings.ToUpper(strings.TrimSpace(op.Function))
	if !validQueryAggregateFunctions[funcUpper] {
		return "", fmt.Errorf("invalid aggregate function: %s", op.Function)
	}

	var expr string
	if funcUpper == "PERCENTAGE" || funcUpper == "RATIO" {
		expr = "ROUND(COUNT(*) * 100.0 / SUM(COUNT(*)) OVER(), 2)"
	} else if funcUpper == "COUNT" && (op.Column == "" || op.Column == "*") {
		expr = "COUNT(*)"
	} else if funcUpper == "GROUP_CONCAT" {
		if op.Column == "" {
			return "", fmt.Errorf("GROUP_CONCAT requires a column")
		}
		if op.Distinct {
			expr = fmt.Sprintf("GROUP_CONCAT(DISTINCT [%s])", op.Column)
		} else {
			expr = fmt.Sprintf("GROUP_CONCAT([%s])", op.Column)
		}
	} else if op.Column != "" {
		if op.Distinct {
			expr = fmt.Sprintf("%s(DISTINCT [%s])", funcUpper, op.Column)
		} else {
			expr = fmt.Sprintf("%s([%s])", funcUpper, op.Column)
		}
	} else {
		expr = fmt.Sprintf("%s(*)", funcUpper)
	}

	if op.Alias != "" {
		expr += fmt.Sprintf(" AS %s", op.Alias)
	}

	return expr, nil
}

// buildOrderByClause constructs a single ORDER BY expression.
func buildOrderByClause(op Operation) string {
	direction := "DESC" // default
	if strings.ToUpper(op.Direction) == "ASC" {
		direction = "ASC"
	}

	col := op.Column
	// If the column matches an aggregate alias (no brackets), keep it bare.
	// Otherwise bracket-escape it as a table column reference.
	if !strings.Contains(col, "(") && col != "" {
		// Check if it looks like an alias (no special chars that need escaping)
		// Aliases from aggregates are bare identifiers — don't bracket them
		// if they don't contain spaces or special chars.
		if strings.ContainsAny(col, " ?!@#$%") {
			col = fmt.Sprintf("[%s]", col)
		}
	}

	return fmt.Sprintf("%s %s", col, direction)
}

// queryBuilderSchema is the JSON schema for the query_builder tool.
const queryBuilderSchema = `{
	"type": "object",
	"properties": {
		"tool_arguments": {
			"type": "object",
			"properties": {
				"cacheId": {
					"type": "string",
					"description": "The cache identifier from the data profile (e.g., cache_1234567890)"
				},
				"operations": {
					"type": "array",
					"description": "Composable query operations to build the SQL statement",
					"items": {
						"type": "object",
						"properties": {
							"type": {
								"type": "string",
								"enum": ["filter", "group_by", "aggregate", "order_by", "select"],
								"description": "Operation type"
							},
							"column": {
								"type": "string",
								"description": "Target column name (use exact column names from schema)"
							},
							"operator": {
								"type": "string",
								"description": "Comparison operator for filter: =, !=, <, >, <=, >=, LIKE, IN, IS NULL, IS NOT NULL"
							},
							"value": {
								"type": "string",
								"description": "Comparison value for filter"
							},
							"function": {
								"type": "string",
								"enum": ["COUNT", "SUM", "AVG", "MIN", "MAX", "GROUP_CONCAT"],
								"description": "Aggregate function"
							},
							"distinct": {
								"type": "boolean",
								"description": "Apply DISTINCT modifier to aggregate"
							},
							"alias": {
								"type": "string",
								"description": "Output column alias for aggregate results"
							},
							"direction": {
								"type": "string",
								"enum": ["ASC", "DESC"],
								"description": "Sort direction for order_by (default: DESC)"
							},
							"columns": {
								"type": "array",
								"items": { "type": "string" },
								"description": "Column names for select operation"
							}
						},
						"required": ["type"]
					}
				},
				"limit": {
					"type": "integer",
					"description": "Maximum rows to return (default: 100, max: 1000)"
				}
			},
			"required": ["cacheId", "operations"]
		}
	},
	"required": ["tool_arguments"]
}`

// NewQueryBuilderTool creates the query_builder FunctionTool.
func NewQueryBuilderTool() *FunctionTool {
	return &FunctionTool{
		NameVal:   "query_builder",
		SchemaVal: queryBuilderSchema,
		Fn: func(ctx context.Context, args map[string]interface{}) (string, error) {
			cacheID, _ := args["cacheId"].(string)

			// Parse operations from the args
			opsRaw, _ := args["operations"].([]interface{})
			var operations []Operation
			for _, opRaw := range opsRaw {
				opBytes, err := json.Marshal(opRaw)
				if err != nil {
					continue
				}
				var op Operation
				if err := json.Unmarshal(opBytes, &op); err != nil {
					continue
				}
				operations = append(operations, op)
			}

			limit := 100
			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			spec := QuerySpec{
				CacheID:    cacheID,
				Operations: operations,
				Limit:      limit,
			}

			sql, err := BuildSQL(spec)
			if err != nil {
				return fmt.Sprintf(`{"success":false,"error":"%s"}`, err.Error()), nil
			}

			return cache.ExecuteSQL(ctx, cacheID, sql)
		},
	}
}
