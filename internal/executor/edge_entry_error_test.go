package executor

import "testing"

func TestIsUninformativeToolError_HallucinatedCacheId(t *testing.T) {
	// Model hallucinated cache_1784607195509971000 — not in any upstream output
	upstreamContext := "## Analysis results\nTotal rows: 150\nColumns: name, email, status"
	toolArgs := `{"cacheId":"cache_1784607195509971000","sql":"SELECT * FROM cache_1784607195509971000"}`
	errorOutput := `{"success":false,"error":"table not found: cache_1784607195509971000"}`

	if !IsUninformativeToolError("sql_cached_data", toolArgs, errorOutput, upstreamContext) {
		t.Fatal("expected uninformative for hallucinated cacheId + table not found")
	}
}

func TestIsUninformativeToolError_RealCacheId(t *testing.T) {
	// The cacheId WAS in upstream output — error is informative
	upstreamContext := "Schema for cache_1784607195509971000:\n| Column | Type |\n| name | TEXT |"
	toolArgs := `{"cacheId":"cache_1784607195509971000","sql":"SELECT * FROM cache_1784607195509971000"}`
	errorOutput := `{"success":false,"error":"table not found: cache_1784607195509971000"}`

	if IsUninformativeToolError("sql_cached_data", toolArgs, errorOutput, upstreamContext) {
		t.Fatal("expected informative for real cacheId that happens to fail")
	}
}

func TestIsUninformativeToolError_IntrospectCache_Hallucinated(t *testing.T) {
	upstreamContext := "No cache data available"
	toolArgs := `{"cacheId":"cache_9999999999999"}`
	errorOutput := `{"success":false,"error":"cache not found"}`

	if !IsUninformativeToolError("introspect_cache", toolArgs, errorOutput, upstreamContext) {
		t.Fatal("expected uninformative for hallucinated introspect_cache cacheId")
	}
}

func TestIsUninformativeToolError_NonMatchingErrorPattern(t *testing.T) {
	// Error is NOT a "not found" pattern — could be a permissions issue, etc.
	upstreamContext := "Some unrelated context"
	toolArgs := `{"cacheId":"cache_1234"}`
	errorOutput := `{"success":false,"error":"permission denied"}`

	if IsUninformativeToolError("sql_cached_data", toolArgs, errorOutput, upstreamContext) {
		t.Fatal("expected informative for non-matching error pattern (permission denied)")
	}
}

func TestIsUninformativeToolError_UnknownTool(t *testing.T) {
	// Tools without key parameter extraction always return false (informative)
	upstreamContext := "Some context"
	toolArgs := `{"query":"test"}`
	errorOutput := `{"success":false,"error":"not found"}`

	if IsUninformativeToolError("web_search", toolArgs, errorOutput, upstreamContext) {
		t.Fatal("expected informative for unknown tool (can't extract key parameter)")
	}
}

func TestIsUninformativeToolError_ReadFile_Hallucinated(t *testing.T) {
	upstreamContext := "Files: main.go, config.go"
	toolArgs := `{"path":"/nonexistent/imaginary/file.go"}`
	errorOutput := `{"success":false,"error":"no such file or directory"}`

	if !IsUninformativeToolError("read_file", toolArgs, errorOutput, upstreamContext) {
		t.Fatal("expected uninformative for hallucinated file path")
	}
}

func TestIsUninformativeToolError_ReadFile_Real(t *testing.T) {
	upstreamContext := "Found files: /nonexistent/imaginary/file.go, /other/file.go"
	toolArgs := `{"path":"/nonexistent/imaginary/file.go"}`
	errorOutput := `{"success":false,"error":"no such file or directory"}`

	if IsUninformativeToolError("read_file", toolArgs, errorOutput, upstreamContext) {
		t.Fatal("expected informative for real file path that happens to be missing")
	}
}
