package compactor

import (
	"os"
	"strings"
	"testing"
)

func TestDetectTabular_CSV(t *testing.T) {
	input := "id,name,role,salary\n1,Alice,admin,100000\n2,Bob,user,80000\n3,Charlie,dev,90000\n"

	td, ok := DetectTabular(input)
	if !ok {
		t.Fatal("expected CSV to be detected as tabular")
	}
	if td.Format != "csv" {
		t.Errorf("expected format csv, got %s", td.Format)
	}
	if len(td.Columns) != 4 {
		t.Errorf("expected 4 columns, got %d", len(td.Columns))
	}
	if len(td.Rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(td.Rows))
	}
	if td.Columns[0] != "id" || td.Columns[1] != "name" {
		t.Errorf("unexpected column names: %v", td.Columns)
	}
}

func TestDetectTabular_TSV(t *testing.T) {
	input := "id\tname\trole\n1\tAlice\tadmin\n2\tBob\tuser\n3\tCharlie\tdev\n"

	td, ok := DetectTabular(input)
	if !ok {
		t.Fatal("expected TSV to be detected as tabular")
	}
	if td.Format != "tsv" {
		t.Errorf("expected format tsv, got %s", td.Format)
	}
	if len(td.Columns) != 3 {
		t.Errorf("expected 3 columns, got %d", len(td.Columns))
	}
}

func TestDetectTabular_JSONArray(t *testing.T) {
	input := `[
		{"id": 1, "name": "Alice", "role": "admin"},
		{"id": 2, "name": "Bob", "role": "user"},
		{"id": 3, "name": "Charlie", "role": "developer"}
	]`

	td, ok := DetectTabular(input)
	if !ok {
		t.Fatal("expected JSON array to be detected as tabular")
	}
	if td.Format != "json" {
		t.Errorf("expected format json, got %s", td.Format)
	}
	if len(td.Rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(td.Rows))
	}
}

func TestDetectTabular_NotTabular(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"plain text", "Hello world, this is just text."},
		{"single JSON object", `{"key": "value"}`},
		{"empty", ""},
		{"single row CSV", "a,b,c\n1,2,3\n"},
		{"inconsistent columns", "a,b,c\n1,2\n3\n4,5,6,7\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := DetectTabular(tc.input)
			if ok {
				t.Errorf("expected %q to NOT be detected as tabular", tc.name)
			}
		})
	}
}

func TestShouldIntercept_FileRead_AlwaysTrue(t *testing.T) {
	td := &TabularData{
		Columns:     []string{"a", "b"},
		Rows:        [][]string{{"1", "2"}, {"3", "4"}},
		SourceBytes: 20, // tiny
	}
	if !ShouldIntercept(td, true, 4096) {
		t.Error("file reads should always intercept tabular data, regardless of size")
	}
}

func TestShouldIntercept_ExternalTool_BelowThreshold(t *testing.T) {
	td := &TabularData{
		Columns:     []string{"a", "b"},
		Rows:        [][]string{{"1", "2"}, {"3", "4"}},
		SourceBytes: 100,
	}
	if ShouldIntercept(td, false, 4096) {
		t.Error("external tool output below threshold should not be intercepted")
	}
}

func TestShouldIntercept_ExternalTool_AboveThreshold(t *testing.T) {
	td := &TabularData{
		Columns:     []string{"a", "b"},
		Rows:        [][]string{{"1", "2"}, {"3", "4"}},
		SourceBytes: 5000,
	}
	if !ShouldIntercept(td, false, 4096) {
		t.Error("external tool output above threshold should be intercepted")
	}
}

func TestGetThreshold_Default(t *testing.T) {
	os.Unsetenv("TZRO_TABULAR_THRESHOLD")
	if GetThreshold() != DefaultTabularThreshold {
		t.Errorf("expected default threshold %d", DefaultTabularThreshold)
	}
}

func TestGetThreshold_EnvVar(t *testing.T) {
	os.Setenv("TZRO_TABULAR_THRESHOLD", "8192")
	defer os.Unsetenv("TZRO_TABULAR_THRESHOLD")
	if GetThreshold() != 8192 {
		t.Errorf("expected threshold 8192, got %d", GetThreshold())
	}
}

func TestIsFileReadTool(t *testing.T) {
	fileReadTools := []string{"view_file", "read_file", "ReadFile", "ViewFile", "Read"}
	for _, tool := range fileReadTools {
		if !IsFileReadTool(tool) {
			t.Errorf("expected %q to be recognized as file read tool", tool)
		}
	}

	nonFileTools := []string{"run_command", "Bash", "grep_search", "write_to_file", ""}
	for _, tool := range nonFileTools {
		if IsFileReadTool(tool) {
			t.Errorf("expected %q to NOT be recognized as file read tool", tool)
		}
	}
}

func TestFormatEnvelope(t *testing.T) {
	td := &TabularData{
		Columns:     []string{"id", "name", "role"},
		Rows:        [][]string{{"1", "Alice", "admin"}, {"2", "Bob", "user"}, {"3", "Charlie", "dev"}},
		Format:      "csv",
		SourceBytes: 100,
	}

	envelope := FormatEnvelope("tbl_abc123", td, 2)
	if !strings.Contains(envelope, "3 rows, 3 cols") {
		t.Errorf("expected row/col count in envelope")
	}
	if !strings.Contains(envelope, "tbl_abc123") {
		t.Errorf("expected table name in envelope")
	}
	if !strings.Contains(envelope, "Alice") {
		t.Errorf("expected sample row data in envelope")
	}
	if !strings.Contains(envelope, "tzro query") {
		t.Errorf("expected query hint in envelope")
	}
}
