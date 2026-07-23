package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"tzro/internal/cache"
)

// sqlColumnNameRe matches characters that are not valid in SQLite unquoted identifiers.
// This MUST match the sanitizeColumnName logic in cache/query_db.go so that
// column names reported by the DataProfile match the materialized SQL table.
var sqlColumnNameRe = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// sanitizeSQLColumnName normalizes a raw header name to match the column name
// used in the materialized SQL table. Without this, the model sees "Target_Account?"
// in the profile but the table column is "Target_Account_", causing query failures.
func sanitizeSQLColumnName(name string) string {
	cleaned := sqlColumnNameRe.ReplaceAllString(name, "_")
	if cleaned == "" || cleaned[0] >= '0' && cleaned[0] <= '9' {
		cleaned = "t_" + cleaned
	}
	return cleaned
}

// DataProfile is the structured profile returned by the Data Profiler
// when read_file encounters a tabular file (CSV, TSV, Excel, large JSON array).
type DataProfile struct {
	Format        string          `json:"format"` // "csv", "tsv", "xlsx", "json"
	Path          string          `json:"path"`
	Delimiter     string          `json:"delimiter,omitempty"` // for csv/tsv
	RowCount      int             `json:"rowCount"`
	ColumnCount   int             `json:"columnCount"`
	FileSizeBytes int64           `json:"fileSizeBytes"`
	Columns       []ColumnProfile `json:"columns"`
	SampleRows    string          `json:"sampleRows"` // TSV-formatted
	CacheID       string          `json:"cacheId"`
	// Excel-specific
	Sheets      []SheetSummary `json:"sheets,omitempty"`
	ActiveSheet string         `json:"activeSheet,omitempty"`
}

// ColumnProfile describes a single column's statistical properties.
type ColumnProfile struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"` // "integer", "float", "string", "boolean", "enum", "mixed"
	NullRate    float64     `json:"nullRate"`
	Cardinality interface{} `json:"cardinality"` // int or ">1000"
	// Conditional fields
	Values []string `json:"values,omitempty"` // only for enum (cardinality ≤ 20)
	Min    *float64 `json:"min,omitempty"`    // only for numeric types
	Max    *float64 `json:"max,omitempty"`    // only for numeric types
}

// SheetSummary summarizes a single Excel sheet.
type SheetSummary struct {
	Name        string `json:"name"`
	RowCount    int    `json:"rowCount"`
	ColumnCount int    `json:"columnCount"`
}

// columnAccumulator tracks running statistics for a single column during streaming.
type columnAccumulator struct {
	name        string
	nullCount   int
	distinctSet map[string]struct{} // capped at 1000
	cappedOut   bool                // true when distinctSet hit 1000
	typeTracker typeInference       // running type inference
	numericMin  *float64
	numericMax  *float64
}

// typeInference tracks inferred type for a column with priority:
// integer > float > boolean > string. Once string, stays string.
type typeInference struct {
	seenInteger bool
	seenFloat   bool
	seenBoolean bool
	seenString  bool
	total       int
}

func (ti *typeInference) observe(val string) {
	ti.total++
	val = strings.TrimSpace(val)
	if val == "" {
		return // nulls don't affect type
	}

	// Try integer
	if _, err := strconv.ParseInt(val, 10, 64); err == nil {
		ti.seenInteger = true
		return
	}

	// Try float
	if _, err := strconv.ParseFloat(val, 64); err == nil {
		ti.seenFloat = true
		return
	}

	// Try boolean
	lower := strings.ToLower(val)
	if lower == "true" || lower == "false" {
		ti.seenBoolean = true
		return
	}

	// Otherwise string
	ti.seenString = true
}

func (ti *typeInference) result() string {
	if ti.seenString {
		return "string"
	}
	if ti.seenBoolean && (ti.seenInteger || ti.seenFloat) {
		return "mixed"
	}
	if ti.seenFloat && ti.seenInteger {
		return "float" // integers are a subset of floats
	}
	if ti.seenFloat {
		return "float"
	}
	if ti.seenInteger {
		return "integer"
	}
	if ti.seenBoolean {
		return "boolean"
	}
	return "string" // default if nothing seen
}

const (
	maxDistinctValues = 1000
	enumThreshold     = 20
	sampleBudget      = 10_000 // max characters for sample rows
)

// ProfileTabularFile streams a tabular file (CSV/TSV) and returns a DataProfile.
// It performs a single-pass scan computing per-column statistics, type inference,
// and reservoir-sampled rows.
func ProfileTabularFile(filePath string) (*DataProfile, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	format := ""
	delimiter := ""
	switch ext {
	case ".csv":
		format = "csv"
	case ".tsv":
		format = "tsv"
		delimiter = "\t"
	default:
		return nil, fmt.Errorf("unsupported tabular format: %s", ext)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Increase scanner buffer for wide CSVs
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	// Read first few lines for delimiter detection (CSV only)
	if format == "csv" && delimiter == "" {
		delimiter = detectDelimiter(filePath)
	}

	// Read header
	if !scanner.Scan() {
		return nil, fmt.Errorf("empty file: no header row")
	}
	headerLine := scanner.Text()
	// Strip UTF-8 BOM if present
	headerLine = strings.TrimPrefix(headerLine, "\xEF\xBB\xBF")
	headerLine = strings.TrimPrefix(headerLine, "\uFEFF")
	headers := splitByDelimiter(headerLine, delimiter)

	// Initialize column accumulators
	accumulators := make([]columnAccumulator, len(headers))
	for i, h := range headers {
		accumulators[i] = columnAccumulator{
			name:        strings.TrimSpace(h),
			distinctSet: make(map[string]struct{}),
		}
	}

	// Stream rows
	rowCount := 0
	var firstRows [][]string // first 3 rows
	var reservoir [][]string // reservoir-sampled rows
	const reservoirSize = 2

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		rowCount++
		fields := splitByDelimiter(line, delimiter)

		// Pad or truncate fields to match header count
		for len(fields) < len(headers) {
			fields = append(fields, "")
		}
		if len(fields) > len(headers) {
			fields = fields[:len(headers)]
		}

		// Collect first 3 rows for sampling
		if len(firstRows) < 3 {
			row := make([]string, len(fields))
			copy(row, fields)
			firstRows = append(firstRows, row)
		}

		// Reservoir sampling for remaining rows
		if rowCount > 3 {
			if len(reservoir) < reservoirSize {
				row := make([]string, len(fields))
				copy(row, fields)
				reservoir = append(reservoir, row)
			} else {
				// Simple reservoir sampling: replace with probability reservoirSize/rowCount
				j := pseudoRandom(rowCount)
				if j < reservoirSize {
					copy(reservoir[j], fields)
				}
			}
		}

		// Update accumulators
		for i, val := range fields {
			if i >= len(accumulators) {
				break
			}
			trimVal := strings.TrimSpace(val)

			// Null tracking
			if trimVal == "" {
				accumulators[i].nullCount++
			}

			// Distinct values (capped)
			if !accumulators[i].cappedOut && trimVal != "" {
				accumulators[i].distinctSet[trimVal] = struct{}{}
				if len(accumulators[i].distinctSet) >= maxDistinctValues {
					accumulators[i].cappedOut = true
				}
			}

			// Type inference
			accumulators[i].typeTracker.observe(trimVal)

			// Numeric min/max
			if trimVal != "" {
				if f, err := strconv.ParseFloat(trimVal, 64); err == nil {
					if accumulators[i].numericMin == nil || f < *accumulators[i].numericMin {
						fCopy := f
						accumulators[i].numericMin = &fCopy
					}
					if accumulators[i].numericMax == nil || f > *accumulators[i].numericMax {
						fCopy := f
						accumulators[i].numericMax = &fCopy
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	// Build column profiles
	columns := make([]ColumnProfile, len(accumulators))
	for i, acc := range accumulators {
		colType := acc.typeTracker.result()

		var cardinality interface{}
		if acc.cappedOut {
			cardinality = ">1000"
		} else {
			cardinality = len(acc.distinctSet)
		}

		var nullRate float64
		if rowCount > 0 {
			nullRate = math.Round(float64(acc.nullCount)/float64(rowCount)*10000) / 10000
		}

		col := ColumnProfile{
			Name:        sanitizeSQLColumnName(acc.name),
			Type:        colType,
			NullRate:    nullRate,
			Cardinality: cardinality,
		}

		// Enum detection: cardinality ≤ 20, type is string, and cardinality is
		// meaningfully lower than row count (avoids false enum in small datasets).
		if !acc.cappedOut && len(acc.distinctSet) <= enumThreshold && len(acc.distinctSet) > 0 &&
			colType == "string" && rowCount > 0 &&
			float64(len(acc.distinctSet)) < float64(rowCount)*0.5 {
			col.Type = "enum"
			vals := make([]string, 0, len(acc.distinctSet))
			for v := range acc.distinctSet {
				vals = append(vals, v)
			}
			col.Values = vals
		}

		// Numeric min/max
		if colType == "integer" || colType == "float" {
			col.Min = acc.numericMin
			col.Max = acc.numericMax
		}

		columns[i] = col
	}

	// Build sample rows with adaptive sizing
	// Sanitize headers for sample rows to match ColumnProfile names
	sanitizedHeaders := make([]string, len(headers))
	for i, h := range headers {
		sanitizedHeaders[i] = sanitizeSQLColumnName(h)
	}
	sampleRows := buildSampleRows(sanitizedHeaders, firstRows, reservoir, columns, rowCount)

	cacheID := cache.NewCacheID()

	return &DataProfile{
		Format:        format,
		Path:          filePath,
		Delimiter:     delimiter,
		RowCount:      rowCount,
		ColumnCount:   len(headers),
		FileSizeBytes: info.Size(),
		Columns:       columns,
		SampleRows:    sampleRows,
		CacheID:       cacheID,
	}, nil
}

// buildSampleRows formats sample rows as TSV with adaptive sizing.
// Algorithm:
//  1. Select 5 rows: first 3 + 2 reservoir-sampled
//  2. Format as TSV
//  3. If > 10K chars: reduce to 3 rows, then 1 row
//  4. Apply column pruning to sample rows
func buildSampleRows(headers []string, firstRows, reservoir [][]string, columns []ColumnProfile, totalRows int) string {
	// Combine samples: first rows + reservoir
	var allSamples [][]string
	allSamples = append(allSamples, firstRows...)
	allSamples = append(allSamples, reservoir...)

	// Determine which columns to prune from samples
	prunedCols := computePrunedColumns(columns, totalRows)

	// Try with all samples first
	tsv := formatSampleTSV(headers, allSamples, prunedCols)
	if len(tsv) <= sampleBudget {
		return tsv
	}

	// Reduce to 3 rows (first 2 + 1 reservoir)
	var reduced [][]string
	if len(firstRows) >= 2 {
		reduced = append(reduced, firstRows[:2]...)
	} else {
		reduced = append(reduced, firstRows...)
	}
	if len(reservoir) > 0 {
		reduced = append(reduced, reservoir[0])
	}
	tsv = formatSampleTSV(headers, reduced, prunedCols)
	if len(tsv) <= sampleBudget {
		return tsv
	}

	// Reduce to 1 row (first data row only)
	if len(firstRows) > 0 {
		tsv = formatSampleTSV(headers, firstRows[:1], prunedCols)
	}

	// If even 1 row still exceeds budget, truncate each value
	if len(tsv) > sampleBudget && len(tsv) > 0 {
		lines := strings.Split(tsv, "\n")
		var truncated []string
		for _, line := range lines {
			fields := strings.Split(line, "\t")
			for i, f := range fields {
				if len(f) > 30 {
					fields[i] = f[:30] + "…"
				}
			}
			truncated = append(truncated, strings.Join(fields, "\t"))
		}
		tsv = strings.Join(truncated, "\n")
	}

	// If still over budget after truncation, progressively drop columns from the right
	if len(tsv) > sampleBudget {
		unpruned := make([]int, 0)
		for i := range headers {
			if !prunedCols[i] {
				unpruned = append(unpruned, i)
			}
		}
		for len(unpruned) > 1 && len(tsv) > sampleBudget {
			// Drop the last unpruned column
			prunedCols[unpruned[len(unpruned)-1]] = true
			unpruned = unpruned[:len(unpruned)-1]
			if len(firstRows) > 0 {
				tsv = formatSampleTSV(headers, firstRows[:1], prunedCols)
			}
		}
	}

	return tsv
}

// computePrunedColumns determines which column indices to drop from samples.
// Drop from samples (not schema) if:
// - Column is >90% null
// - Column cardinality >95% of row count AND type is string (likely free-text/ID)
// - Column has a single constant value across all rows
func computePrunedColumns(columns []ColumnProfile, totalRows int) map[int]bool {
	pruned := make(map[int]bool)
	for i, col := range columns {
		// >90% null
		if col.NullRate > 0.9 {
			pruned[i] = true
			continue
		}

		// Single constant value
		if card, ok := col.Cardinality.(int); ok && card <= 1 && totalRows > 1 {
			pruned[i] = true
			continue
		}

		// High cardinality string (likely ID/free-text)
		if col.Type == "string" && totalRows > 0 {
			if card, ok := col.Cardinality.(int); ok {
				threshold := float64(totalRows) * 0.95
				if float64(card) > threshold {
					pruned[i] = true
					continue
				}
			}
			if _, ok := col.Cardinality.(string); ok {
				// ">1000" — definitely high cardinality
				pruned[i] = true
				continue
			}
		}
	}
	return pruned
}

// formatSampleTSV formats headers + data rows as TSV, excluding pruned columns.
func formatSampleTSV(headers []string, rows [][]string, prunedCols map[int]bool) string {
	var lines []string

	// Header
	var hdr []string
	for i, h := range headers {
		if !prunedCols[i] {
			hdr = append(hdr, h)
		}
	}
	lines = append(lines, strings.Join(hdr, "\t"))

	// Data rows
	for _, row := range rows {
		var vals []string
		for i := range headers {
			if prunedCols[i] {
				continue
			}
			if i < len(row) {
				vals = append(vals, row[i])
			} else {
				vals = append(vals, "")
			}
		}
		lines = append(lines, strings.Join(vals, "\t"))
	}

	return strings.Join(lines, "\n")
}

// detectDelimiter reads the first 5 lines and scores candidate delimiters.
func detectDelimiter(filePath string) string {
	file, err := os.Open(filePath)
	if err != nil {
		return ","
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var lines []string
	for scanner.Scan() && len(lines) < 5 {
		lines = append(lines, scanner.Text())
	}

	if len(lines) == 0 {
		return ","
	}

	candidates := []string{",", ";", "|", "\t"}
	bestDelim := ","
	bestScore := 0.0

	for _, delim := range candidates {
		var counts []int
		for _, line := range lines {
			counts = append(counts, strings.Count(line, delim)+1)
		}

		// Score = consistency × totalFields
		if len(counts) == 0 {
			continue
		}

		// Calculate consistency: all lines should have the same field count
		first := counts[0]
		consistent := true
		for _, c := range counts[1:] {
			if c != first {
				consistent = false
				break
			}
		}

		totalFields := 0
		for _, c := range counts {
			totalFields += c
		}

		score := float64(totalFields)
		if consistent && first > 1 {
			score *= float64(len(lines)) // boost for consistency
		}

		if score > bestScore {
			bestScore = score
			bestDelim = delim
		}
	}

	return bestDelim
}

// splitByDelimiter splits a line by the given delimiter.
// For comma delimiter, it handles basic CSV quoting.
func splitByDelimiter(line, delimiter string) []string {
	if delimiter == "," {
		return parseCSVLine(line)
	}
	return strings.Split(line, delimiter)
}

// parseCSVLine handles basic CSV quoting rules.
func parseCSVLine(line string) []string {
	var fields []string
	var current strings.Builder
	inQuotes := false

	for i := 0; i < len(line); i++ {
		ch := line[i]
		if inQuotes {
			if ch == '"' {
				if i+1 < len(line) && line[i+1] == '"' {
					current.WriteByte('"')
					i++ // skip escaped quote
				} else {
					inQuotes = false
				}
			} else {
				current.WriteByte(ch)
			}
		} else {
			if ch == '"' {
				inQuotes = true
			} else if ch == ',' {
				fields = append(fields, current.String())
				current.Reset()
			} else {
				current.WriteByte(ch)
			}
		}
	}
	fields = append(fields, current.String())
	return fields
}

// pseudoRandom generates a deterministic pseudo-random index for reservoir sampling.
// Uses a simple modulo-based approach for reproducibility.
func pseudoRandom(n int) int {
	// Simple hash-based pseudo-random for reservoir sampling
	return (n * 2654435761) % n
}

// IsTabularExtension returns true if the file extension indicates a tabular format
// that should be profiled instead of returned raw.
func IsTabularExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".csv", ".tsv", ".xlsx", ".xls":
		return true
	}
	return false
}

// ShouldProfileJSON returns true if a .json file exceeds the thresholds
// for profiling (>200 lines OR >10KB).
func ShouldProfileJSON(filePath string) bool {
	info, err := os.Stat(filePath)
	if err != nil {
		return false
	}
	if info.Size() > 10*1024 {
		return true
	}

	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer file.Close()

	lineCount := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lineCount++
		if lineCount > 200 {
			return true
		}
	}
	return false
}

// ProfileJSONFile profiles a large JSON array file and returns a DataProfile.
// It reads the JSON, extracts headers from the first object, and computes statistics.
func ProfileJSONFile(filePath string) (*DataProfile, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read JSON file: %w", err)
	}

	info, _ := os.Stat(filePath)

	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	arr, ok := raw.([]interface{})
	if !ok || len(arr) == 0 {
		return nil, fmt.Errorf("JSON file is not an array or is empty")
	}

	// Extract headers from first object
	firstObj, ok := arr[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("JSON array does not contain objects")
	}

	var headers []string
	for k := range firstObj {
		headers = append(headers, k)
	}

	// Build column profiles from all records
	accumulators := make([]columnAccumulator, len(headers))
	for i, h := range headers {
		accumulators[i] = columnAccumulator{
			name:        h,
			distinctSet: make(map[string]struct{}),
		}
	}

	for _, item := range arr {
		obj, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		for i, h := range headers {
			val := fmt.Sprintf("%v", obj[h])
			if obj[h] == nil {
				accumulators[i].nullCount++
				continue
			}
			trimVal := strings.TrimSpace(val)
			if !accumulators[i].cappedOut && trimVal != "" {
				accumulators[i].distinctSet[trimVal] = struct{}{}
				if len(accumulators[i].distinctSet) >= maxDistinctValues {
					accumulators[i].cappedOut = true
				}
			}
			accumulators[i].typeTracker.observe(trimVal)
		}
	}

	columns := make([]ColumnProfile, len(accumulators))
	for i, acc := range accumulators {
		colType := acc.typeTracker.result()
		var cardinality interface{}
		if acc.cappedOut {
			cardinality = ">1000"
		} else {
			cardinality = len(acc.distinctSet)
		}
		var nullRate float64
		if len(arr) > 0 {
			nullRate = math.Round(float64(acc.nullCount)/float64(len(arr))*10000) / 10000
		}
		columns[i] = ColumnProfile{
			Name:        sanitizeSQLColumnName(acc.name),
			Type:        colType,
			NullRate:    nullRate,
			Cardinality: cardinality,
		}
	}

	// Build sample rows from first 3 records
	var firstRows [][]string
	for idx, item := range arr {
		if idx >= 3 {
			break
		}
		obj, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		row := make([]string, len(headers))
		for i, h := range headers {
			row[i] = fmt.Sprintf("%v", obj[h])
		}
		firstRows = append(firstRows, row)
	}
	// Sanitize headers for sample rows to match ColumnProfile names
	sanitizedHeaders := make([]string, len(headers))
	for i, h := range headers {
		sanitizedHeaders[i] = sanitizeSQLColumnName(h)
	}
	sampleRows := buildSampleRows(sanitizedHeaders, firstRows, nil, columns, len(arr))

	return &DataProfile{
		Format:        "json",
		Path:          filePath,
		RowCount:      len(arr),
		ColumnCount:   len(headers),
		FileSizeBytes: info.Size(),
		Columns:       columns,
		SampleRows:    sampleRows,
		CacheID:       cache.NewCacheID(),
	}, nil
}
