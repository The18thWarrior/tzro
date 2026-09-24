package executor

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"tzro/pkg/ast"
	"tzro/pkg/probe"
	"tzro/pkg/store"
)

// BuiltinDispatcher executes tzro built-in tools directly in-process.
type BuiltinDispatcher struct {
	WorkspaceRoot string
	StoreDB       *store.Store
}

// NewBuiltinDispatcher creates a new in-process tool dispatcher.
func NewBuiltinDispatcher(workspaceRoot string, s *store.Store) *BuiltinDispatcher {
	return &BuiltinDispatcher{
		WorkspaceRoot: workspaceRoot,
		StoreDB:       s,
	}
}

// Dispatch routes a tool call to the appropriate in-process handler.
func (b *BuiltinDispatcher) Dispatch(ctx context.Context, tool string, args map[string]interface{}) (map[string]interface{}, error) {
	switch tool {
	case "bash":
		return b.dispatchBash(ctx, args)
	case "probe":
		return b.dispatchProbe(ctx, args)
	case "skeleton":
		return b.dispatchSkeleton(ctx, args)
	case "search":
		return b.dispatchSearch(ctx, args)
	case "ingest":
		return b.dispatchIngest(ctx, args)
	case "query":
		return b.dispatchQuery(ctx, args)
	default:
		return nil, fmt.Errorf("unknown tool: %q", tool)
	}
}

// dispatchBash executes a sandboxed bash command.
func (b *BuiltinDispatcher) dispatchBash(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	cmdStr, _ := args["command"].(string)
	if cmdStr == "" {
		return nil, fmt.Errorf("bash tool requires 'command' argument")
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("bash execution error: %w", err)
		}
	}

	return map[string]interface{}{
		"stdout":    stdout.String(),
		"stderr":    stderr.String(),
		"exit_code": exitCode,
	}, nil
}

// dispatchProbe runs in-process symbol discovery via pkg/probe.
func (b *BuiltinDispatcher) dispatchProbe(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("probe tool requires 'query' argument")
	}

	maxResults := 10
	if mr, ok := args["max_results"].(float64); ok {
		maxResults = int(mr)
	}

	workspace := b.WorkspaceRoot
	if ws, ok := args["workspace"].(string); ok && ws != "" {
		workspace = ws
	}

	report, err := probe.Probe(workspace, query, maxResults, b.StoreDB)
	if err != nil {
		return nil, fmt.Errorf("probe failed: %w", err)
	}

	// Convert matches to generic interface for JSON serialization
	matches := make([]interface{}, len(report.Matches))
	for i, m := range report.Matches {
		matches[i] = map[string]interface{}{
			"file_path":     m.FilePath,
			"symbol_name":   m.SymbolName,
			"kind":          m.Kind,
			"start_line":    m.StartLine,
			"end_line":      m.EndLine,
			"matching_line": m.MatchingLine,
			"hash":          m.Hash,
		}
	}

	return map[string]interface{}{
		"query":         report.Query,
		"matches":       matches,
		"scanned_files": report.ScannedFiles,
		"duration_ms":   report.DurationMs,
		"markdown":      report.FormatMarkdown(),
	}, nil
}

// dispatchSkeleton runs in-process AST skeletonization via pkg/ast.
func (b *BuiltinDispatcher) dispatchSkeleton(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	filePath, _ := args["file"].(string)
	if filePath == "" {
		return nil, fmt.Errorf("skeleton tool requires 'file' argument")
	}

	source, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading file %q: %w", filePath, err)
	}

	workspace := b.WorkspaceRoot
	result, err := ast.Skeletonize(filePath, source, b.StoreDB, workspace)
	if err != nil {
		return nil, fmt.Errorf("skeletonize failed: %w", err)
	}

	return map[string]interface{}{
		"skeleton":      result.SkeletonCode,
		"original_size": result.OriginalSize,
		"skeleton_size": result.SkeletonSize,
		"savings_ratio": result.SavingsRatio,
		"elided_blocks": result.ElidedBlocks,
		"hashes":        result.Hashes,
	}, nil
}

// dispatchSearch runs a unified evidence search via pkg/search.
func (b *BuiltinDispatcher) dispatchSearch(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("search tool requires 'query' argument")
	}

	// Search requires more setup — delegate to the search engine
	// For now, fall back to probe for basic discovery
	return b.dispatchProbe(ctx, map[string]interface{}{
		"query":       query,
		"max_results": args["max_results"],
	})
}

// dispatchIngest ingests tabular data into SQLite.
func (b *BuiltinDispatcher) dispatchIngest(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	filePath, _ := args["file"].(string)
	if filePath == "" {
		return nil, fmt.Errorf("ingest tool requires 'file' argument")
	}

	tableName, _ := args["table"].(string)
	if tableName == "" {
		// Derive table name from filename
		base := filepath.Base(filePath)
		tableName = strings.TrimSuffix(base, filepath.Ext(base))
		tableName = strings.ReplaceAll(tableName, "-", "_")
		tableName = strings.ReplaceAll(tableName, " ", "_")
	}

	columns, rows, err := parseTabularFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("parsing tabular file: %w", err)
	}

	if b.StoreDB == nil {
		return nil, fmt.Errorf("no store configured for ingest")
	}

	if err := b.StoreDB.ImportTabular(tableName, columns, rows); err != nil {
		return nil, fmt.Errorf("importing tabular data: %w", err)
	}

	return map[string]interface{}{
		"table":     tableName,
		"columns":   columns,
		"row_count": len(rows),
	}, nil
}

// dispatchQuery runs a SQL query, auto-ingesting a file if provided.
func (b *BuiltinDispatcher) dispatchQuery(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	sqlStr, _ := args["sql"].(string)
	if sqlStr == "" {
		return nil, fmt.Errorf("query tool requires 'sql' argument")
	}

	if b.StoreDB == nil {
		return nil, fmt.Errorf("no store configured for query")
	}

	// Auto-ingest if a file is provided
	if filePath, ok := args["file"].(string); ok && filePath != "" {
		tableName := ""
		// Derive table name from filename
		base := filepath.Base(filePath)
		tableName = strings.TrimSuffix(base, filepath.Ext(base))
		tableName = strings.ReplaceAll(tableName, "-", "_")
		tableName = strings.ReplaceAll(tableName, " ", "_")

		columns, rows, err := parseTabularFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("auto-ingest parsing: %w", err)
		}

		if err := b.StoreDB.ImportTabular(tableName, columns, rows); err != nil {
			return nil, fmt.Errorf("auto-ingest import: %w", err)
		}
	}

	// Execute the SQL query
	rows, columns, err := b.StoreDB.QuerySQL(sqlStr)
	if err != nil {
		return nil, fmt.Errorf("SQL query failed: %w", err)
	}

	return map[string]interface{}{
		"rows":    rows,
		"columns": columns,
	}, nil
}

// parseTabularFile reads a CSV, TSV, or JSON file into columns and rows.
func parseTabularFile(filePath string) ([]string, [][]string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading file: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".csv":
		return parseCSV(string(content), ',')
	case ".tsv":
		return parseCSV(string(content), '\t')
	default:
		// Try CSV first
		cols, rows, err := parseCSV(string(content), ',')
		if err == nil && len(cols) > 0 {
			return cols, rows, nil
		}
		return nil, nil, fmt.Errorf("unsupported file format: %s", ext)
	}
}

// parseCSV parses delimited content into columns and rows.
func parseCSV(content string, delimiter rune) ([]string, [][]string, error) {
	reader := csv.NewReader(strings.NewReader(content))
	reader.Comma = delimiter
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("CSV parse error: %w", err)
	}

	if len(records) < 2 {
		return nil, nil, fmt.Errorf("CSV must have at least a header and one data row")
	}

	columns := records[0]
	rows := records[1:]
	return columns, rows, nil
}
