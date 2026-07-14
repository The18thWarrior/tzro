package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileTabularFile_BasicCSV(t *testing.T) {
	// Create a small CSV fixture
	csv := "name,age,active\nAlice,30,true\nBob,25,false\nCharlie,35,true\n"
	tmpDir := t.TempDir()
	csvPath := filepath.Join(tmpDir, "test.csv")
	if err := os.WriteFile(csvPath, []byte(csv), 0644); err != nil {
		t.Fatal(err)
	}

	profile, err := ProfileTabularFile(csvPath)
	if err != nil {
		t.Fatalf("ProfileTabularFile failed: %v", err)
	}

	if profile.Format != "csv" {
		t.Errorf("Format = %q, want %q", profile.Format, "csv")
	}
	if profile.RowCount != 3 {
		t.Errorf("RowCount = %d, want 3", profile.RowCount)
	}
	if profile.ColumnCount != 3 {
		t.Errorf("ColumnCount = %d, want 3", profile.ColumnCount)
	}
	if profile.Path != csvPath {
		t.Errorf("Path = %q, want %q", profile.Path, csvPath)
	}
	if len(profile.Columns) != 3 {
		t.Fatalf("len(Columns) = %d, want 3", len(profile.Columns))
	}

	// Check column names
	wantNames := []string{"name", "age", "active"}
	for i, want := range wantNames {
		if profile.Columns[i].Name != want {
			t.Errorf("Columns[%d].Name = %q, want %q", i, profile.Columns[i].Name, want)
		}
	}

	// Check column types
	if profile.Columns[0].Type != "string" {
		t.Errorf("name column Type = %q, want %q", profile.Columns[0].Type, "string")
	}
	if profile.Columns[1].Type != "integer" {
		t.Errorf("age column Type = %q, want %q", profile.Columns[1].Type, "integer")
	}
	if profile.Columns[2].Type != "boolean" {
		t.Errorf("active column Type = %q, want %q", profile.Columns[2].Type, "boolean")
	}

	// Sample rows should be non-empty TSV
	if profile.SampleRows == "" {
		t.Error("SampleRows should not be empty")
	}

	// CacheID should be set
	if profile.CacheID == "" {
		t.Error("CacheID should not be empty")
	}

	// FileSizeBytes should be positive
	if profile.FileSizeBytes <= 0 {
		t.Errorf("FileSizeBytes = %d, want > 0", profile.FileSizeBytes)
	}
}

// Slice 2: TSV profiling
func TestProfileTabularFile_TSV(t *testing.T) {
	tsv := "id\tname\tscore\n1\tAlice\t95.5\n2\tBob\t87.3\n"
	tmpDir := t.TempDir()
	tsvPath := filepath.Join(tmpDir, "test.tsv")
	if err := os.WriteFile(tsvPath, []byte(tsv), 0644); err != nil {
		t.Fatal(err)
	}

	profile, err := ProfileTabularFile(tsvPath)
	if err != nil {
		t.Fatalf("ProfileTabularFile failed: %v", err)
	}

	if profile.Format != "tsv" {
		t.Errorf("Format = %q, want %q", profile.Format, "tsv")
	}
	if profile.Delimiter != "\t" {
		t.Errorf("Delimiter = %q, want tab", profile.Delimiter)
	}
	if profile.RowCount != 2 {
		t.Errorf("RowCount = %d, want 2", profile.RowCount)
	}
	if profile.ColumnCount != 3 {
		t.Errorf("ColumnCount = %d, want 3", profile.ColumnCount)
	}
	// score should be float
	if profile.Columns[2].Type != "float" {
		t.Errorf("score Type = %q, want %q", profile.Columns[2].Type, "float")
	}
}

// Slice 3: Delimiter sniffing
func TestDetectDelimiter(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"comma", "a,b,c\n1,2,3\n4,5,6\n", ","},
		{"semicolon", "a;b;c\n1;2;3\n4;5;6\n", ";"},
		{"pipe", "a|b|c\n1|2|3\n4|5|6\n", "|"},
		{"tab", "a\tb\tc\n1\t2\t3\n4\t5\t6\n", "\t"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			p := filepath.Join(tmpDir, "test.csv")
			if err := os.WriteFile(p, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}
			got := detectDelimiter(p)
			if got != tt.want {
				t.Errorf("detectDelimiter = %q, want %q", got, tt.want)
			}
		})
	}
}

// Slice 4: Type inference
func TestTypeInference(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{"integers", []string{"1", "2", "3"}, "integer"},
		{"floats", []string{"1.5", "2.7", "3.9"}, "float"},
		{"mixed int float", []string{"1", "2.5", "3"}, "float"},
		{"booleans", []string{"true", "false", "true"}, "boolean"},
		{"strings", []string{"hello", "world"}, "string"},
		{"mixed string int", []string{"hello", "42"}, "string"},
		{"mixed bool int", []string{"true", "42"}, "mixed"},
		{"empty", []string{}, "string"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ti typeInference
			for _, v := range tt.values {
				ti.observe(v)
			}
			got := ti.result()
			if got != tt.want {
				t.Errorf("typeInference.result() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Slice 5: Null rate calculation
func TestProfileTabularFile_NullRate(t *testing.T) {
	csv := "name,value\nAlice,100\n,200\nCharlie,\n,\n"
	tmpDir := t.TempDir()
	csvPath := filepath.Join(tmpDir, "test.csv")
	if err := os.WriteFile(csvPath, []byte(csv), 0644); err != nil {
		t.Fatal(err)
	}

	profile, err := ProfileTabularFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}

	// name column: 2/4 rows are null
	if profile.Columns[0].NullRate != 0.5 {
		t.Errorf("name NullRate = %f, want 0.5", profile.Columns[0].NullRate)
	}
	// value column: 2/4 rows are null
	if profile.Columns[1].NullRate != 0.5 {
		t.Errorf("value NullRate = %f, want 0.5", profile.Columns[1].NullRate)
	}
}

// Slice 6: Cardinality cap
func TestProfileTabularFile_CardinalityCap(t *testing.T) {
	// Create a CSV with >1000 unique values in one column
	var lines []string
	lines = append(lines, "id,category")
	for i := 0; i < 1100; i++ {
		lines = append(lines, fmt.Sprintf("val_%d,cat_%d", i, i%5))
	}
	content := strings.Join(lines, "\n")

	tmpDir := t.TempDir()
	csvPath := filepath.Join(tmpDir, "big.csv")
	if err := os.WriteFile(csvPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	profile, err := ProfileTabularFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}

	// id column should be capped at ">1000"
	idCard := profile.Columns[0].Cardinality
	if idCard != ">1000" {
		t.Errorf("id Cardinality = %v, want \">1000\"", idCard)
	}

	// category column should have cardinality 5 and be "enum"
	catCard, ok := profile.Columns[1].Cardinality.(int)
	if !ok || catCard != 5 {
		t.Errorf("category Cardinality = %v, want 5", profile.Columns[1].Cardinality)
	}
	if profile.Columns[1].Type != "enum" {
		t.Errorf("category Type = %q, want %q", profile.Columns[1].Type, "enum")
	}
	if len(profile.Columns[1].Values) != 5 {
		t.Errorf("category Values count = %d, want 5", len(profile.Columns[1].Values))
	}
}

// Slice 7: Numeric min/max
func TestProfileTabularFile_NumericMinMax(t *testing.T) {
	csv := "name,score\nAlice,95.5\nBob,87.3\nCharlie,100.0\n"
	tmpDir := t.TempDir()
	csvPath := filepath.Join(tmpDir, "test.csv")
	if err := os.WriteFile(csvPath, []byte(csv), 0644); err != nil {
		t.Fatal(err)
	}

	profile, err := ProfileTabularFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}

	scoreCol := profile.Columns[1]
	if scoreCol.Min == nil || *scoreCol.Min != 87.3 {
		t.Errorf("score Min = %v, want 87.3", scoreCol.Min)
	}
	if scoreCol.Max == nil || *scoreCol.Max != 100.0 {
		t.Errorf("score Max = %v, want 100.0", scoreCol.Max)
	}
}

// Slice 8: Adaptive sample sizing
func TestBuildSampleRows_AdaptiveSizing(t *testing.T) {
	// Create very wide headers that would exceed 10K when combined
	numCols := 200
	headers := make([]string, numCols)
	for i := range headers {
		headers[i] = fmt.Sprintf("column_%d_with_a_long_name", i)
	}

	// Create 5 rows of data
	makeRow := func() []string {
		row := make([]string, numCols)
		for i := range row {
			row[i] = "some_value_that_is_moderately_long"
		}
		return row
	}

	firstRows := [][]string{makeRow(), makeRow(), makeRow()}
	reservoir := [][]string{makeRow(), makeRow()}
	columns := make([]ColumnProfile, numCols)
	for i := range columns {
		columns[i] = ColumnProfile{Name: headers[i], Type: "string", Cardinality: 100}
	}

	result := buildSampleRows(headers, firstRows, reservoir, columns, 1000)

	if len(result) > sampleBudget {
		t.Errorf("sample rows exceed budget: %d > %d", len(result), sampleBudget)
	}
	if result == "" {
		t.Error("sample rows should not be empty")
	}
}

// Slice 9: Column pruning (high null, constant, high-cardinality string)
func TestComputePrunedColumns(t *testing.T) {
	columns := []ColumnProfile{
		{Name: "id", Type: "string", NullRate: 0.0, Cardinality: ">1000"},       // high cardinality string → prune
		{Name: "status", Type: "string", NullRate: 0.0, Cardinality: 3},         // low cardinality → keep
		{Name: "notes", Type: "string", NullRate: 0.95, Cardinality: 5},         // >90% null → prune
		{Name: "constant", Type: "string", NullRate: 0.0, Cardinality: 1},       // single value → prune
		{Name: "score", Type: "integer", NullRate: 0.0, Cardinality: 100},       // numeric → keep
	}

	pruned := computePrunedColumns(columns, 1000)

	if !pruned[0] {
		t.Error("high-cardinality string column should be pruned")
	}
	if pruned[1] {
		t.Error("low-cardinality string column should NOT be pruned")
	}
	if !pruned[2] {
		t.Error("high-null column should be pruned")
	}
	if !pruned[3] {
		t.Error("constant column should be pruned")
	}
	if pruned[4] {
		t.Error("numeric column should NOT be pruned")
	}
}

// Slice 10: Encoding detection (UTF-8 BOM)
func TestProfileTabularFile_UTF8BOM(t *testing.T) {
	// UTF-8 BOM followed by CSV content
	bom := "\xEF\xBB\xBF"
	csv := bom + "name,value\nAlice,100\nBob,200\n"
	tmpDir := t.TempDir()
	csvPath := filepath.Join(tmpDir, "bom.csv")
	if err := os.WriteFile(csvPath, []byte(csv), 0644); err != nil {
		t.Fatal(err)
	}

	profile, err := ProfileTabularFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}

	// BOM should be stripped from first column name
	if profile.Columns[0].Name != "name" {
		t.Errorf("first column name = %q, want %q (BOM not stripped?)", profile.Columns[0].Name, "name")
	}
	if profile.RowCount != 2 {
		t.Errorf("RowCount = %d, want 2", profile.RowCount)
	}
}

// Slice 11: Large JSON array profiling
func TestShouldProfileJSON(t *testing.T) {
	tmpDir := t.TempDir()

	// Small JSON: ≤200 lines AND ≤10KB → should NOT profile
	smallJSON := filepath.Join(tmpDir, "small.json")
	if err := os.WriteFile(smallJSON, []byte(`[{"a": 1}, {"a": 2}]`), 0644); err != nil {
		t.Fatal(err)
	}
	if ShouldProfileJSON(smallJSON) {
		t.Error("small JSON should NOT be profiled")
	}

	// Large JSON: >10KB → should profile
	var bigLines []string
	for i := 0; i < 50; i++ {
		bigLines = append(bigLines, fmt.Sprintf(`{"id": %d, "data": "%s"}`, i, strings.Repeat("x", 200)))
	}
	bigJSON := filepath.Join(tmpDir, "big.json")
	if err := os.WriteFile(bigJSON, []byte("["+strings.Join(bigLines, ",")+"]"), 0644); err != nil {
		t.Fatal(err)
	}
	if !ShouldProfileJSON(bigJSON) {
		t.Error("large JSON (>10KB) should be profiled")
	}
}

// Slice 12: Small JSON passthrough (tested via ShouldProfileJSON above)

// Slice 13: Reservoir sampling
func TestProfileTabularFile_ReservoirSampling(t *testing.T) {
	// Create a CSV with enough rows to trigger reservoir sampling
	var lines []string
	lines = append(lines, "id,value")
	for i := 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprintf("%d,%d", i, i*10))
	}
	content := strings.Join(lines, "\n")

	tmpDir := t.TempDir()
	csvPath := filepath.Join(tmpDir, "reservoir.csv")
	if err := os.WriteFile(csvPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	profile, err := ProfileTabularFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}

	if profile.RowCount != 50 {
		t.Errorf("RowCount = %d, want 50", profile.RowCount)
	}

	// Sample rows should contain header + data rows
	sampleLines := strings.Split(profile.SampleRows, "\n")
	if len(sampleLines) < 2 {
		t.Errorf("SampleRows should have at least header + 1 data row, got %d lines", len(sampleLines))
	}
	// First line should be header
	if !strings.Contains(sampleLines[0], "id") {
		t.Errorf("SampleRows header = %q, should contain 'id'", sampleLines[0])
	}
}

// Slice 10 additional: IsTabularExtension
func TestIsTabularExtension(t *testing.T) {
	tests := []struct {
		ext  string
		want bool
	}{
		{".csv", true},
		{".tsv", true},
		{".xlsx", true},
		{".xls", true},
		{".json", false},
		{".go", false},
		{".CSV", true}, // case insensitive
	}
	for _, tt := range tests {
		if got := IsTabularExtension(tt.ext); got != tt.want {
			t.Errorf("IsTabularExtension(%q) = %v, want %v", tt.ext, got, tt.want)
		}
	}
}

// Test CSV quoting edge case
func TestParseCSVLine(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		want  []string
	}{
		{"simple", "a,b,c", []string{"a", "b", "c"}},
		{"quoted", `"hello, world",b,c`, []string{"hello, world", "b", "c"}},
		{"escaped quotes", `"he said ""hi""",b`, []string{`he said "hi"`, "b"}},
		{"empty fields", "a,,c", []string{"a", "", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCSVLine(tt.line)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
