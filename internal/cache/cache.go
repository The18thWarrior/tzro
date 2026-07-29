package cache

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/memory"
)

type CacheEnvelope struct {
	CacheID      string                 `json:"cacheId"`
	DataType     string                 `json:"dataType"`
	RootPath     string                 `json:"rootPath"`
	Fields       []string               `json:"fields"`
	FieldTypes   map[string]string      `json:"fieldTypes"`
	EnumValues   map[string][]string    `json:"enumValues,omitempty"`
	SampleRecord map[string]interface{} `json:"sampleRecord"`
}

type CacheStore interface {
	// Store handles envelope creation, writes to SQLite, backups to file, and returns the envelope JSON string and CacheID.
	Store(ctx context.Context, rawPayload string) (envelopeStr string, cacheID string, err error)

	// StoreFileRef stores a reference to an on-disk file (path only, no content copy).
	// Returns the generated cacheID.
	StoreFileRef(ctx context.Context, filePath string, envelopeJSON string) (cacheID string, err error)

	// Introspect retrieves the cache envelope JSON string (with DB lookup and file fallback).
	Introspect(ctx context.Context, cacheID string) string

	// Read retrieves offset-based paginated slice of records from the cache (with DB lookup and file fallback).
	Read(ctx context.Context, cacheID string, limit, offset int) string

	// Query runs a SQL query against the materialized cache table in the ephemeral query DB.
	Query(ctx context.Context, cacheID, sqlExpr string) string
}

// NewCacheID generates a unique cache identifier from the current nanosecond
// timestamp with trailing zeros stripped. Trailing zeros in IDs like
// "cache_1784607195509971000" cause local LLMs to treat the ID as a number
// and round/truncate it (e.g., "cache_178460719550000000000000000000000"),
// producing table-not-found errors. Stripping trailing zeros yields IDs like
// "cache_1784607195509971" which the model reliably copies verbatim.
func NewCacheID() string {
	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	ts = strings.TrimRight(ts, "0")
	return "cache_" + ts
}

// resolveTzroPath delegates to config.ResolvePath — canonical TZRO_DIR resolution.
func resolveTzroPath(relPath string) string {
	return config.ResolvePath(relPath)
}

type sqlCacheStore struct{}

var DefaultStore CacheStore = &sqlCacheStore{}

// ColumnPruner defines the interface for LLM-guided column selection.
type ColumnPruner interface {
	Prune(ctx context.Context, headers []string, stepInstruction string) ([]string, error)
}

type llmColumnPruner struct{}

var DefaultColumnPruner ColumnPruner = &llmColumnPruner{}

func (p *llmColumnPruner) Prune(ctx context.Context, headers []string, stepInstruction string) ([]string, error) {
	if len(headers) == 0 {
		return nil, nil
	}

	headersJSON, _ := json.Marshal(headers)
	userPrompt := fmt.Sprintf("Step Instructions: %q\nAvailable Columns: %s\n", stepInstruction, string(headersJSON))

	const PruneSystemPrompt = `You are a context optimization agent. Your job is to analyze a tabular TSV dataset's columns and a user step's instructions, then determine which columns are essential to retain in order to successfully execute the instructions.
You MUST retain columns that are directly referenced, highly relevant, or necessary for the step.
Keep unique reference columns like ID or Name out of this selection, as they will be automatically preserved.
Respond with ONLY valid JSON matching the schema below. No markdown fences.`

	const PruneSchema = `{
  "type": "object",
  "properties": {
    "columns": {
      "type": "array",
      "items": {
        "type": "string"
      }
    }
  },
  "required": ["columns"]
}`

	req := inference.NewSimpleRequest(PruneSystemPrompt, userPrompt, PruneSchema)

	resContent, err := inference.ExecuteRouterStructured(ctx, req)
	if err != nil {
		return nil, err
	}

	var result struct {
		Columns []string `json:"columns"`
	}
	if err := json.Unmarshal([]byte(resContent), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal prune columns result: %w", err)
	}

	return result.Columns, nil
}

// PruneColumns slices the TSV string to keep only the selected columns from LLM pruning,
// plus always keeping unique reference key columns.
func PruneColumns(ctx context.Context, tsvContent string, stepInstruction string) (string, error) {
	if tsvContent == "" || stepInstruction == "" {
		return tsvContent, nil
	}

	lines := strings.Split(tsvContent, "\n")
	if len(lines) == 0 {
		return tsvContent, nil
	}

	headersLine := lines[0]
	headers := strings.Split(headersLine, "\t")
	if len(headers) == 0 || (len(headers) == 1 && headers[0] == "") {
		return tsvContent, nil
	}

	selected, err := DefaultColumnPruner.Prune(ctx, headers, stepInstruction)
	if err != nil {
		return tsvContent, err
	}

	// Build a map of columns to keep
	keepMap := make(map[string]bool)
	for _, col := range selected {
		keepMap[strings.ToLower(strings.TrimSpace(col))] = true
	}

	// Always keep unique key columns (e.g. "id", "name", "uid", "uuid", "key", "email", etc.)
	isUniqueKey := func(col string) bool {
		lower := strings.ToLower(strings.TrimSpace(col))
		if lower == "id" || lower == "name" || lower == "uid" || lower == "uuid" || lower == "key" || lower == "email" {
			return true
		}
		if strings.HasSuffix(lower, "id") || strings.HasSuffix(lower, "name") {
			return true
		}
		return false
	}

	// Identify indices to keep
	var keepIndices []int
	var newHeaders []string
	for i, header := range headers {
		trimmedHeader := strings.TrimSpace(header)
		if isUniqueKey(trimmedHeader) || keepMap[strings.ToLower(trimmedHeader)] {
			keepIndices = append(keepIndices, i)
			newHeaders = append(newHeaders, header)
		}
	}

	// If no columns are selected (or all are filtered out, which shouldn't happen but just in case),
	// fallback to returning original TSV content.
	if len(keepIndices) == 0 {
		return tsvContent, nil
	}

	// Rebuild the TSV
	var outputLines []string
	outputLines = append(outputLines, strings.Join(newHeaders, "\t"))

	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		var newCols []string
		for _, idx := range keepIndices {
			if idx < len(cols) {
				newCols = append(newCols, cols[idx])
			} else {
				newCols = append(newCols, "")
			}
		}
		outputLines = append(outputLines, strings.Join(newCols, "\t"))
	}

	return strings.Join(outputLines, "\n"), nil
}

// Process runs the compaction pipeline. If the size of either the raw or
// compacted payload exceeds 12288 bytes, it automatically generates the
// CacheEnvelope, writes the raw payload to SQLite and disk backups, and returns
// the JSON envelope string and the new CacheID. Otherwise, it returns the
// compacted text and an empty CacheID.
func Process(ctx context.Context, payload string, stepInstruction string) (processedPayload string, cacheID string, err error) {
	compacted, _ := compact(ctx, payload, stepInstruction)
	if len(payload) > 12288 || len(compacted) > 12288 {
		envelopeStr, cid, err := DefaultStore.Store(ctx, payload)
		if err != nil {
			return compacted, "", err
		}
		return envelopeStr, cid, nil
	}
	return compacted, "", nil
}

func (s *sqlCacheStore) Store(ctx context.Context, rawPayload string) (string, string, error) {
	envelopeStr, envelope := createCacheEnvelope(rawPayload)
	cacheID := envelope.CacheID

	// Save to SQLite
	db := memory.DB.RawDB()
	if db != nil {
		createdAt := time.Now().Unix()
		_, err := db.Exec(`INSERT OR REPLACE INTO disk_cache (cache_id, raw_payload, envelope_json, created_at)
			VALUES (?, ?, ?, ?)`, cacheID, rawPayload, envelopeStr, createdAt)
		if err != nil {
			fmt.Printf("[Cache Store Error] Failed to write cache to database: %v\n", err)
		}
	}

	// Materialize in ephemeral query DB
	columnTypes := envelopeFieldTypesToSQLite(envelope.FieldTypes)
	if err := MaterializeTable(cacheID, rawPayload, columnTypes, ""); err != nil {
		fmt.Fprintf(os.Stderr, "[Cache] Materialization warning: %v\n", err)
		// Non-fatal — SQL queries will lazily re-materialize
	}

	// Backup file cache
	cacheFileDir := resolveTzroPath(filepath.Join(".tzro", "cache"))
	if err := os.MkdirAll(cacheFileDir, 0755); err != nil {
		return envelopeStr, cacheID, fmt.Errorf("failed to create cache backup directory: %w", err)
	}

	cacheFilePath := filepath.Join(cacheFileDir, cacheID+".json")
	if err := os.WriteFile(cacheFilePath, []byte(rawPayload), 0644); err != nil {
		return envelopeStr, cacheID, fmt.Errorf("failed to write cache backup file: %w", err)
	}

	return envelopeStr, cacheID, nil
}

func (s *sqlCacheStore) StoreFileRef(ctx context.Context, filePath string, envelopeJSON string) (string, error) {
	cacheID := NewCacheID()

	db := memory.DB.RawDB()
	if db == nil {
		return "", fmt.Errorf("database not available")
	}

	createdAt := time.Now().Unix()
	_, err := db.Exec(`INSERT OR REPLACE INTO disk_cache (cache_id, raw_payload, envelope_json, file_path, created_at)
		VALUES (?, '', ?, ?, ?)`, cacheID, envelopeJSON, filePath, createdAt)
	if err != nil {
		return "", fmt.Errorf("failed to store file reference: %w", err)
	}

	// Materialize in ephemeral query DB from the file
	columnTypes := extractColumnTypesFromEnvelope(envelopeJSON)
	rawJSON := s.readFileAsJSON(filePath)
	if !strings.HasPrefix(rawJSON, "Error:") {
		if err := MaterializeTable(cacheID, rawJSON, columnTypes, ""); err != nil {
			fmt.Fprintf(os.Stderr, "[Cache] File materialization warning: %v\n", err)
		}
	}

	return cacheID, nil
}

func (s *sqlCacheStore) Introspect(ctx context.Context, cacheID string) string {
	db := memory.DB.RawDB()
	if db != nil {
		var envelopeJSON string
		err := db.QueryRow("SELECT envelope_json FROM disk_cache WHERE cache_id = ?", cacheID).Scan(&envelopeJSON)
		if err == nil && envelopeJSON != "" {
			RecordCacheHit()
			return envelopeJSON
		}
	}

	// Fallback to reading file and recreating envelope
	cacheFilePath := resolveTzroPath(filepath.Join(".tzro", "cache", cacheID+".json"))
	bytes, err := os.ReadFile(cacheFilePath)
	if err != nil {
		RecordCacheMiss()
		return fmt.Sprintf("Error: cache with ID '%s' not found in database or disk", cacheID)
	}

	RecordCacheHit()
	envJSON, _ := createCacheEnvelope(string(bytes))
	return envJSON
}

func (s *sqlCacheStore) Read(ctx context.Context, cacheID string, limit, offset int) string {
	rawPayload := s.getRawPayload(cacheID)
	if strings.HasPrefix(rawPayload, "Error:") {
		return rawPayload
	}

	var parsed interface{}
	if err := json.Unmarshal([]byte(rawPayload), &parsed); err != nil {
		return fmt.Sprintf("Error: failed to parse cached JSON: %v", err)
	}

	var records []interface{}
	if arr, ok := parsed.([]interface{}); ok {
		records = arr
	} else if mapData, ok := parsed.(map[string]interface{}); ok {
		if recs, ok := mapData["records"].([]interface{}); ok {
			records = recs
		} else {
			for _, v := range mapData {
				if arr, ok := v.([]interface{}); ok {
					records = arr
					break
				}
			}
		}
	}

	if len(records) == 0 {
		return "[]"
	}

	if offset < 0 {
		offset = 0
	}
	if offset >= len(records) {
		return "[]"
	}

	end := offset + limit
	if end > len(records) {
		end = len(records)
	}

	sliced := records[offset:end]
	resBytes, err := json.MarshalIndent(sliced, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error: failed to marshal paginated records: %v", err)
	}

	return string(resBytes)
}

func (s *sqlCacheStore) Query(ctx context.Context, cacheID, sqlExpr string) string {
	result, err := ExecuteSQL(ctx, cacheID, sqlExpr)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return result
}

func (s *sqlCacheStore) getRawPayload(cacheID string) string {
	db := memory.DB.RawDB()
	if db != nil {
		var rawPayload string
		err := db.QueryRow("SELECT raw_payload FROM disk_cache WHERE cache_id = ?", cacheID).Scan(&rawPayload)
		if err == nil && rawPayload != "" {
			RecordCacheHit()
			return rawPayload
		}

		// Check for file_path reference (path-only cache entry)
		var filePath string
		err = db.QueryRow("SELECT COALESCE(file_path, '') FROM disk_cache WHERE cache_id = ?", cacheID).Scan(&filePath)
		if err == nil && filePath != "" {
			RecordCacheHit()
			return s.readFileAsJSON(filePath)
		}
	}

	// Fallback to disk file
	cacheFilePath := resolveTzroPath(filepath.Join(".tzro", "cache", cacheID+".json"))
	bytes, err := os.ReadFile(cacheFilePath)
	if err != nil {
		RecordCacheMiss()
		return fmt.Sprintf("Error: cache with ID '%s' not found on database or disk", cacheID)
	}
	RecordCacheHit()
	return string(bytes)
}

// readFileAsJSON reads a file and converts it to JSON format.
// For CSV/TSV files, converts to a JSON array of objects.
// For other files, reads raw content.
func (s *sqlCacheStore) readFileAsJSON(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".csv", ".tsv":
		return csvToJSON(filePath, ext)
	default:
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Sprintf("Error: failed to read file at '%s': %v", filePath, err)
		}
		return string(data)
	}
}

// csvToJSON converts a CSV/TSV file to a JSON array of objects.
// Uses quote-aware parsing for CSV to handle RFC 4180 quoted fields
// (e.g., fields containing commas like "McDevitt, John").
func csvToJSON(filePath string, ext string) string {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Sprintf("Error: failed to open file at '%s': %v", filePath, err)
	}
	defer file.Close()

	delimiter := ","
	if ext == ".tsv" {
		delimiter = "\t"
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	if !scanner.Scan() {
		return "[]"
	}
	headerLine := scanner.Text()
	// Strip BOM
	headerLine = strings.TrimPrefix(headerLine, "\xEF\xBB\xBF")
	headerLine = strings.TrimPrefix(headerLine, "\uFEFF")

	headers := splitCSVFields(headerLine, delimiter)
	for i := range headers {
		headers[i] = strings.TrimSpace(headers[i])
	}

	var records []map[string]interface{}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := splitCSVFields(line, delimiter)
		record := make(map[string]interface{})
		for i, h := range headers {
			var val string
			if i < len(fields) {
				val = strings.TrimSpace(fields[i])
			}
			record[h] = val
		}
		records = append(records, record)
	}

	resBytes, err := json.Marshal(records)
	if err != nil {
		return fmt.Sprintf("Error: failed to marshal CSV to JSON: %v", err)
	}
	return string(resBytes)
}

// splitCSVFields splits a line by the given delimiter with quote awareness.
// For comma-delimited files, handles RFC 4180 quoting (double-quoted fields
// may contain commas and escaped quotes). For other delimiters (TSV), uses
// simple split since tab-delimited files rarely use quoting.
func splitCSVFields(line, delimiter string) []string {
	if delimiter == "," {
		return parseCSVFields(line)
	}
	return strings.Split(line, delimiter)
}

// parseCSVFields handles RFC 4180 CSV quoting rules: fields enclosed in
// double quotes may contain commas and escaped quotes ("").
func parseCSVFields(line string) []string {
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

// Private compaction helpers

func compact(ctx context.Context, payload string, stepInstruction string) (string, bool) {
	// Layer 0: Base64 Binary Strip
	c := stripBase64(payload)

	// Layer 1: Convert HTML to MD
	c = convertHTMLToMD(c)

	// Try parsing as JSON for layers 2, 3, 4
	var rawData interface{}
	err := json.Unmarshal([]byte(c), &rawData)
	if err != nil {
		// Non-JSON content returned directly
		return c, false
	}

	// Layer 2: Tabular JSON to TSV
	if arrayData, ok := rawData.([]interface{}); ok && len(arrayData) > 0 {
		tsv, ok := tabularJSONToTSV(arrayData)
		if ok {
			if stepInstruction != "" {
				if pruned, err := PruneColumns(ctx, tsv, stepInstruction); err == nil {
					tsv = pruned
				}
			}
			return tsv, len(payload) > 12000
		}
	}

	// Layer 4: Flatten Nested Hierarchies to Dot notation
	if mapData, ok := rawData.(map[string]interface{}); ok {
		flattened := make(map[string]interface{})
		flattenMap(mapData, flattened, "", 0)

		// Layer 3: KV Line formatting
		var lines []string
		for k, v := range flattened {
			switch v.(type) {
			case []interface{}, map[string]interface{}:
				if b, err := json.Marshal(v); err == nil {
					lines = append(lines, fmt.Sprintf("%s: %s", k, string(b)))
					continue
				}
			}
			lines = append(lines, fmt.Sprintf("%s: %v", k, v))
		}
		return strings.Join(lines, "\n"), len(payload) > 12000
	}

	return c, len(payload) > 12000
}

func stripBase64(s string) string {
	// Simple regex match for base64 strings
	re := regexp.MustCompile(`"([A-Za-z0-9+/]{80,})[=]{0,2}"`)
	return re.ReplaceAllStringFunc(s, func(match string) string {
		length := len(match)
		return fmt.Sprintf(`"[binary:base64_encoded_stream, %dKB]"`, length/1024)
	})
}

func convertHTMLToMD(s string) string {
	// Basic HTML stripping converter
	r := regexp.MustCompile(`<[^>]*>`)
	text := r.ReplaceAllString(s, " ")

	// Compress multiple spaces
	reSpace := regexp.MustCompile(`\s+`)
	return strings.TrimSpace(reSpace.ReplaceAllString(text, " "))
}

func tabularJSONToTSV(arr []interface{}) (string, bool) {
	if len(arr) == 0 {
		return "", false
	}

	// Gather headers from the first map object
	firstObj, ok := arr[0].(map[string]interface{})
	if !ok {
		return "", false
	}

	var headers []string
	for k := range firstObj {
		// Omit common bloat metadata keys
		if k != "attributes" && k != "__typename" {
			headers = append(headers, k)
		}
	}
	sort.Strings(headers)

	if len(headers) == 0 {
		return "", false
	}

	var lines []string
	lines = append(lines, strings.Join(headers, "\t"))

	for _, item := range arr {
		obj, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		var row []string
		for _, h := range headers {
			val := obj[h]
			// Basic formatting of inner maps or lists to avoid brackets
			valStr := fmt.Sprintf("%v", val)
			valStr = strings.ReplaceAll(valStr, "\n", " ")
			valStr = strings.ReplaceAll(valStr, "\t", " ")
			row = append(row, valStr)
		}
		lines = append(lines, strings.Join(row, "\t"))
	}

	return strings.Join(lines, "\n"), true
}

func flattenMap(input map[string]interface{}, output map[string]interface{}, prefix string, depth int) {
	if depth > 3 {
		// Stop nested tracking beyond 3 hops
		return
	}
	for k, v := range input {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}

		if childMap, ok := v.(map[string]interface{}); ok {
			flattenMap(childMap, output, key, depth+1)
		} else {
			output[key] = v
		}
	}
}

func createCacheEnvelope(payload string) (string, CacheEnvelope) {
	cacheID := NewCacheID()

	var rawData interface{}
	_ = json.Unmarshal([]byte(payload), &rawData)

	envelope := CacheEnvelope{
		CacheID:      cacheID,
		DataType:     "object",
		RootPath:     ".",
		FieldTypes:   make(map[string]string),
		SampleRecord: make(map[string]interface{}),
		EnumValues:   make(map[string][]string),
	}

	if arr, ok := rawData.([]interface{}); ok && len(arr) > 0 {
		envelope.DataType = "array"
		envelope.RootPath = ".records"

		fieldValues := make(map[string]map[string]bool)
		if first, ok := arr[0].(map[string]interface{}); ok {
			for k, v := range first {
				envelope.Fields = append(envelope.Fields, k)
				envelope.FieldTypes[k] = fmt.Sprintf("%T", v)
				fieldValues[k] = make(map[string]bool)
			}
			envelope.SampleRecord = first
		}

		for _, record := range arr {
			if recMap, ok := record.(map[string]interface{}); ok {
				for k, v := range recMap {
					if strVal, ok := v.(string); ok && strVal != "" {
						if _, exists := fieldValues[k]; !exists {
							fieldValues[k] = make(map[string]bool)
						}
						fieldValues[k][strVal] = true
					}
				}
			}
		}

		for k, valSet := range fieldValues {
			if len(valSet) > 0 && len(valSet) <= 5 {
				var uniqueVals []string
				for val := range valSet {
					uniqueVals = append(uniqueVals, val)
				}
				envelope.EnumValues[k] = uniqueVals
			}
		}
	} else if mapData, ok := rawData.(map[string]interface{}); ok {
		var records []interface{}
		var recordsKey string
		for k, v := range mapData {
			if arr, ok := v.([]interface{}); ok {
				records = arr
				recordsKey = k
				break
			}
		}

		if len(records) > 0 {
			envelope.DataType = "array"
			envelope.RootPath = "." + recordsKey

			fieldValues := make(map[string]map[string]bool)
			if first, ok := records[0].(map[string]interface{}); ok {
				for k, v := range first {
					envelope.Fields = append(envelope.Fields, k)
					envelope.FieldTypes[k] = fmt.Sprintf("%T", v)
					fieldValues[k] = make(map[string]bool)
				}
				envelope.SampleRecord = first
			}

			for _, record := range records {
				if recMap, ok := record.(map[string]interface{}); ok {
					for k, v := range recMap {
						if strVal, ok := v.(string); ok && strVal != "" {
							if _, exists := fieldValues[k]; !exists {
								fieldValues[k] = make(map[string]bool)
							}
							fieldValues[k][strVal] = true
						}
					}
				}
			}

			for k, valSet := range fieldValues {
				if len(valSet) > 0 && len(valSet) <= 5 {
					var uniqueVals []string
					for val := range valSet {
						uniqueVals = append(uniqueVals, val)
					}
					envelope.EnumValues[k] = uniqueVals
				}
			}
		} else {
			for k, v := range mapData {
				envelope.Fields = append(envelope.Fields, k)
				envelope.FieldTypes[k] = fmt.Sprintf("%T", v)
			}
			envelope.SampleRecord = mapData
		}
	}

	// P0 Fix: Sanitize column names in the envelope to match the SQL table.
	// MaterializeTable applies sanitizeColumnName (which strips non-alphanumeric
	// chars like '?'), but the envelope was reporting the original raw names.
	// This mismatch caused the model to generate SQL like:
	//   SELECT * FROM cache_... WHERE Target_Account? = 'Yes'
	// which fails because the column is actually Target_Account_ in SQLite.
	envelope = sanitizeEnvelopeFieldNames(envelope)

	// Truncate long values in sample record and enum values to reduce envelope size.
	// Wide CSVs with 30+ columns and long text fields can produce 10K+ char envelopes
	// that overflow the router's context window when injected as tool output.
	envelope = truncateEnvelopeValues(envelope)

	envJSON, _ := json.MarshalIndent(envelope, "", "  ")
	return string(envJSON), envelope
}

// truncateEnvelopeValues caps sample record string values at maxSampleValueLen
// and limits enum value entries to maxEnumEntries per field. This reduces the
// envelope size for wide CSVs without losing schema discovery value.
func truncateEnvelopeValues(env CacheEnvelope) CacheEnvelope {
	const maxSampleValueLen = 100
	const maxEnumEntries = 3

	// Truncate sample record values
	for k, v := range env.SampleRecord {
		if s, ok := v.(string); ok && len(s) > maxSampleValueLen {
			env.SampleRecord[k] = s[:maxSampleValueLen] + "..."
		}
	}

	// Cap enum value lists
	for k, vals := range env.EnumValues {
		if len(vals) > maxEnumEntries {
			env.EnumValues[k] = vals[:maxEnumEntries]
		}
		// Also truncate individual enum values
		for i, v := range env.EnumValues[k] {
			if len(v) > maxSampleValueLen {
				env.EnumValues[k][i] = v[:maxSampleValueLen] + "..."
			}
		}
	}

	return env
}
