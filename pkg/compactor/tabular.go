package compactor

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DefaultTabularThreshold is the default byte threshold for external-tool tabular interception.
const DefaultTabularThreshold = 102400

// TabularData holds parsed tabular content ready for SQLite import.
type TabularData struct {
	Columns     []string   `json:"columns"`
	Rows        [][]string `json:"rows"`
	Format      string     `json:"format"` // "csv", "tsv", or "json"
	SourceBytes int        `json:"source_bytes"`
}

// GetThreshold reads the TZRO_TABULAR_THRESHOLD env var and returns the byte threshold.
// Defaults to DefaultTabularThreshold (4096) if unset or unparseable.
func GetThreshold() int {
	if v := os.Getenv("TZRO_TABULAR_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return DefaultTabularThreshold
}

// ShouldIntercept decides whether tabular data should be imported into SQLite.
// File reads always intercept. External tool outputs only intercept above the threshold.
func ShouldIntercept(td *TabularData, isFileRead bool, threshold int) bool {
	if td == nil || len(td.Rows) < 2 {
		return false
	}
	if isFileRead {
		return true
	}
	return td.SourceBytes > threshold
}

// IsFileReadTool returns true if the tool name corresponds to a file-read operation
// across known agent harnesses (Antigravity, Claude Code, Hermes, Copilot, Pi-Coder).
func IsFileReadTool(toolName string) bool {
	lower := strings.ToLower(toolName)
	switch lower {
	case "view_file", "read_file", "readfile", "viewfile",
		"read", "cat", "read_url_content",
		"file_read", "file_view":
		return true
	}
	// Catch partial matches for harness-specific naming
	return strings.Contains(lower, "read_file") || strings.Contains(lower, "view_file")
}

// DetectTabular attempts to parse the content as structured tabular data.
// Returns the parsed TabularData and true if tabular structure is detected.
// Tries JSON array first, then CSV, then TSV.
func DetectTabular(content string) (*TabularData, bool) {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) == 0 {
		return nil, false
	}

	// 1. Try JSON array of uniform objects
	if td, ok := detectJSONArray(trimmed); ok {
		td.SourceBytes = len(content)
		return td, true
	}

	// 2. Try CSV (comma-separated)
	if td, ok := detectDelimited(trimmed, ',', "csv"); ok {
		td.SourceBytes = len(content)
		return td, true
	}

	// 3. Try TSV (tab-separated)
	if td, ok := detectDelimited(trimmed, '\t', "tsv"); ok {
		td.SourceBytes = len(content)
		return td, true
	}

	return nil, false
}

// detectJSONArray parses a JSON array of uniform objects into TabularData.
func detectJSONArray(content string) (*TabularData, bool) {
	if !strings.HasPrefix(content, "[") || !strings.HasSuffix(content, "]") {
		return nil, false
	}

	var arr []map[string]any
	if err := json.Unmarshal([]byte(content), &arr); err != nil {
		return nil, false
	}

	if len(arr) < 2 {
		return nil, false
	}

	// Collect unique keys in deterministic order from first object
	keySet := make(map[string]bool)
	var columns []string
	for _, item := range arr {
		for k := range item {
			if !keySet[k] {
				keySet[k] = true
				columns = append(columns, k)
			}
		}
	}

	if len(columns) == 0 {
		return nil, false
	}

	// Convert to string rows
	rows := make([][]string, 0, len(arr))
	for _, item := range arr {
		row := make([]string, len(columns))
		for i, col := range columns {
			if val, ok := item[col]; ok {
				row[i] = fmt.Sprintf("%v", val)
			} else {
				row[i] = ""
			}
		}
		rows = append(rows, row)
	}

	return &TabularData{
		Columns: columns,
		Rows:    rows,
		Format:  "json",
	}, true
}

// detectDelimited parses CSV/TSV content into TabularData.
// Requires: ≥2 data rows (plus header), ≥80% of rows have the same column count.
func detectDelimited(content string, delimiter rune, format string) (*TabularData, bool) {
	reader := csv.NewReader(strings.NewReader(content))
	reader.Comma = delimiter
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		// Try a more lenient parse — split by lines and delimiter
		return detectDelimitedLenient(content, delimiter, format)
	}

	if len(records) < 3 { // header + at least 2 data rows
		return nil, false
	}

	// Check column count consistency (≥80% match)
	headerLen := len(records[0])
	if headerLen < 2 {
		return nil, false
	}

	matchCount := 0
	for _, row := range records[1:] {
		if len(row) == headerLen {
			matchCount++
		}
	}

	consistency := float64(matchCount) / float64(len(records)-1)
	if consistency < 0.8 {
		return nil, false
	}

	columns := records[0]
	rows := make([][]string, 0, len(records)-1)
	for _, row := range records[1:] {
		// Pad or truncate to match header length
		normalized := make([]string, headerLen)
		for i := 0; i < headerLen && i < len(row); i++ {
			normalized[i] = row[i]
		}
		rows = append(rows, normalized)
	}

	return &TabularData{
		Columns: columns,
		Rows:    rows,
		Format:  format,
	}, true
}

// detectDelimitedLenient is a fallback parser that splits by lines and delimiter
// when csv.Reader fails (e.g., unquoted fields with special chars).
func detectDelimitedLenient(content string, delimiter rune, format string) (*TabularData, bool) {
	lines := strings.Split(content, "\n")
	var nonEmpty []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			nonEmpty = append(nonEmpty, trimmed)
		}
	}

	if len(nonEmpty) < 3 {
		return nil, false
	}

	delStr := string(delimiter)
	headerParts := strings.Split(nonEmpty[0], delStr)
	if len(headerParts) < 2 {
		return nil, false
	}

	headerLen := len(headerParts)
	matchCount := 0
	for _, line := range nonEmpty[1:] {
		parts := strings.Split(line, delStr)
		if len(parts) == headerLen {
			matchCount++
		}
	}

	consistency := float64(matchCount) / float64(len(nonEmpty)-1)
	if consistency < 0.8 {
		return nil, false
	}

	columns := make([]string, headerLen)
	for i, h := range headerParts {
		columns[i] = strings.TrimSpace(h)
	}

	rows := make([][]string, 0, len(nonEmpty)-1)
	for _, line := range nonEmpty[1:] {
		parts := strings.Split(line, delStr)
		normalized := make([]string, headerLen)
		for i := 0; i < headerLen && i < len(parts); i++ {
			normalized[i] = strings.TrimSpace(parts[i])
		}
		rows = append(rows, normalized)
	}

	return &TabularData{
		Columns: columns,
		Rows:    rows,
		Format:  format,
	}, true
}

// FormatEnvelope generates the compact data envelope that replaces the raw tabular output.
// Includes schema, row count, sample rows, and the query pointer.
func FormatEnvelope(tableName string, td *TabularData, sampleCount int) string {
	if sampleCount <= 0 {
		sampleCount = 5
	}
	if sampleCount > len(td.Rows) {
		sampleCount = len(td.Rows)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Tabular Data Imported (%d rows, %d cols)\n", len(td.Rows), len(td.Columns)))
	sb.WriteString(fmt.Sprintf("Table: `%s`\n", tableName))
	sb.WriteString(fmt.Sprintf("Format: %s | Source: %d bytes\n\n", td.Format, td.SourceBytes))

	// Column list
	sb.WriteString("Columns: ")
	sb.WriteString(strings.Join(td.Columns, ", "))
	sb.WriteString("\n\n")

	// Sample rows as markdown table
	sb.WriteString(fmt.Sprintf("## Sample (first %d rows)\n", sampleCount))
	sb.WriteString("| ")
	sb.WriteString(strings.Join(td.Columns, " | "))
	sb.WriteString(" |\n")
	sb.WriteString("|")
	sb.WriteString(strings.Repeat(" --- |", len(td.Columns)))
	sb.WriteString("\n")

	for i := 0; i < sampleCount; i++ {
		sb.WriteString("| ")
		sb.WriteString(strings.Join(td.Rows[i], " | "))
		sb.WriteString(" |\n")
	}

	sb.WriteString(fmt.Sprintf("\nQuery with: `tzro query %s \"SELECT ... FROM %s WHERE ...\"`\n", tableName, tableName))

	return sb.String()
}
